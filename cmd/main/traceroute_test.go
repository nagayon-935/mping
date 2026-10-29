package main

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

type destinationTracer chan string

func (d destinationTracer) TraceRoute(_ context.Context, dest string, _ int, _ time.Duration) ([]string, error) {
	d <- dest
	return []string{dest}, nil
}

// funcTracer adapts a function to the tracer interface.
type funcTracer func(context.Context) ([]string, error)

func (f funcTracer) TraceRoute(ctx context.Context, _ string, _ int, _ time.Duration) ([]string, error) {
	return f(ctx)
}

func TestRunTraceroutesUsesMonitoredAddress(t *testing.T) {
	for _, tc := range []struct{ name, ip, want string }{
		{"IPv4", "192.0.2.10", "192.0.2.10"},
		{"IPv6", "2001:db8::10", "2001:db8::10"},
		{"unresolved", "", "dns.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := stats.NewTargetStats("friendly label")
			target.SetResolveAddress("dns.example")
			target.SetIP(tc.ip)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := make(destinationTracer, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				runTraceroutes(ctx, called, target)
			}()
			select {
			case dest := <-called:
				if dest != tc.want {
					t.Errorf("traced %q; want monitored address %q", dest, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Error("traceroute did not start")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("traceroute did not stop")
			}
		})
	}
}

// firstRouteShown reports whether hops is a published route rather than empty
// or the "Tracing..." placeholder.
func firstRouteShown(hops []string) bool {
	return len(hops) > 0 && hops[0] != "Tracing..."
}

// traceUntil runs runTraceroutes until the displayed route satisfies shown,
// then cancels it, checks it stops, and returns the displayed route.
func traceUntil(t *testing.T, p tracer, target *stats.TargetStats, shown func([]string) bool) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runTraceroutes(ctx, p, target)
	}()

	waitFor(t, 2*time.Second, "the traceroute result", func() bool { return shown(target.GetView().TraceHops) })
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("runTraceroutes did not stop after context cancel")
	}
	return target.GetView().TraceHops
}

func TestRunTraceroutes_PublishesResult(t *testing.T) {
	tests := []struct {
		name     string
		p        tracer
		wantHops []string
		// wantPrefix, when set, is matched against the first hop instead.
		wantPrefix string
	}{
		{"route found", &fakePinger{}, []string{"hop1", "hop2"}, ""},
		{"trace error is shown as an error line", &fakePinger{traceErr: io.ErrUnexpectedEOF}, nil, "error:"},
		{"empty hop list reads as no route", funcTracer(func(context.Context) ([]string, error) { return nil, nil }), []string{"no route found"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")

			hops := traceUntil(t, tt.p, target, firstRouteShown)

			if tt.wantPrefix != "" {
				if len(hops) != 1 || !strings.HasPrefix(hops[0], tt.wantPrefix) {
					t.Errorf("hops = %v, want a single %q line", hops, tt.wantPrefix)
				}
				return
			}
			if !reflect.DeepEqual(hops, tt.wantHops) {
				t.Errorf("hops = %v, want %v", hops, tt.wantHops)
			}
		})
	}
}

// TestRunTraceroutes_Placeholder checks the "Tracing..." placeholder: it is
// shown while the first trace is in flight, but never replaces a route that is
// already on display.
func TestRunTraceroutes_Placeholder(t *testing.T) {
	tests := []struct {
		name     string
		previous []string
		want     []string
	}{
		{"first run shows the placeholder", nil, []string{"Tracing..."}},
		{"a re-run keeps the previous route visible", []string{"previous route"}, []string{"previous route"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := stats.NewTargetStats("example.com")
			if tt.previous != nil {
				target.SetTraceHops(tt.previous)
			}
			var during []string
			tr := funcTracer(func(context.Context) ([]string, error) {
				during = target.GetView().TraceHops
				return []string{"new route"}, nil
			})

			traceUntil(t, tr, target, func(hops []string) bool { return reflect.DeepEqual(hops, []string{"new route"}) })

			if !reflect.DeepEqual(during, tt.want) {
				t.Errorf("route displayed while tracing = %v, want %v", during, tt.want)
			}
		})
	}
}

func TestCancelledTraceKeepsPreviousRoute(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	target.SetTraceHops([]string{"previous route"})
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runTraceroutes(ctx, funcTracer(func(ctx context.Context) ([]string, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() }), target)
		close(done)
	}()
	<-entered
	cancel()
	<-done
	if got := target.GetView().TraceHops; !reflect.DeepEqual(got, []string{"previous route"}) {
		t.Fatalf("cancellation changed route: %v", got)
	}
}

func TestTraceResetJoinsPreviousRun(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	sup := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{target}})
	entered, release := make(chan struct{}), make(chan struct{})
	sup.startTraceroutes(funcTracer(func(ctx context.Context) ([]string, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}))
	oldDone := sup.traceDone
	<-entered
	resetDone := make(chan struct{})
	newEntered := make(chan struct{})
	go func() {
		sup.startTraceroutes(funcTracer(func(context.Context) ([]string, error) { close(newEntered); return []string{"new route"}, nil }))
		close(resetDone)
	}()
	select {
	case <-newEntered:
		t.Error("replacement started before previous traceroute exited")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-oldDone
	<-resetDone
	<-newEntered
	waitFor(t, time.Second, "the replacement route to be published", func() bool {
		return reflect.DeepEqual(target.GetView().TraceHops, []string{"new route"})
	})
	sup.traceCancel()
	<-sup.traceDone
	if got := target.GetView().TraceHops; !reflect.DeepEqual(got, []string{"new route"}) {
		t.Fatalf("stale trace result: %v", got)
	}
}
