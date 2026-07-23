package main

import (
	"bytes"
	"testing"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

// closeOrderPinger wraps fakePinger and records whether Close() was ever
// invoked before Wait() had returned. stopPinger() is expected to call
// Stop() → Wait() → Close() in that order (the F1 fix); this is a
// deterministic check, not timing-based, since fakePinger's Wait()/Close()
// just set flags synchronously.
type closeOrderPinger struct {
	*fakePinger
	closeBeforeWaitViolation bool
}

func (o *closeOrderPinger) Close() {
	if !o.waited {
		o.closeBeforeWaitViolation = true
	}
	o.fakePinger.Close()
}

// TestRunStop_ClosesPingerAfterWait verifies the F1 fix end-to-end: OnStop
// (→ stopAll → stopPinger) must call Close() on the pinger, and only after
// Wait() has already returned. Before the fix, stopPinger never called
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

	base := &fakePinger{}
	tracker := &closeOrderPinger{fakePinger: base}
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

	if !tracker.stopCalled {
		t.Error("expected Stop() to be called")
	}
	if !tracker.waited {
		t.Error("expected Wait() to be called")
	}
	if !tracker.closeCalled {
		t.Error("expected Close() to be called — the F1 fix")
	}
	if tracker.closeBeforeWaitViolation {
		t.Error("Close() was called before Wait() returned (expected Stop → Wait → Close ordering)")
	}
}

// TestPingerAdapter_MTRProber verifies the architecture-seam fix: the real
// *pingerAdapter satisfies pingerController.MTRProber() by wrapping its
// underlying *pinger.Pinger in a pingerMTRAdapter, without any downcast at
// the call site.
func TestPingerAdapter_MTRProber(t *testing.T) {
	adapter := &pingerAdapter{Pinger: pinger.NewPinger(nil)}
	prober := adapter.MTRProber()
	if prober == nil {
		t.Fatal("expected non-nil mtr.HopProber")
	}

	// Drive it through mtr.NewEngine to confirm it satisfies the real
	// interface contract end-to-end, not just type-checks.
	engine := mtr.NewEngine(prober, nil, mtr.Config{})
	engine.Start()
	engine.Stop()
}
