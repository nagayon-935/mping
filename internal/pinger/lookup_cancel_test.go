package pinger

import (
	"context"
	"testing"
	"time"
)

// TestLookupsForContext_StopWhenCallerCancels verifies the MTR hop annotator
// can abandon a slow ASN or PTR lookup: the call returns once the caller's
// context is cancelled, and the abandoned result is not cached.
func TestLookupsForContext_StopWhenCallerCancels(t *testing.T) {
	blockUntilCancelled := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	tests := []struct {
		name   string
		opts   Options
		lookup func(p *Pinger, ctx context.Context) string
		cached func(p *Pinger) bool
	}{
		{
			name: "ASN",
			opts: Options{LookupTXTContext: func(ctx context.Context, _ string) ([]string, error) { return nil, blockUntilCancelled(ctx) }},
			lookup: func(p *Pinger, ctx context.Context) string {
				return p.GetASNInfoForContext(ctx, "192.0.2.1").Number
			},
			cached: func(p *Pinger) bool { _, ok := p.asnCache["192.0.2.1"]; return ok },
		},
		{
			name: "PTR",
			opts: Options{LookupAddrContext: func(ctx context.Context, _ string) ([]string, error) { return nil, blockUntilCancelled(ctx) }},
			lookup: func(p *Pinger, ctx context.Context) string {
				return p.GetPTRForContext(ctx, "192.0.2.1")
			},
			cached: func(p *Pinger) bool { _, ok := p.ptrCache["192.0.2.1"]; return ok },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			p := NewPingerWithOptions(nil, tt.opts)
			p.AsnEnabled, p.PtrEnabled = true, true
			p.asnJitter = func() time.Duration { return 0 }
			p.ptrJitter = func() time.Duration { return 0 }
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan string, 1)

			// Act
			go func() { done <- tt.lookup(p, ctx) }()
			time.Sleep(20 * time.Millisecond)
			cancel()

			// Assert
			select {
			case got := <-done:
				if got != "" {
					t.Fatalf("got %q, want empty result for a cancelled lookup", got)
				}
			case <-time.After(time.Second):
				t.Fatal("lookup ignored the caller's cancellation")
			}
			if tt.cached(p) {
				t.Fatal("cancelled lookup was cached")
			}
		})
	}
}
