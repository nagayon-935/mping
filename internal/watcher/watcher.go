// Package watcher provides a file-change watcher with debouncing.
package watcher

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceDelay = 200 * time.Millisecond

// Watch monitors the file at path for content changes (Write or Create events).
//
// The parent directory is watched rather than the file itself so that
// editor save patterns that atomically replace the file via rename are
// correctly detected (e.g. vim :w, nano, many CLI tools).
//
// A 200 ms debounce timer coalesces rapid successive events into a single
// onChange call.
//
// Watch blocks until ctx is cancelled, then returns nil.
// A non-nil error is returned only for setup failures (e.g. fsnotify init,
// unreadable directory).
func Watch(ctx context.Context, path string, onChange func()) error {
	// Resolve to an absolute path so we can compare event paths correctly
	// regardless of how the caller expressed path (relative vs absolute).
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path %q: %w", path, err)
	}
	dir := filepath.Dir(absPath)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	// Watch the parent directory.  This catches:
	//   • direct writes      → Write event on the file
	//   • atomic rename-over → Create event on the file path
	//   • delete + recreate  → Create event on the file path
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch directory %q: %w", dir, err)
	}

	// Start timer in stopped state; it is armed only after the first event.
	timer := time.NewTimer(debounceDelay)
	timer.Stop()
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			// Ignore events for other files in the same directory.
			if filepath.Clean(event.Name) != absPath {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				// Drain a pending timer tick before resetting to avoid double-fire.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounceDelay)
			}

		case _, ok := <-w.Errors:
			if !ok {
				return nil
			}
			// Non-fatal watcher errors (e.g. EINTR): ignore and continue.

		case <-timer.C:
			onChange()
		}
	}
}
