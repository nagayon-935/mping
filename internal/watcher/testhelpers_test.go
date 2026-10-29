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

// startWatch runs fn in a goroutine and returns a stop func that cancels it,
// waits for it to return, and fails the test if it returned an error or did
// not exit. stop is safe to call more than once.
//
// It also gives the watcher a moment to register its directories. That is
// only an optimization: a change made before the watcher is live is lost,
// which is why tests prove liveness with triggerUntilCalled before relying
// on a single change.
func startWatch(t *testing.T, fn func(ctx context.Context) error) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- fn(ctx) }()
	time.Sleep(50 * time.Millisecond)
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

// triggerUntilCalled runs trigger until onChange has been called, so a
// change that races the watcher's startup is retried instead of failing the
// test. A successful return proves the watcher is live. If a retry was
// needed it then waits for the count to settle, so no event from the
// earlier attempts is still pending when the caller takes its baseline.
func triggerUntilCalled(t *testing.T, called *atomic.Int32, trigger func()) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for attempts := 1; ; attempts++ {
		if time.Now().After(deadline) {
			t.Fatal("onChange not called after the change")
		}
		trigger()
		if waitFor(debounceDelay+100*time.Millisecond, func() bool { return called.Load() > 0 }) {
			if attempts > 1 {
				settle(t, called)
			}
			return
		}
	}
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
