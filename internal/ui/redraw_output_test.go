package ui

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/nagayon-935/mping/internal/stats"
)

// countingTty is a tcell.Tty that discards terminal output while counting
// its bytes, so a test can measure what a redraw sends to the terminal.
type countingTty struct {
	mu            sync.Mutex
	written       int
	closed        chan struct{}
	once          sync.Once
	width, height int
}

func newCountingTty(width, height int) *countingTty {
	return &countingTty{closed: make(chan struct{}), width: width, height: height}
}

func (c *countingTty) Start() error        { return nil }
func (c *countingTty) Stop() error         { return nil }
func (c *countingTty) NotifyResize(func()) {}
func (c *countingTty) Close() error        { return c.Drain() }

// Drain releases the blocked Read so the screen's input loop can exit.
func (c *countingTty) Drain() error { c.once.Do(func() { close(c.closed) }); return nil }

func (c *countingTty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: c.width, Height: c.height}, nil
}

// Read blocks until Drain: the test never types anything.
func (c *countingTty) Read([]byte) (int, error) {
	<-c.closed
	return 0, fmt.Errorf("tty closed")
}

func (c *countingTty) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written += len(p)
	return len(p), nil
}

// take returns the bytes written since the last call.
func (c *countingTty) take() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.written
	c.written = 0
	return n
}

// newTerminalTUI builds the main layout for count replying targets on a real
// terminfo screen whose output goes to the returned countingTty.
func newTerminalTUI(t *testing.T, count, width, height int) (*tui, *countingTty) {
	t.Helper()
	t.Setenv("TERM", "xterm-256color")
	tty := newCountingTty(width, height)
	screen, err := tcell.NewTerminfoScreenFromTty(tty)
	if err != nil {
		t.Fatalf("terminfo screen: %v", err)
	}
	app := tview.NewApplication()
	app.SetScreen(newDiffScreen(screen)) // initializes the screen
	t.Cleanup(screen.Fini)

	orig := newApplication
	newApplication = func() *tview.Application { return app }
	t.Cleanup(func() { newApplication = orig })

	targets := make([]*stats.TargetStats, count)
	for i := range targets {
		targets[i] = stats.NewTargetStats(fmt.Sprintf("host-%02d.example.com", i))
		targets[i].SetIP(fmt.Sprintf("192.0.2.%d", i+1))
		for j := range 30 {
			targets[i].IncSent()
			targets[i].OnSuccess(time.Duration(1+(i+j)%7)*time.Millisecond, 64)
		}
	}
	ui := newTUI(RunOptions{
		Targets: targets, Interval: time.Second, Timeout: time.Second,
		SourceIPv4: "192.0.2.254", PacketSize: 56,
	})
	ui.session = newUISession()
	t.Cleanup(func() { ui.session.Stop(); ui.session.Wait() })
	ui.buildLayout()
	app.SetRoot(ui.root, true)
	return ui, tty
}

// TestRedrawSendsOnlyChangedCells guards the cost of a refresh: tview clears
// and repaints the whole screen buffer on every draw, and only the cells whose
// content really changed may reach the terminal (see diffScreen). With 40 hosts a full repaint
// is tens of kilobytes of escape sequences per draw.
func TestRedrawSendsOnlyChangedCells(t *testing.T) {
	ui, tty := newTerminalTUI(t, 40, 200, 60)

	ui.tr.update()
	ui.app.ForceDraw()
	ui.app.ForceDraw() // settle the layout-dependent second pass
	if first := tty.take(); first < 5000 {
		t.Fatalf("first frames sent %d bytes; expected a full screen of output", first)
	}

	ui.app.ForceDraw()
	if unchanged := tty.take(); unchanged > 200 {
		t.Errorf("redrawing an unchanged screen sent %d bytes, want at most 200", unchanged)
	}

	ui.targets[3].IncSent()
	ui.targets[3].OnSuccess(123*time.Millisecond, 64)
	ui.tr.update()
	ui.app.ForceDraw()
	if oneRow := tty.take(); oneRow == 0 || oneRow > 1500 {
		t.Errorf("redrawing after one host's sample sent %d bytes, want 1..1500", oneRow)
	}
}
