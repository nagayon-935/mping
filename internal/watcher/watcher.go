// Package watcher provides a file-change watcher with debouncing.
package watcher

import (
	"context"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceDelay = 200 * time.Millisecond

// Watch monitors the file at path for Write and Create events.
// When such an event is detected, a 200 ms debounce timer starts (resetting on
// each additional event within the window). Once the timer fires, onChange is
// called in the Watch goroutine.
//
// Watch blocks until ctx is cancelled, then returns nil.
// A non-nil error is returned only for setup failures (e.g. fsnotify init).
func Watch(ctx context.Context, path string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	if err := w.Add(path); err != nil {
		return fmt.Errorf("watch %q: %w", path, err)
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
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if event.Has(fsnotify.Create) {
					// Vim/nano/emacs save by creating a new file and renaming it
					// over the original. Re-add the path to keep watching the
					// new inode.
					_ = w.Add(path)
				}
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
			// Non-fatal watcher errors (e.g. EINTR): log and continue.

		case <-timer.C:
			onChange()
		}
	}
}
