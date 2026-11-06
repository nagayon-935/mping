package main

import (
	"bytes"
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
)

func TestRunPMTU_BackgroundDiscoveryIsJoinedBeforeTeardown(t *testing.T) {
	for _, uiError := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal exit", true: "UI failure"}[uiError], func(t *testing.T) {
			started, returned := make(chan struct{}), make(chan struct{})
			var measuring atomic.Bool
			stubRunSeams(t, func(ui.RunOptions) error {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("IPv6 discovery did not start")
				}
				if uiError {
					return io.ErrUnexpectedEOF
				}
				return nil
			})
			newPinger = func([]*stats.TargetStats, pinger.Options) pingerController {
				return &fakePinger{
					onStart: func() { measuring.Store(true) },
					onStop: func() {
						if measuring.Load() && !closed(returned) {
							t.Error("pinger teardown preceded background PMTU completion")
						}
					},
					discoverPathContext: func(ctx context.Context, _ string) (pinger.PathMTUResult, error) {
						if !measuring.Load() {
							t.Error("IPv6 discovery preceded pinger start")
						}
						close(started)
						<-ctx.Done()
						close(returned)
						return pinger.PathMTUResult{}, ctx.Err()
					},
				}
			}
			var out, errOut bytes.Buffer

			code := run([]string{"-m", "::1"}, &out, &errOut)

			if !closed(returned) || (code != 0) != uiError {
				t.Fatalf("background completed=%v code=%d stderr=%s", closed(returned), code, errOut.String())
			}
		})
	}
}

func TestRunPMTU_StartFailureDoesNotLaunchIPv6Discovery(t *testing.T) {
	var calls atomic.Int32
	stubRunSeams(t, func(ui.RunOptions) error {
		t.Error("UI started despite pinger failure")
		return nil
	})
	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController {
		return &fakePinger{startErr: io.ErrUnexpectedEOF, discoverPathContext: func(context.Context, string) (pinger.PathMTUResult, error) {
			calls.Add(1)
			return pinger.PathMTUResult{}, nil
		}}
	}
	var out, errOut bytes.Buffer

	code := run([]string{"-m", "::1"}, &out, &errOut)

	if code == 0 || calls.Load() != 0 {
		t.Fatalf("code=%d IPv6 discoveries=%d", code, calls.Load())
	}
}
