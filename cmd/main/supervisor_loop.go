package main

import "errors"

// errSupervisorShutDown is returned when the command loop is already gone.
// Callers treat it as "nothing to do" rather than a failure: it only happens
// once run() has torn the iteration down.
var errSupervisorShutDown = errors.New("supervisor shut down")

// cmdChanBuffer lets a burst of key presses enqueue without blocking the
// goroutines the tview key handler spawns.
const cmdChanBuffer = 16

// Start launches the command goroutine. Every supervisor mutation happens on
// it, which is what lets the state itself be unsynchronized.
func (s *supervisor) Start() {
	s.cmds = make(chan command, cmdChanBuffer)
	s.done = make(chan struct{})
	s.loopDone = make(chan struct{})
	go s.loop()
}

// Shutdown stops the command goroutine and waits for it to exit. Safe to
// call more than once: run()'s error path and its normal cleanup path can
// both reach it.
func (s *supervisor) Shutdown() {
	s.shutdownOnce.Do(func() { close(s.done) })
	<-s.loopDone
}

func (s *supervisor) loop() {
	defer close(s.loopDone)
	for {
		select {
		case <-s.done:
			return
		case c := <-s.cmds:
			err := s.handle(c)
			s.publish()
			if c.reply != nil {
				c.reply <- err // buffered by the sender; never blocks
			}
		}
	}
}

// do sends a command and waits for its result.
//
// The command channel is deliberately never closed. UI callbacks run on
// goroutines that can still be in flight after uiRun() returns, so closing
// would risk a send on a closed channel — the same class of bug this
// refactor exists to remove. Instead both the send and the reply wait select
// on s.done, so a late caller gets errSupervisorShutDown and returns.
//
// Waiting on the reply MUST also watch s.done: without it, a caller whose
// command was accepted just before Shutdown would block forever.
func (s *supervisor) do(k cmdKind) error {
	reply := make(chan error, 1)
	select {
	case s.cmds <- command{kind: k, reply: reply}:
	case <-s.done:
		return errSupervisorShutDown
	}
	select {
	case err := <-reply:
		return err
	case <-s.done:
		return errSupervisorShutDown
	}
}

// publish republishes the values read outside the command loop. httpResults()
// is called by the render loop every tick, so it cannot go through the queue.
func (s *supervisor) publish() {
	if s.httpChecker != nil {
		r := s.httpChecker.Results()
		s.httpSnap.Store(&r)
	} else {
		s.httpSnap.Store(nil)
	}
	if s.p != nil {
		p := s.p
		s.pingerSnap.Store(&p)
	}
	s.stateSnap.Store(int32(s.state))
}

// stateSnapshot returns the state as of the last processed command. Test-only:
// production code never reads state off the loop. Because do() waits for its
// reply, a stateSnapshot() call after a do() always observes that command.
func (s *supervisor) stateSnapshot() supervisorState {
	return supervisorState(s.stateSnap.Load())
}
