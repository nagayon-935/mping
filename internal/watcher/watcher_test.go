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

	// Trigger a change.
	if err := os.WriteFile(path, []byte("hosts:\n  - b.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + margin.
	time.Sleep(debounceDelay + 150*time.Millisecond)

	if n := called.Load(); n < 1 {
		t.Errorf("onChange not called after write; called %d times", n)
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

func TestWatch_NonExistentFile(t *testing.T) {
	err := Watch(t.Context(), "/tmp/does-not-exist-mping-test.yaml", func() {})
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}
