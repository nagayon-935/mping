package ui

import (
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const sessionQueueSize = 32

// uiSession owns the callback worker and refresh goroutine for one Run.
// Operations execute in input order. UI updates are asynchronous, cancellable
// mailbox messages: producers never wait for an application that has stopped.
// Stop discards queued work; Wait joins the currently running callback and
// refresh loop. Callbacks must return, as RunOptions callbacks have no context.
type uiSession struct {
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
	tasks    chan func()
	updates  chan func()
	wakeKey  *tcell.EventKey
	screenMu sync.RWMutex
	screen   tcell.Screen
}

func newUISession() *uiSession {
	s := &uiSession{
		done: make(chan struct{}), tasks: make(chan func(), sessionQueueSize),
		updates: make(chan func(), sessionQueueSize),
		wakeKey: tcell.NewEventKey(tcell.KeyRune, 0, tcell.ModNone),
	}
	s.start(s.runCallbacks)
	return s
}

// start is used only during Run construction, before Wait is possible.
func (s *uiSession) start(f func()) {
	s.wg.Add(1)
	go func() { defer s.wg.Done(); f() }()
}

func (s *uiSession) Stop() { s.once.Do(func() { close(s.done) }) }
func (s *uiSession) Wait() { s.wg.Wait() }

func (s *uiSession) stopped() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Submit never blocks the input loop. A full queue is reported to the user
// by the caller; callbacks themselves can wait on supervisor commands.
func (s *uiSession) Submit(f func()) bool {
	if s.stopped() {
		return false
	}
	select {
	case <-s.done:
		return false
	case s.tasks <- f:
		return true
	default:
		return false
	}
}

func (s *uiSession) runCallbacks() {
	for {
		select {
		case <-s.done:
			return
		case f := <-s.tasks:
			if s.stopped() {
				return
			}
			f()
		}
	}
}

// Post can wait for mailbox space, but cancellation always releases it.
func (s *uiSession) Post(f func()) bool {
	if s.stopped() {
		return false
	}
	select {
	case <-s.done:
		return false
	case s.updates <- f:
		s.wake()
		return true
	}
}

func (s *uiSession) wake() {
	s.screenMu.RLock()
	screen := s.screen
	s.screenMu.RUnlock()
	if screen != nil {
		// PostEvent never blocks. If the screen queue is full, its pending
		// events will cause another draw, which retries this wake-up.
		_ = screen.PostEvent(s.wakeKey)
	}
}

// bind delivers mailbox updates on tview's input loop. The before-draw hook
// only remembers the active screen and wakes pending updates: tview holds its
// application lock there, so callbacks (which may Stop/SetFocus) run outside it.
// Pointer identity keeps the private wake key distinct from real user input.
func (s *uiSession) bind(app *tview.Application, input func(*tcell.EventKey) *tcell.EventKey) {
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		s.screenMu.Lock()
		s.screen = screen
		s.screenMu.Unlock()
		if !s.stopped() && len(s.updates) > 0 {
			s.wake()
		}
		return false
	})
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event != s.wakeKey {
			return input(event)
		}
		// Bound each delivery so busy producers cannot starve user input.
		for n := len(s.updates); n > 0 && !s.stopped(); n-- {
			f := <-s.updates
			f()
		}
		return nil
	})
}
