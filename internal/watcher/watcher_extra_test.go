package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatch_WriteAfterDebounce verifies that a second write made after the
// debounce timer has fired also triggers onChange. This exercises the timer
// drain path (timer.Stop returns false on an already-fired timer) that occurs
// when a write event arrives immediately after the debounce fires.
func TestWatch_WriteAfterDebounce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Watch(ctx, path, func() { called.Add(1) })
	}()

	time.Sleep(50 * time.Millisecond)

	// First write starts the debounce timer.
	if err := os.WriteFile(path, []byte("v2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for the debounce to fire and onChange to be called once.
	time.Sleep(debounceDelay + 100*time.Millisecond)

	// Second write arrives after the timer has already fired. This hits the
	// !timer.Stop() == true branch and exercises the drain select.
	if err := os.WriteFile(path, []byte("v3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n < 2 {
		t.Errorf("expected at least 2 onChange calls, got %d", n)
	}

	cancel()
	<-done
}

// TestWatch_RelativePath verifies that Watch resolves a relative path correctly
// and still fires onChange on file modification.
func TestWatch_RelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change working directory to dir so we can pass a relative path.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	var called atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Watch(ctx, "hosts.yaml", func() { called.Add(1) })
	}()

	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(path, []byte("updated\n"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n < 1 {
		t.Errorf("onChange not called after write via relative path; called %d times", n)
	}

	cancel()
	<-done
}
