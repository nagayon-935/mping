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
// It is WatchFiles with a single path.
func Watch(ctx context.Context, path string, onChange func()) error {
	return WatchFiles(ctx, []string{path}, onChange)
}

// WatchFiles monitors every file in paths for content changes (Write or
// Create events) and calls onChange once per debounced burst of changes to
// any of them. A listed file need not exist yet: creating it counts as a
// change. Duplicate paths are watched once.
//
// The parent directories are watched rather than the files themselves so
// that editor save patterns that atomically replace a file via rename are
// correctly detected (e.g. vim :w, nano, many CLI tools).
//
// A 200 ms debounce timer coalesces rapid successive events into a single
// onChange call.
//
// WatchFiles blocks until ctx is cancelled, then returns nil.
// A non-nil error is returned for setup failures (e.g. fsnotify init,
// unreadable directory) or for runtime fsnotify errors (e.g. ENOSPC, EBADF)
// that would leave auto-reload silently broken if ignored.
func WatchFiles(ctx context.Context, paths []string, onChange func()) error {
	// Resolve to absolute paths so we can compare event paths correctly
	// regardless of how the caller expressed them (relative vs absolute).
	watched := make(map[string]bool, len(paths))
	var dirs []string
	seenDir := make(map[string]bool, len(paths))
	for _, path := range paths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve path %q: %w", path, err)
		}
		watched[absPath] = true
		if dir := filepath.Dir(absPath); !seenDir[dir] {
			seenDir[dir] = true
			dirs = append(dirs, dir)
		}
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	// Watch each parent directory.  This catches:
	//   • direct writes      → Write event on the file
	//   • atomic rename-over → Create event on the file path
	//   • delete + recreate  → Create event on the file path
	for _, dir := range dirs {
		if err := w.Add(dir); err != nil {
			return fmt.Errorf("watch directory %q: %w", dir, err)
		}
	}

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		var timerChan <-chan time.Time
		if timer != nil {
			timerChan = timer.C
		}

		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			// Ignore events for other files in the same directories.
			if !watched[filepath.Clean(event.Name)] {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if timer == nil {
					timer = time.NewTimer(debounceDelay)
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(debounceDelay)
				}
			}

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			// Surface the error; the caller decides whether to restart the
			// watcher. Silently swallowing ENOSPC or EBADF would leave
			// auto-reload silently broken.
			return fmt.Errorf("fsnotify: %w", err)

		case <-timerChan:
			timer = nil
			onChange()
		}
	}
}
