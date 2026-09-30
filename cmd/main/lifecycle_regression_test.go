package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
	ui "github.com/nagayon-935/mping/internal/ui"
)

type regressionTracer func(context.Context) ([]string, error)

func (f regressionTracer) TraceRoute(ctx context.Context, _ string, _ int, _ time.Duration) ([]string, error) {
	return f(ctx)
}

func TestTraceResetJoinsPreviousRun(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	sup := newSupervisor(supervisorConfig{targets: []*stats.TargetStats{target}})
	entered, release := make(chan struct{}), make(chan struct{})
	sup.startTraceroutes(regressionTracer(func(ctx context.Context) ([]string, error) {
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
		sup.startTraceroutes(regressionTracer(func(context.Context) ([]string, error) { close(newEntered); return []string{"new route"}, nil }))
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
	deadline := time.Now().Add(time.Second)
	for !reflect.DeepEqual(target.GetView().TraceHops, []string{"new route"}) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	sup.traceCancel()
	<-sup.traceDone
	if got := target.GetView().TraceHops; !reflect.DeepEqual(got, []string{"new route"}) {
		t.Fatalf("stale trace result: %v", got)
	}
}

func TestCancelledTraceKeepsPreviousRoute(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	target.SetTraceHops([]string{"previous route"})
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runTraceroutes(ctx, regressionTracer(func(ctx context.Context) ([]string, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() }), []*stats.TargetStats{target})
		close(done)
	}()
	<-entered
	cancel()
	<-done
	if got := target.GetView().TraceHops; !reflect.DeepEqual(got, []string{"previous route"}) {
		t.Fatalf("cancellation changed route: %v", got)
	}
}

type joiningResultPinger struct {
	lifecycleFakePinger
	target *stats.TargetStats
	once   sync.Once
}

func (p *joiningResultPinger) Wait() { p.once.Do(func() { p.target.OnSuccess(time.Millisecond, 64) }) }

func TestFinalJSONMatchesJoinedStats(t *testing.T) {
	target := stats.NewTargetStats("127.0.0.1")
	target.IncSent()
	sup := newSupervisor(supervisorConfig{makePinger: func(int) pingerController { return &joiningResultPinger{target: target} }, targets: []*stats.TargetStats{target}})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	var errs bytes.Buffer
	finishIteration(config{jsonOutputFile: path}, []*stats.TargetStats{target}, sup, &errs, func() {}, done, func() {}, done)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap stats.ExportSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Targets[0].Recv != target.GetView().Recv {
		t.Fatalf("JSON recv=%d, joined recv=%d", snap.Targets[0].Recv, target.GetView().Recv)
	}
}

func TestResolveAllPreservesZoneAndGroups(t *testing.T) {
	specs := []targetSpec{{Host: "fe80::1%en0", DSCP: "EF"}, {Host: "::1"}}
	groups := []ui.TargetGroup{{Name: "local", Indices: []int{0, 1}}}
	got, gotGroups, err := expandTargets(specs, groups, config{resolveAll: true, ipv6Only: true, sourceAddr: "::1"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specs) || !reflect.DeepEqual(gotGroups, groups) {
		t.Fatalf("zone/group/DSCP changed: %+v %+v", got, gotGroups)
	}
}

func TestSupervisorStopResetRestartPreservesStateAndOwnership(t *testing.T) {
	sup, created := newStateTestSupervisor(t)
	sup.Start()
	defer sup.Shutdown()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	target := sup.cfg.targets[0]
	old := target.NewProbe()
	old.IncSent()
	old.OnSuccess(time.Millisecond, 64, 0)
	sup.stopAll()
	if target.GetView().Sent != 1 || target.GetView().Recv != 1 {
		t.Fatal("stop cleared statistics")
	}
	sup.resetStats()
	if sup.stateSnapshot() != stateStopped || len(*created) != 1 || !(*created)[0].isReleased() {
		t.Fatal("reset resurrected a stopped measurement")
	}
	if old.OnFailure("late timeout") {
		t.Fatal("stopped reset accepted old probe")
	}
	if err := sup.do(cmdRestart); err != nil {
		t.Fatal(err)
	}
	fresh := target.NewProbe()
	fresh.IncSent()
	fresh.OnSuccess(time.Millisecond, 64, 0)
	if err := sup.do(cmdRestart); err != nil {
		t.Fatal(err)
	}
	if target.GetView().Recv != 1 || len(*created) != 3 {
		t.Fatal("restart did not preserve stats/create new instance")
	}
	before := *sup.pingerSnap.Load()
	sup.resetStats()
	if *sup.pingerSnap.Load() != before || target.GetView().Sent != 0 || target.GetView().Recv != 0 {
		t.Fatal("running reset replaced ping instance or kept old statistics")
	}
	sup.Shutdown() // also stops all owned producers without a separate terminate
	if sup.stateSnapshot() != stateTerminated {
		t.Fatal("Shutdown did not terminate")
	}
	for _, p := range *created {
		if !p.isReleased() {
			t.Fatal("Shutdown leaked a pinger")
		}
	}
}

func TestSupervisorShutdownJoinsCompletionObservers(t *testing.T) {
	p := &completingPinger{workersDone: make(chan struct{})}
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	sup.Shutdown()
	if !p.isReleased() || sup.stateSnapshot() != stateTerminated {
		t.Fatal("measurement still live after Shutdown")
	}
	select {
	case <-sup.loopDone:
	default:
		t.Fatal("command loop still live")
	}
	// Shutdown has already waited for observers; repeated joins must return.
	done := make(chan struct{})
	go func() { sup.observers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion observer leaked")
	}
}
