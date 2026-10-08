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

// refreshedHook, when non-nil, is called after each post-change path
// refresh in WatchPaths. Tests use it to wait for the refresh instead of
// sleeping; it is always nil in production.
var refreshedHook func()

// Watch monitors the file at path for content changes (Write or Create events).
// It is WatchFiles with a single path.
func Watch(ctx context.Context, path string, onChange func()) error {
	return WatchFiles(ctx, []string{path}, onChange)
}

// WatchFiles monitors every file in paths for content changes (Write or
// Create events) and calls onChange once per debounced burst of changes to
// any of them. A listed file need not exist yet: creating it counts as a
// change. Duplicate paths are watched once. It is WatchPaths with a fixed
// path set.
func WatchFiles(ctx context.Context, paths []string, onChange func()) error {
	return WatchPaths(ctx, func() []string { return paths }, onChange)
}

// WatchPaths is WatchFiles with a path set that may change: paths is called
// at startup and again after every onChange, so a change that names new
// files (e.g. a hosts file gaining an include) widens the watch without a
// restart. Paths dropped from the set stop triggering onChange.
//
// The parent directories are watched rather than the files themselves so
// that editor save patterns that atomically replace a file via rename are
// correctly detected (e.g. vim :w, nano, many CLI tools).
//
// A 200 ms debounce timer coalesces rapid successive events into a single
// onChange call.
//
// WatchPaths blocks until ctx is cancelled, then returns nil.
// A non-nil error is returned for setup failures (e.g. fsnotify init, an
// unreadable directory at startup) or for runtime fsnotify errors (e.g.
// ENOSPC, EBADF) that would leave auto-reload silently broken if ignored.
// A directory that can't be watched during a later refresh (e.g. a newly
// named one that doesn't exist yet) is skipped and retried on the next
// refresh instead, so a bad edit can't disable reloading altogether.
func WatchPaths(ctx context.Context, paths func() []string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	// Watch each parent directory.  This catches:
	//   • direct writes      → Write event on the file
	//   • atomic rename-over → Create event on the file path
	//   • delete + recreate  → Create event on the file path
	addedDirs := make(map[string]bool)
	var watched map[string]bool
	refresh := func(strict bool) error {
		next := make(map[string]bool)
		for _, path := range paths() {
			// Resolve to absolute paths so we can compare event paths
			// regardless of how the caller expressed them.
			absPath, err := filepath.Abs(path)
			if err != nil {
				if strict {
					return fmt.Errorf("resolve path %q: %w", path, err)
				}
				continue
			}
			dir := filepath.Dir(absPath)
			if !addedDirs[dir] {
				if err := w.Add(dir); err != nil {
					if strict {
						return fmt.Errorf("watch directory %q: %w", dir, err)
					}
					continue
				}
				addedDirs[dir] = true
			}
			next[absPath] = true
		}
		watched = next
		return nil
	}
	if err := refresh(true); err != nil {
		return err
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
			_ = refresh(false) // non-strict: never returns an error
			if refreshedHook != nil {
				refreshedHook()
			}
		}
	}
}
