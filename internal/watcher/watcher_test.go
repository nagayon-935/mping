package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatch_CallsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - a.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := Watch(ctx, path, func() { called.Add(1) }); err != nil {
			t.Errorf("Watch: %v", err)
		}
	}()

	// Give watcher time to start.
	time.Sleep(50 * time.Millisecond)

	// Trigger a direct write (no rename).
	if err := os.WriteFile(path, []byte("hosts:\n  - b.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + margin.
	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n < 1 {
		t.Errorf("onChange not called after direct write; called %d times", n)
	}

	cancel()
	<-done
}

// TestWatch_AtomicRename verifies that the watcher detects saves done via an
// atomic rename (write-to-tmp then rename-over), which is what vim, nano, and
// many other editors do.
func TestWatch_AtomicRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("hosts:\n  - a.com\n"), 0644); err != nil {
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

	// Simulate vim-style atomic save: write to tmp, then rename over original.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("hosts:\n  - b.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n < 1 {
		t.Errorf("onChange not called after atomic rename; called %d times", n)
	}

	cancel()
	<-done
}

func TestWatch_Debounce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("initial\n"), 0644); err != nil {
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

	// Write multiple times rapidly — should coalesce into a single onChange call.
	for range 5 {
		if err := os.WriteFile(path, []byte("update\n"), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for debounce to settle.
	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n != 1 {
		t.Errorf("expected 1 debounced call, got %d", n)
	}

	cancel()
	<-done
}

func TestWatch_CancelExits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(path, []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Watch(ctx, path, func() {})
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Watch did not exit after ctx cancellation")
	}
}

// TestWatch_OtherFilesIgnored verifies that changes to sibling files in the
// same directory do not trigger onChange.
func TestWatch_OtherFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hosts.yaml")
	other := filepath.Join(dir, "other.yaml")

	if err := os.WriteFile(target, []byte("target\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var called atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Watch(ctx, target, func() { called.Add(1) })
	}()

	time.Sleep(50 * time.Millisecond)

	// Modify the sibling file — should NOT trigger onChange.
	if err := os.WriteFile(other, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}

	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n != 0 {
		t.Errorf("onChange triggered by sibling file change; called %d times", n)
	}

	cancel()
	<-done
}

// TestWatch_NonExistentDirectory verifies that Watch returns an error when the
// parent directory of path does not exist.
func TestWatch_NonExistentDirectory(t *testing.T) {
	err := Watch(t.Context(), "/nonexistent-dir-mping-test/hosts.yaml", func() {})
	if err == nil {
		t.Error("expected error for non-existent parent directory, got nil")
	}
}
