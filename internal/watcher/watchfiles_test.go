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
		if err := os.WriteFile(p, []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
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
	if err := os.WriteFile(sibling, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n != before {
		t.Fatalf("unlisted sibling triggered onChange (%d → %d calls)", before, n)
	}
}

// TestWatchFiles_CreatingMissingFileTriggers covers an include file that
// does not exist yet: creating it should trigger a reload.
func TestWatchFiles_CreatingMissingFileTriggers(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "later.csv")
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchFiles(ctx, []string{missing}, func() { called.Add(1) })
	})
	defer stop()

	writeUntilCalled(t, missing, &called)
}

// TestWatchPaths_RefreshesAfterChange covers a hosts file edited to name a
// new include file in a not-yet-watched directory: after the hosts-file
// change fires, the path set is re-read, so creating the include file
// fires again without touching the hosts file a second time.
func TestWatchPaths_RefreshesAfterChange(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "hosts.yaml")
	incPath := filepath.Join(t.TempDir(), "new.csv")
	if err := os.WriteFile(mainPath, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
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

	waitForMore := func(before int32, what string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for called.Load() == before {
			if time.Now().After(deadline) {
				t.Fatalf("%s did not trigger onChange", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
		// The path refresh runs right after onChange returns.
		time.Sleep(50 * time.Millisecond)
	}

	writeUntilCalled(t, mainPath, &called) // watcher is live, include not listed yet
	withInclude.Store(true)                // the hosts-file edit that adds the include
	before := called.Load()
	if err := os.WriteFile(mainPath, []byte("include: new.csv\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForMore(before, "editing the hosts file")
	before = called.Load()
	if err := os.WriteFile(incPath, []byte("10.0.0.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForMore(before, "creating the newly listed include file")
}
