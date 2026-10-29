package main

import (
	"bytes"
	"testing"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// TestRunStop_ClosesPingerAfterWait verifies the F1 fix end-to-end: OnStop
// (→ stopAll → tearDownAll) must call Close() on the pinger, and only after
// Wait() has already returned. Before the fix, the stop path never called
// Close() at all — the pre-existing "closed" flag (still set by either
// Stop() or Close(), for backward compatibility with older assertions)
// couldn't distinguish that, since Stop() alone already set it.
func TestRunStop_ClosesPingerAfterWait(t *testing.T) {
	origPinger := newPinger
	origUI := uiRun
	t.Cleanup(func() {
		newPinger = origPinger
		uiRun = origUI
	})

	// tearDownAll() is expected to call Stop() -> Wait() -> Close() in that
	// order (the F1 fix). The onClose hook makes the ordering check
	// deterministic: it fires before Close() is recorded.
	tracker := &fakePinger{}
	closeBeforeWait := false
	tracker.onClose = func() { closeBeforeWait = closeBeforeWait || !tracker.Waited() }
	newPinger = func(targets []*stats.TargetStats, opts pinger.Options) pingerController {
		return tracker
	}
	uiRun = func(opts ui.RunOptions) error {
		opts.OnStop()
		return nil
	}

	var out, errOut bytes.Buffer
	code := run([]string{"-S", "10.0.0.2", "example.com"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("expected 0, got %d (err: %s)", code, errOut.String())
	}

	if !tracker.isReleased() {
		t.Error("expected Stop() and Close() to be called — Close() is the F1 fix")
	}
	if !tracker.Waited() {
		t.Error("expected Wait() to be called")
	}
	if closeBeforeWait {
		t.Error("Close() was called before Wait() returned (expected Stop → Wait → Close ordering)")
	}
}
