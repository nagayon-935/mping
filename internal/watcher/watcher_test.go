package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatch_TriggersOnChange verifies that Watch notices each way a file
// gets saved: a direct write, an atomic rename-over (what vim, nano, and
// many other editors do), and a write to a path given relative to the
// working directory.
func TestWatch_TriggersOnChange(t *testing.T) {
	tests := []struct {
		name     string
		relative bool // hand Watch the path relative to the working directory
		// change is made once, after the watcher is known to be live; nil
		// means the direct write that proves liveness is the change under test.
		change func(t *testing.T, path string)
	}{
		{
			name: "direct write",
		},
		{
			name: "atomic rename over the file",
			change: func(t *testing.T, path string) {
				// vim-style save: write to a temp file, then rename it over the original.
				tmp := path + ".tmp"
				writeFile(t, tmp, time.Now().String())
				if err := os.Rename(tmp, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:     "write via relative path",
			relative: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "hosts.yaml")
			writeFile(t, path, "hosts:\n  - a.com\n")
			watchArg := path
			if tt.relative {
				t.Chdir(dir)
				watchArg = "hosts.yaml"
			}
			var called atomic.Int32
			stop := startWatch(t, func(ctx context.Context) error {
				return Watch(ctx, watchArg, func() { called.Add(1) })
			})
			defer stop()

			triggerUntilCalled(t, &called, func() { touch(t, path) })
			if tt.change == nil {
				return
			}
			before := called.Load()
			tt.change(t, path)
			waitForMore(t, &called, before, tt.name)
		})
	}
}

// TestWatch_Debounce verifies that a rapid burst of writes coalesces into a
// single onChange call, and that a write made after that debounce fired
// starts a new one.
func TestWatch_Debounce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	writeFile(t, path, "initial\n")
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return Watch(ctx, path, func() { called.Add(1) })
	})
	defer stop()
	triggerUntilCalled(t, &called, func() { touch(t, path) }) // watcher is live
	before := called.Load()

	for range 5 {
		touch(t, path)
		time.Sleep(20 * time.Millisecond)
	}
	if n := settle(t, &called); n != before+1 {
		t.Fatalf("burst of writes gave %d onChange calls, want 1", n-before)
	}

	touch(t, path)
	waitForMore(t, &called, before+1, "a write after the debounce fired")
}

// TestWatchPaths_CancelExits verifies that a running watcher returns nil
// once its context is cancelled.
func TestWatchPaths_CancelExits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	writeFile(t, path, "initial\n")
	started := make(chan struct{})
	var once sync.Once
	paths := func() []string {
		once.Do(func() { close(started) })
		return []string{path}
	}
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchPaths(ctx, paths, func() {})
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchPaths never read its path set")
	}

	stop() // fails the test if WatchPaths errors or does not return
}

// TestWatchPaths_CancelDuringDebounceDropsPendingChange verifies that
// cancelling while a debounce is pending returns promptly without calling
// onChange for the change that was still being debounced.
func TestWatchPaths_CancelDuringDebounceDropsPendingChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	writeFile(t, path, "initial\n")
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return Watch(ctx, path, func() { called.Add(1) })
	})
	defer stop()
	triggerUntilCalled(t, &called, func() { touch(t, path) }) // watcher is live
	before := called.Load()

	writtenAt := time.Now()
	touch(t, path)
	// Half the debounce: long enough for the event to arrive, short enough
	// that the 200 ms timer it starts is still pending at cancellation.
	time.Sleep(debounceDelay / 2)
	stop()

	if time.Since(writtenAt) >= debounceDelay {
		t.Skip("scheduler stall let the debounce expire before cancellation")
	}
	if n := called.Load(); n != before {
		t.Fatalf("onChange called %d time(s) for a change still being debounced at cancellation", n-before)
	}
}

// TestWatch_SetupFailures verifies that problems that make watching
// impossible at startup are returned instead of leaving a silent watcher.
func TestWatch_SetupFailures(t *testing.T) {
	tests := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr string
	}{
		{
			name:    "parent directory does not exist",
			path:    func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing", "hosts.yaml") },
			wantErr: "watch directory",
		},
		{
			name:    "relative path with an unusable working directory",
			path:    func(t *testing.T) string { breakCwd(t); return "hosts.yaml" },
			wantErr: "resolve path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			err := Watch(ctx, path, func() {})

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Watch error = %v, want one mentioning %q", err, tt.wantErr)
			}
		})
	}
}
