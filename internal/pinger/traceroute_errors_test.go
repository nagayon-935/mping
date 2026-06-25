package pinger

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// traceFunc is the common signature of TraceRoute so the
// validation/error-path cases can be shared between both.
type traceFunc func(dest string, maxHops int, timeout time.Duration) ([]string, error)

func TestTraceRoute_ValidationAndResolveErrors(t *testing.T) {
	resolveErr := fmt.Errorf("boom")
	p := NewPingerWithOptions(nil, Options{
		ResolveIPAddr: func(network, address string) (*net.IPAddr, error) {
			return nil, resolveErr
		},
	})

	variants := map[string]traceFunc{
		"TraceRoute":      p.TraceRoute,
	}

	for name, fn := range variants {
		t.Run(name+"/empty dest", func(t *testing.T) {
			if _, err := fn("", 30, time.Second); err == nil {
				t.Error("expected error for empty dest")
			}
		})
		t.Run(name+"/non-positive maxHops", func(t *testing.T) {
			if _, err := fn("8.8.8.8", 0, time.Second); err == nil {
				t.Error("expected error for maxHops <= 0")
			}
		})
		t.Run(name+"/resolve failure", func(t *testing.T) {
			if _, err := fn("8.8.8.8", 30, time.Second); err == nil {
				t.Error("expected error when resolve fails")
			}
		})
	}
}
