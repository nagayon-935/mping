package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/hostsfile"
	"github.com/nagayon-935/mping/internal/mtr"
	"github.com/nagayon-935/mping/internal/pinger"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/nagayon-935/mping/internal/ui"
	"github.com/nagayon-935/mping/internal/web"
	"github.com/rivo/tview"
)

// hostEntries builds a []hostsfile.Entry from bare hostnames.
func hostEntries(hosts ...string) []hostsfile.Entry {
	entries := make([]hostsfile.Entry, len(hosts))
	for i, h := range hosts {
		entries[i] = hostsfile.Entry{Host: h}
	}
	return entries
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// writeHostsYAML writes content to hosts.yaml in a fresh temp dir and returns
// its path.
func writeHostsYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write hosts file: %v", err)
	}
	return path
}

// pollUntil polls cond every few milliseconds until it holds and reports
// whether it did within timeout.
func pollUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// closed reports, without blocking, whether ch has been closed.
func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// stubRunSeams swaps newPinger/uiRun/webStart/openBrowser for the duration of a
// test: every pinger is a fresh fakePinger and uiRun is uiFn (tests that need
// their own pinger or to set uiRun later assign newPinger / uiRun after the
// call). webStart is wrapped so the real server binds a kernel-chosen port
// instead of the configured one, keeping tests independent of what is free
// locally. The returned func yields the web server once run() has started it.
func stubRunSeams(t *testing.T, uiFn func(ui.RunOptions) error) (started func() *web.Server) {
	t.Helper()
	origPinger, origUI, origWeb, origBrowser := newPinger, uiRun, webStart, openBrowser
	t.Cleanup(func() { newPinger, uiRun, webStart, openBrowser = origPinger, origUI, origWeb, origBrowser })

	newPinger = func([]*stats.TargetStats, pinger.Options) pingerController { return &fakePinger{} }
	uiRun = uiFn
	openBrowser = func(string) {}
	var srv *web.Server
	webStart = func(opts web.Options) (*web.Server, error) {
		opts.Port = 0
		s, err := web.Start(opts)
		srv = s
		return s, err
	}
	return func() *web.Server { return srv }
}

// fakePinger is the pingerController double shared by the whole package. The
// zero value is a working, well-behaved pinger: Start succeeds, Wait returns
// immediately and its route prober declines to open a hop socket. Tests opt
// into other behaviour
// through the exported-by-convention knobs below, set before the pinger is
// handed to the code under test.
//
// Lifecycle state is mutex-protected because the supervisor drives the pinger
// from its command-loop goroutine while tests inspect it from their own.
type fakePinger struct {
	// Failure / result injection.
	startErr             error
	discoverMTU          int
	discoverBottleneckIP string
	discoverErr          error
	discoverLogs         []string

	// routeStarted and routeReturned, when set, make the route prober's
	// OpenHopSocket block on ctx.Done() (a slow, in-flight lookup) instead
	// of failing at once; it closes routeStarted as it begins to block and
	// routeReturned right before it returns, so a test can observe when the
	// route engine actually exited.
	routeStarted  chan struct{}
	routeReturned chan struct{}

	// workersDone, when non-nil, makes WaitWorkers block until finish() is
	// called, modelling a count-limited run whose probes complete on demand.
	workersDone chan struct{}
	finishOnce  sync.Once

	// onStop / onClose / onWait run at the start of the matching call, before
	// the call is recorded. Tests use them to check ordering against other
	// events without a wrapper type per method.
	onStop, onClose, onWait func()

	// waited is written by Wait(), which — like the real *pinger.Pinger.Wait()
	// it stands in for (a thin sync.WaitGroup.Wait() wrapper, safe for
	// concurrent callers by design) — can legitimately be called from two
	// goroutines at once: run()'s doneCh watcher (waitPinger(), count-limited
	// mode) and the supervisor's tearDownAll(), racing during shutdown.
	waited atomic.Bool

	mu                       sync.Mutex
	started, stopped, closed bool
	// interfaceSet records the last value passed to SetInterface, so tests
	// can verify makePingerFactory wires cfg.ifaceName through without
	// needing a real *pinger.Pinger.
	interfaceSet string
}

func (f *fakePinger) Start(interval, timeout time.Duration) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return nil
}

func (f *fakePinger) Stop() {
	if f.onStop != nil {
		f.onStop()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}

func (f *fakePinger) Wait() {
	if f.onWait != nil {
		f.onWait()
	}
	f.waited.Store(true)
}

func (f *fakePinger) WaitWorkers() {
	if f.workersDone != nil {
		<-f.workersDone
		return
	}
	f.Wait()
}

// finish lets a pinger created with workersDone report its probes complete.
func (f *fakePinger) finish() { f.finishOnce.Do(func() { close(f.workersDone) }) }

func (f *fakePinger) Close() {
	if f.onClose != nil {
		f.onClose()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

// Waited reports whether Wait() has been called, safe to read concurrently
// with a still-in-flight Wait() call (see the waited field's doc comment).
func (f *fakePinger) Waited() bool { return f.waited.Load() }

func (f *fakePinger) isStarted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// isReleased reports whether both Stop() and Close() ran, i.e. the pinger's
// resources were handed back.
func (f *fakePinger) isReleased() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped && f.closed
}

func (f *fakePinger) DiscoverMaxPayload(ctx context.Context, dest string, start int, min int, logf func(string)) (int, string, error) {
	if f.discoverErr != nil {
		return 0, "", f.discoverErr
	}
	for _, line := range f.discoverLogs {
		logf(line)
	}
	if f.discoverMTU == 0 {
		return start, "", nil
	}
	return f.discoverMTU, f.discoverBottleneckIP, nil
}

func (f *fakePinger) SetSource(ip string)                       {}
func (f *fakePinger) SetSize(size int)                          {}
func (f *fakePinger) SetCount(count int)                        {}
func (f *fakePinger) SetResolveInterval(interval time.Duration) {}
func (f *fakePinger) SetLogWriter(w io.Writer)                  {}
func (f *fakePinger) SetInterface(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interfaceSet = name
}

// MTRProber returns a stub prober rather than nil: cmdResetRoute starts an
// mtr.Engine unconditionally whenever it is invoked, without checking
// cfg.routeEnabled itself (that gating lives at the call site), so the
// engine's per-target goroutine really does call prober.OpenHopSocket. A nil
// mtr.HopProber is a nil interface value and calling a method on it panics;
// noopHopProber gives it a safe implementation that declines to open a socket.
func (f *fakePinger) MTRProber() mtr.HopProber {
	if f.routeStarted != nil {
		return blockingHopProber{started: f.routeStarted, returned: f.routeReturned}
	}
	return noopHopProber{}
}

// blockingHopProber's OpenHopSocket blocks until ctx is cancelled; see
// fakePinger.routeStarted. Use it with a single target.
type blockingHopProber struct {
	noopHopProber
	started, returned chan struct{}
}

func (p blockingHopProber) OpenHopSocket(ctx context.Context, dest string) (mtr.HopSocket, error) {
	close(p.started)
	<-ctx.Done()
	close(p.returned)
	return nil, ctx.Err()
}

// noopHopProber is a minimal mtr.HopProber that always fails to open a hop
// socket, so an mtr.Engine started against it exits its per-target goroutine
// immediately instead of doing any real probing.
type noopHopProber struct{}

func (noopHopProber) OpenHopSocket(ctx context.Context, dest string) (mtr.HopSocket, error) {
	return nil, errors.New("noopHopProber: no socket available")
}

func (noopHopProber) ProbeHop(ctx context.Context, sock mtr.HopSocket, dest string, ttl, traceID int, timeout time.Duration) (pinger.HopReply, error) {
	return pinger.HopReply{}, errors.New("noopHopProber: no probe available")
}

func (noopHopProber) NextTraceID() int { return 0 }

func (noopHopProber) ASNInfoFor(ip string) pinger.ASNInfo { return pinger.ASNInfo{} }
func (noopHopProber) PTRFor(string) string                { return "" }

// liveFakePinger is a fakePinger that also implements dynamicPinger, so the
// supervisor takes its live-edit and event-driven completion paths.
type liveFakePinger struct {
	fakePinger
	events  chan struct{}
	done    chan struct{}
	failAdd bool
}

var _ dynamicPinger = (*liveFakePinger)(nil)

func newLiveFakePinger() *liveFakePinger {
	return &liveFakePinger{events: make(chan struct{}, 1), done: make(chan struct{})}
}
func (p *liveFakePinger) AddTarget(*stats.TargetStats, string) error {
	if p.failAdd {
		return errors.New("ID allocation failed")
	}
	return nil
}
func (p *liveFakePinger) RemoveTarget(*stats.TargetStats) {}
func (p *liveFakePinger) WorkerEvents() <-chan struct{}   { return p.events }
func (p *liveFakePinger) WorkersFinished() bool           { return false }
func (p *liveFakePinger) Done() <-chan struct{}           { return p.done }

// stateSnapshot returns the state as of the last processed command. Test-only:
// production code never reads state off the loop. Because do() waits for its
// reply, a stateSnapshot() call after a do() always observes that command.
func (s *supervisor) stateSnapshot() supervisorState {
	return supervisorState(s.stateSnap.Load())
}

// newStateTestSupervisor builds a supervisor whose pinger factory records
// every pinger it hands out, so a test can assert lifecycle effects without
// opening a real raw socket. The factory only runs on the supervisor's command
// goroutine, so created is safe to read once a do()/handle() call has returned.
func newStateTestSupervisor(t *testing.T) (*supervisor, *[]*fakePinger) {
	t.Helper()
	var created []*fakePinger
	sup := newSupervisor(supervisorConfig{
		makePinger: func(size int) pingerController {
			fp := &fakePinger{}
			created = append(created, fp)
			return fp
		},
		targets:  []*stats.TargetStats{stats.NewTargetStats("example.com")},
		interval: time.Second,
		timeout:  time.Second,
		logCh:    make(chan string, 8),
	})
	return sup, &created
}

func doCmd(t *testing.T, s *supervisor, k cmdKind) error {
	t.Helper()
	return s.handle(command{kind: k})
}

// plainLogLine renders a Log-pane line without tview colour tags, using
// tview's own parser so escaped brackets (e.g. "[red[]") come out literally.
func plainLogLine(line string) string {
	text := tview.NewTextView().SetDynamicColors(true).SetText(line).GetText(true)
	return strings.TrimRight(text, "\n")
}
