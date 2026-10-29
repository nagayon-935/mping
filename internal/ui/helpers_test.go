package ui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// testTimeout bounds every wait in the simulation-screen tests. It is only a
// failure deadline: the waits below return as soon as their condition holds.
const testTimeout = 3 * time.Second

// pollUntil re-checks cond until it holds or testTimeout passes. Use it for
// state that has no completion signal to wait on.
func pollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// recvWithin returns the next value from ch, failing the test if none
// arrives within testTimeout.
func recvWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// screenVisibleRow returns row y of the screen as text. It skips the
// continuation cells of wide characters so full-width text reads naturally.
func screenVisibleRow(screen tcell.Screen, y, width int) string {
	var b strings.Builder
	for x := 0; x < width; {
		r, combining, _, cells := screen.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
		for _, c := range combining {
			b.WriteRune(c)
		}
		x += max(1, cells)
	}
	return b.String()
}

// cellForeground returns the text color of a table cell. TableCell.Color is
// only used while the cell has the default style, which NewTableCell never
// leaves it with, so the color has to be read from the style.
func cellForeground(c *tview.TableCell) tcell.Color {
	fg, _, _ := c.Style.Decompose()
	return fg
}

// screenText returns every row of the screen joined by newlines.
func screenText(screen tcell.Screen) string {
	w, h := screen.Size()
	var b strings.Builder
	for y := range h {
		b.WriteString(screenVisibleRow(screen, y, w))
		b.WriteByte('\n')
	}
	return b.String()
}

// tuiHarness runs Run on a SimulationScreen and publishes every drawn frame
// as text. Tests inject keys and wait for markers on screen instead of
// sleeping: a wait returns the moment a matching frame is drawn.
//
// Frames queue in draw order, so a wait can match a frame drawn before the
// action it follows; pass an absent marker (or a stricter predicate) when a
// stale frame could satisfy the marker.
type tuiHarness struct {
	t      *testing.T
	app    *tview.Application
	screen tcell.SimulationScreen
	frames chan string
	done   chan error
	exited bool
}

// startTUI starts Run(opts) on a width x height simulation screen and waits
// for the first draw (that frame stays queued). Zero Interval, Timeout and PacketSize get defaults.
// The TUI is shut down at test cleanup if the test did not already do so.
func startTUI(t *testing.T, opts RunOptions, width, height int) *tuiHarness {
	t.Helper()
	if opts.Interval == 0 {
		opts.Interval = 50 * time.Millisecond
	}
	if opts.Timeout == 0 {
		opts.Timeout = time.Second
	}
	if opts.PacketSize == 0 {
		opts.PacketSize = 56
	}
	app := tview.NewApplication()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	app.SetScreen(screen)
	screen.SetSize(width, height)
	h := &tuiHarness{t: t, app: app, screen: screen, frames: make(chan string, 256), done: make(chan error, 1)}
	drawn := make(chan struct{})
	var drawnOnce sync.Once
	app.SetAfterDrawFunc(func(s tcell.Screen) {
		select {
		case h.frames <- screenText(s):
		default:
		}
		drawnOnce.Do(func() { close(drawn) })
	})

	orig := newApplication
	newApplication = func() *tview.Application { return app }
	go func() { h.done <- Run(opts) }()
	t.Cleanup(func() {
		if !h.exited {
			screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
			screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
			select {
			case <-h.done:
			case <-time.After(testTimeout):
			}
		}
		newApplication = orig
	})
	recvWithin(t, drawn, "the first frame")
	return h
}

// press injects a rune key.
func (h *tuiHarness) press(r rune) {
	h.screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
}

// key injects a special key.
func (h *tuiHarness) key(k tcell.Key) {
	h.screen.InjectKey(k, 0, tcell.ModNone)
}

// typeText injects s as consecutive rune keys.
func (h *tuiHarness) typeText(s string) {
	for _, r := range s {
		h.press(r)
	}
}

// waitFrame returns the first queued or future frame that satisfies ok,
// failing with the last frame seen if none does within testTimeout.
func (h *tuiHarness) waitFrame(what string, ok func(frame string) bool) string {
	h.t.Helper()
	timer := time.NewTimer(testTimeout)
	defer timer.Stop()
	last := ""
	for {
		select {
		case frame := <-h.frames:
			last = frame
			if ok(frame) {
				return frame
			}
		case <-timer.C:
			h.t.Fatalf("no frame with %s; last frame:\n%s", what, last)
		}
	}
}

// wait returns the first frame containing marker.
func (h *tuiHarness) wait(marker string) string {
	h.t.Helper()
	return h.waitFrame(marker, func(f string) bool { return strings.Contains(f, marker) })
}

// waitWithout returns the first frame containing marker but not absent.
func (h *tuiHarness) waitWithout(marker, absent string) string {
	h.t.Helper()
	return h.waitFrame(marker+" without "+absent, func(f string) bool {
		return strings.Contains(f, marker) && !strings.Contains(f, absent)
	})
}

// waitExit waits for Run to return and requires it to have succeeded.
func (h *tuiHarness) waitExit() {
	h.t.Helper()
	h.exited = true
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatalf("Run error: %v", err)
		}
	case <-time.After(testTimeout):
		h.t.Fatal("Run did not stop")
	}
}

// quit presses q and waits for Run to return.
func (h *tuiHarness) quit() {
	h.t.Helper()
	h.press('q')
	h.waitExit()
}

func TestInferInitialTTL(t *testing.T) {
	tests := []struct {
		name    string
		lastTTL int
		want    string
	}{
		{"zero", 0, "-"},
		{"negative", -1, "-"},
		{"linux one hop away", 63, "64"},
		{"linux at boundary", 64, "64"},
		{"windows one hop above linux boundary", 65, "128"},
		{"windows one hop away", 127, "128"},
		{"windows at boundary", 128, "128"},
		{"network device one hop above windows boundary", 129, "255"},
		{"network device at max", 255, "255"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferInitialTTL(tt.lastTTL); got != tt.want {
				t.Fatalf("inferInitialTTL(%d) = %q, want %q", tt.lastTTL, got, tt.want)
			}
		})
	}
}

func TestHopCountString(t *testing.T) {
	tests := []struct {
		name string
		hops []string
		want string
	}{
		{"nil", nil, "-"},
		{"empty", []string{}, "-"},
		{"tracing", []string{"Tracing..."}, "-"},
		{"no route", []string{"no route found"}, "-"},
		{"socket error", []string{"error: socket: operation not permitted"}, "-"},
		{"one hop", []string{"1.1.1.1"}, "1"},
		{"three hops", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}, "3"},
		{"with unreachable", []string{"1.1.1.1", "*", "8.8.8.8"}, "3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hopCountString(tt.hops); got != tt.want {
				t.Fatalf("hopCountString(%v) = %q, want %q", tt.hops, got, tt.want)
			}
		})
	}
}

func TestWrapHops(t *testing.T) {
	tests := []struct {
		name     string
		hops     []string
		maxWidth int
		want     []string
	}{
		{"nil hops", nil, 80, nil},
		{"empty hops", []string{}, 80, nil},
		{"zero maxWidth", []string{"1.1.1.1"}, 0, nil},
		{
			"all fit on one line",
			[]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			80,
			[]string{"1.1.1.1 -> 2.2.2.2 -> 3.3.3.3"},
		},
		{
			// "1.1.1.1 -> 2.2.2.2" = 18 chars fits, adding " -> 3.3.3.3" = 29 > 20
			"wraps at hop boundary",
			[]string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			20,
			[]string{"1.1.1.1 -> 2.2.2.2", "3.3.3.3"},
		},
		{
			// maxWidth too narrow for even two hops together: each hop on own line
			"each hop on its own line",
			[]string{"1.1.1.1", "2.2.2.2"},
			7,
			[]string{"1.1.1.1", "2.2.2.2"},
		},
		{
			// single hop wider than maxWidth is still returned as-is
			"single hop wider than maxWidth",
			[]string{"192.168.100.200"},
			5,
			[]string{"192.168.100.200"},
		},
		{
			// first hop wider than maxWidth, second hop fits its own line
			"long first hop forces each on own line",
			[]string{"192.168.100.200", "10.0.0.1"},
			5,
			[]string{"192.168.100.200", "10.0.0.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapHops(tt.hops, tt.maxWidth)
			if len(got) != len(tt.want) {
				t.Fatalf("wrapHops(%v, %d) = %v, want %v", tt.hops, tt.maxWidth, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("wrapHops(%v, %d)[%d] = %q, want %q", tt.hops, tt.maxWidth, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDisplaySourceIPForDst(t *testing.T) {
	tests := []struct {
		name     string
		dst      string
		src4     string
		src6     string
		expected string
	}{
		{"ipv4 destination uses ipv4 source", "8.8.8.8", "10.0.0.2", "2001:db8::2", "10.0.0.2"},
		{"ipv4 destination falls back to auto when no ipv4 source", "8.8.8.8", "", "2001:db8::1", "Auto"},
		{"ipv6 destination uses ipv6 source", "2001:db8::1", "", "2001:db8::cafe", "2001:db8::cafe"},
		{"ipv6 destination with zone uses ipv6 source", "fe80::1%en0", "10.0.0.2", "fe80::2%en0", "fe80::2%en0"},
		{"ipv6 zone is stripped before the family check", "fe80::1%eth0", "", "2001::cafe", "2001::cafe"},
		{"ipv6 destination falls back to auto when no ipv6 source", "2001:4860:4860::8888", "10.0.0.2", "", "Auto"},
		{"hostname uses ipv4 source", "hostname.local", "10.0.0.1", "", "10.0.0.1"},
		{"unparseable colon string is ipv6 and has no source", "not::valid::ip", "", "", "Auto"},
		{"unparseable colon string without ipv6 source is auto", "not::valid::ip", "10.0.0.1", "", "Auto"},
		{"unparseable colon string uses ipv6 source", "not::valid::ip", "10.0.0.1", "2001::cafe", "2001::cafe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displaySourceIPForDst(tt.dst, tt.src4, tt.src6)
			if got != tt.expected {
				t.Fatalf("displaySourceIPForDst(%q): got %q, want %q", tt.dst, got, tt.expected)
			}
		})
	}
}

func TestAppendErrorLog(t *testing.T) {
	view := tview.NewTextView()
	logs := []string{}
	appendErrorLog(&logs, view, "one")
	appendErrorLog(&logs, view, "two")
	if len(logs) != 2 {
		t.Fatalf("logs len: got %d", len(logs))
	}
	if !strings.Contains(view.GetText(false), "two") {
		t.Fatalf("expected latest log in text")
	}
}

func TestNormalizeWriteIP(t *testing.T) {
	msg := normalizeWriteIP("write ip 0.0.0.0->1.1.1.1: x", "10.0.0.2")
	if !strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("expected replaced source ip, got %q", msg)
	}
	msg = normalizeWriteIP("write ip 0.0.0.0->1.1.1.1: x", "Auto")
	if strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("unexpected replacement: %q", msg)
	}
	msg = normalizeWriteIP("other error", "10.0.0.2")
	if msg != "other error" {
		t.Fatalf("unexpected change: %q", msg)
	}
}

func TestBuildErrorLogMessage(t *testing.T) {
	view := stats.TargetView{Host: "example.com"}
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	msg := buildErrorLogMessage(view, "10.0.0.2", "write ip 0.0.0.0->1.1.1.1: x", ts)
	if !strings.Contains(msg, "example.com") || !strings.Contains(msg, "10.0.0.2") {
		t.Fatalf("unexpected msg: %q", msg)
	}
}

func TestUpdateAlertState(t *testing.T) {
	view := stats.TargetView{
		Host:    "example.com",
		LastRTT: 300 * time.Millisecond,
		Jitter:  60 * time.Millisecond,
	}
	state, msgs := updateAlertState(view, "10.0.0.2", 90.0, time.Now(), alertFlags{})
	if !state.lossRed || !state.rttRed || !state.jitterRed {
		t.Fatalf("expected alert flags set: %+v", state)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	clearView := stats.TargetView{Host: "example.com"}
	state2, msgs2 := updateAlertState(clearView, "10.0.0.2", 0.0, time.Now(), state)
	if state2.lossRed || state2.rttRed || state2.jitterRed {
		t.Fatalf("expected flags cleared: %+v", state2)
	}
	if len(msgs2) != 0 {
		t.Fatalf("expected no messages, got %d", len(msgs2))
	}
}

// ---- appendErrorLog truncation ----

func TestAppendErrorLog_Truncation(t *testing.T) {
	view := tview.NewTextView()
	logs := make([]string, 0)
	// Fill to exactly 1000 entries
	for range 1000 {
		logs = append(logs, "entry")
	}
	// Adding one more should trigger truncation (remove oldest entry)
	appendErrorLog(&logs, view, "new-entry")
	if len(logs) != 1000 {
		t.Errorf("expected 1000 after truncation, got %d", len(logs))
	}
	if logs[len(logs)-1] != "new-entry" {
		t.Errorf("last entry should be new-entry, got %q", logs[len(logs)-1])
	}
}
