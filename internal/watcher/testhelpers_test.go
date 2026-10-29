package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Watch is WatchPaths with a single fixed path.
func Watch(ctx context.Context, path string, onChange func()) error {
	return WatchFiles(ctx, []string{path}, onChange)
}

// WatchFiles is WatchPaths with a fixed path set.
func WatchFiles(ctx context.Context, paths []string, onChange func()) error {
	return WatchPaths(ctx, func() []string { return paths }, onChange)
}

// startWatch runs fn in a goroutine and returns once the watcher has
// registered its initial directories (watchingHook), so any change the test
// makes from then on is seen. The returned stop func cancels the watcher,
// waits for it to return, and fails the test if it returned an error or did
// not exit. stop is safe to call more than once.
func startWatch(t *testing.T, fn func(ctx context.Context) error) (stop func()) {
	t.Helper()
	watching := make(chan struct{}, 1)
	watchingHook = func() { watching <- struct{}{} }
	t.Cleanup(func() { watchingHook = nil })
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- fn(ctx) }()
	select {
	case <-watching:
	case err := <-errCh:
		cancel()
		t.Fatalf("watch returned before watching: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("watcher did not start watching")
	}
	var once sync.Once
	return func() {
		t.Helper()
		cancel()
		once.Do(func() {
			select {
			case err := <-errCh:
				if err != nil {
					t.Errorf("watch returned error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("watch did not exit after ctx cancellation")
			}
		})
	}
}

// waitFor polls cond every 20ms until it holds or timeout elapses, and
// reports whether it held.
func waitFor(timeout time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(timeout); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			return false
		}
	}
	return true
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

// waitForMore waits until called exceeds before.
func waitForMore(t *testing.T, called *atomic.Int32, before int32, what string) {
	t.Helper()
	if !waitFor(5*time.Second, func() bool { return called.Load() > before }) {
		t.Fatalf("%s did not trigger onChange", what)
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

// waitRefresh waits for the next post-change path refresh to finish.
func waitRefresh(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("path refresh did not run")
	}
}

// pathSets returns a paths func that serves sets[i] on its i-th call and
// keeps serving the last set afterwards. WatchPaths calls it once at startup
// and once after every onChange, so sets[0] is the initial watch set and
// sets[1] the one in force after the first change.
func pathSets(sets ...[]string) func() []string {
	var calls atomic.Int32
	return func() []string {
		return sets[min(int(calls.Add(1))-1, len(sets)-1)]
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// touch rewrites path with fresh content, so repeated calls are real changes.
func touch(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, time.Now().String())
}

// breakCwd makes the working directory unusable so that filepath.Abs fails
// for relative paths. Removing the directory does that on Linux; macOS keeps
// reporting the old path in that case but fails once the directory is
// unreadable. The test is skipped where neither works.
func breakCwd(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // let the TempDir cleanup run
	t.Chdir(dir)
	broken := func() bool { _, err := filepath.Abs("x"); return err != nil }
	if err := os.Chmod(dir, 0); err == nil && broken() {
		return
	}
	if err := os.Remove(dir); err == nil && broken() {
		return
	}
	t.Skip("this platform can still resolve relative paths from an unusable working directory")
}
