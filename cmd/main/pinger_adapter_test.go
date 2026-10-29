package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
)

func TestPingerAdapterSetters(t *testing.T) {
	p := &pingerAdapter{Pinger: pinger.NewPingerWithOptions(nil, pinger.Options{})}

	p.SetSource("1.2.3.4")
	if p.Source != "1.2.3.4" {
		t.Errorf("SetSource: got %q, want %q", p.Source, "1.2.3.4")
	}
	p.SetInterface("eth0")
	if p.Interface != "eth0" {
		t.Errorf("SetInterface: got %q, want %q", p.Interface, "eth0")
	}
	p.SetSize(128)
	if p.Size != 128 {
		t.Errorf("SetSize: got %d, want 128", p.Size)
	}
	p.SetCount(5)
	if p.Count != 5 {
		t.Errorf("SetCount: got %d, want 5", p.Count)
	}
	p.SetResolveInterval(30 * time.Second)
	if p.ResolveInterval != 30*time.Second {
		t.Errorf("SetResolveInterval: got %v, want 30s", p.ResolveInterval)
	}
	var buf bytes.Buffer
	p.SetLogWriter(&buf)
	if p.LogWriter != &buf {
		t.Error("SetLogWriter: writer not set correctly")
	}
}

// TestPingerAdapter_MTRProber verifies the architecture-seam fix: the real
// *pingerAdapter satisfies pingerController.MTRProber() by wrapping its
// underlying *pinger.Pinger in a pingerMTRAdapter, without any downcast at
// the call site.
func TestPingerAdapter_MTRProber(t *testing.T) {
	adapter := &pingerAdapter{Pinger: pinger.NewPingerWithOptions(nil, pinger.Options{})}
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
