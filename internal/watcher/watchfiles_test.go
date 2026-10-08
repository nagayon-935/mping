package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// startWatch runs fn in a goroutine and returns a stop func that cancels it,
// waits for it to return, and fails the test if it returned an error.
func startWatch(t *testing.T, fn func(ctx context.Context) error) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- fn(ctx) }()
	return func() {
		t.Helper()
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("watch returned error: %v", err)
		}
	}
}

// writeUntilCalled rewrites path until called becomes non-zero, so a write
// that races the watcher's startup is retried instead of failing the test.
// It then waits for the count to settle, so no event from these writes is
// still pending when the caller takes its next baseline.
func writeUntilCalled(t *testing.T, path string, called *atomic.Int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for called.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("onChange not called after writing %s", path)
		}
		if err := os.WriteFile(path, []byte(time.Now().String()), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(debounceDelay + 100*time.Millisecond)
	}
	settle(t, called)
}

// settle waits until called stays unchanged for longer than the debounce
// delay, i.e. no onChange is pending, and returns the settled count.
func settle(t *testing.T, called *atomic.Int32) int32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	last := called.Load()
	for stableSince := time.Now(); time.Since(stableSince) < debounceDelay+150*time.Millisecond; {
		if time.Now().After(deadline) {
			t.Fatal("onChange count never settled")
		}
		time.Sleep(20 * time.Millisecond)
		if n := called.Load(); n != last {
			last, stableSince = n, time.Now()
		}
	}
	return last
}

// waitForMore polls until called exceeds before.
func waitForMore(t *testing.T, called *atomic.Int32, before int32, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for called.Load() == before {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not trigger onChange", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hookRefreshes makes refreshedHook signal on the returned channel.
func hookRefreshes(t *testing.T) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{}, 16)
	refreshedHook = func() { ch <- struct{}{} }
	t.Cleanup(func() { refreshedHook = nil })
	return ch
}

func waitRefresh(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("path refresh did not run")
	}
}

// drain discards refresh signals already delivered.
func drain(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestWatchFiles_AnyListedFileTriggers verifies that a change to any of the
// watched files — here an include file in a different directory than the
// main file — fires onChange, while unlisted siblings stay ignored.
func TestWatchFiles_AnyListedFileTriggers(t *testing.T) {
	mainDir, incDir := t.TempDir(), t.TempDir()
	mainPath := filepath.Join(mainDir, "hosts.yaml")
	incPath := filepath.Join(incDir, "list.csv")
	sibling := filepath.Join(incDir, "other.csv")
	for _, p := range []string{mainPath, incPath, sibling} {
		writeFile(t, p, "x\n")
	}
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchFiles(ctx, []string{mainPath, incPath, incPath}, func() { called.Add(1) })
	})
	defer stop()

	// Prove the watcher is live via the include file first, so the sibling
	// check below can't pass merely because the watcher wasn't ready yet.
	writeUntilCalled(t, incPath, &called)
	before := called.Load()
	writeFile(t, sibling, "changed\n")

	if n := settle(t, &called); n != before {
		t.Fatalf("unlisted sibling triggered onChange (%d → %d calls)", before, n)
	}
}

// TestWatchFiles_CreatingMissingFileTriggers covers an include file that
// does not exist yet: creating it — a single write, not retried — must
// trigger a reload once the watcher is known to be live.
func TestWatchFiles_CreatingMissingFileTriggers(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.csv")
	missing := filepath.Join(dir, "later.csv")
	writeFile(t, probe, "x\n")
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchFiles(ctx, []string{probe, missing}, func() { called.Add(1) })
	})
	defer stop()
	writeUntilCalled(t, probe, &called) // watcher is live

	before := called.Load()
	writeFile(t, missing, "10.0.0.1\n")

	waitForMore(t, &called, before, "creating the missing file")
}

// TestWatchPaths_RefreshesAfterChange covers a hosts file edited to name a
// new include file in a not-yet-watched directory: after the hosts-file
// change fires, the path set is re-read, so creating the include file
// fires again without touching the hosts file a second time.
func TestWatchPaths_RefreshesAfterChange(t *testing.T) {
	refreshed := hookRefreshes(t)
	mainPath := filepath.Join(t.TempDir(), "hosts.yaml")
	incPath := filepath.Join(t.TempDir(), "new.csv")
	writeFile(t, mainPath, "x\n")
	var withInclude atomic.Bool
	paths := func() []string {
		if withInclude.Load() {
			return []string{mainPath, incPath}
		}
		return []string{mainPath}
	}
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchPaths(ctx, paths, func() { called.Add(1) })
	})
	defer stop()

	writeUntilCalled(t, mainPath, &called) // watcher is live, include not listed yet
	drain(refreshed)
	withInclude.Store(true) // the hosts-file edit that adds the include
	before := called.Load()
	writeFile(t, mainPath, "include: new.csv\n")
	waitForMore(t, &called, before, "editing the hosts file")
	waitRefresh(t, refreshed)
	before = settle(t, &called)

	writeFile(t, incPath, "10.0.0.1\n")

	waitForMore(t, &called, before, "creating the newly listed include file")
}

// TestWatchPaths_DroppedPathStopsTriggering covers the reverse: once a file
// leaves the path set (e.g. an include removed from the hosts file),
// changes to it no longer fire onChange.
func TestWatchPaths_DroppedPathStopsTriggering(t *testing.T) {
	refreshed := hookRefreshes(t)
	mainPath := filepath.Join(t.TempDir(), "hosts.yaml")
	incPath := filepath.Join(t.TempDir(), "old.csv")
	writeFile(t, mainPath, "x\n")
	writeFile(t, incPath, "x\n")
	var dropped atomic.Bool
	paths := func() []string {
		if dropped.Load() {
			return []string{mainPath}
		}
		return []string{mainPath, incPath}
	}
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchPaths(ctx, paths, func() { called.Add(1) })
	})
	defer stop()

	writeUntilCalled(t, mainPath, &called)
	drain(refreshed)
	dropped.Store(true) // the hosts-file edit that removes the include
	before := called.Load()
	writeFile(t, mainPath, "hosts: [a]\n")
	waitForMore(t, &called, before, "editing the hosts file")
	waitRefresh(t, refreshed)
	before = settle(t, &called)

	writeFile(t, incPath, "changed\n")

	if n := settle(t, &called); n != before {
		t.Fatalf("dropped include still triggered onChange (%d → %d calls)", before, n)
	}
}
