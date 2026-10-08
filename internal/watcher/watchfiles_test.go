package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := WatchFiles(ctx, []string{mainPath, incPath, incPath}, func() { called.Add(1) }); err != nil {
			t.Errorf("WatchFiles: %v", err)
		}
	}()
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(sibling, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(debounceDelay + 150*time.Millisecond)
	if n := called.Load(); n != 0 {
		t.Fatalf("unlisted sibling triggered onChange %d times", n)
	}

	if err := os.WriteFile(incPath, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(debounceDelay + 150*time.Millisecond)
	if n := called.Load(); n != 1 {
		t.Fatalf("include change: onChange called %d times, want 1", n)
	}

	cancel()
	<-done
}

// TestWatchFiles_CreatingMissingFileTriggers covers an include file that
// does not exist yet: creating it should trigger a reload.
func TestWatchFiles_CreatingMissingFileTriggers(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "later.csv")

	var called atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = WatchFiles(ctx, []string{missing}, func() { called.Add(1) })
	}()
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(missing, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(debounceDelay + 150*time.Millisecond)
	if n := called.Load(); n < 1 {
		t.Fatalf("creating a watched file did not trigger onChange")
	}

	cancel()
	<-done
}
