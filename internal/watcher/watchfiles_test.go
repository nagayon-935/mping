package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestWatchFiles_AnyListedFileTriggers verifies that a change to any of the
// watched files — here an include file in a different directory than the
// main file, listed twice — fires onChange, while unlisted siblings stay
// ignored.
func TestWatchFiles_AnyListedFileTriggers(t *testing.T) {
	mainDir, incDir := t.TempDir(), t.TempDir()
	mainPath := filepath.Join(mainDir, "hosts.yaml")
	incPath := filepath.Join(incDir, "list.csv")
	sibling := filepath.Join(incDir, "other.csv")
	for _, p := range []string{mainPath, incPath, sibling} {
		writeFile(t, p, "x\n")
	}
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchFiles(ctx, []string{mainPath, incPath, incPath}, func() { called.Add(1) })
	})
	defer stop()

	touch(t, incPath)
	waitForMore(t, &called, 0, "the first change")
	before := called.Load()
	touch(t, mainPath)
	waitForMore(t, &called, before, "changing the main file")
	before = called.Load()

	writeFile(t, sibling, "changed\n")

	if n := settle(t, &called); n != before {
		t.Fatalf("unlisted sibling triggered onChange (%d → %d calls)", before, n)
	}
}

// TestWatchFiles_CreatingMissingFileTriggers covers an include file that
// does not exist yet: creating it must trigger a reload.
func TestWatchFiles_CreatingMissingFileTriggers(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "later.csv")
	var called atomic.Int32
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchFiles(ctx, []string{missing}, func() { called.Add(1) })
	})
	defer stop()

	writeFile(t, missing, "10.0.0.1\n")

	waitForMore(t, &called, 0, "creating the missing file")
}

// TestWatchPaths_RefreshesPathSetAfterChange covers a hosts file edited to
// change the path set: after its change fires, the set is re-read, so a
// newly listed file (an include in a not-yet-watched directory) starts
// triggering without touching the hosts file again, and a dropped one
// stops.
func TestWatchPaths_RefreshesPathSetAfterChange(t *testing.T) {
	tests := []struct {
		name         string
		before       func(mainPath, incPath string) []string // set at startup
		after        func(mainPath, incPath string) []string // set once the hosts file has changed
		incExists    bool                                    // whether the include file exists before the hosts file changes
		wantTriggers bool                                    // whether changing the include file fires after the refresh
	}{
		{
			name:         "newly listed file starts triggering",
			before:       func(m, _ string) []string { return []string{m} },
			after:        func(m, inc string) []string { return []string{m, inc} },
			wantTriggers: true,
		},
		{
			name:      "dropped file stops triggering",
			before:    func(m, inc string) []string { return []string{m, inc} },
			after:     func(m, _ string) []string { return []string{m} },
			incExists: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refreshed := hookRefreshes(t)
			mainPath := filepath.Join(t.TempDir(), "hosts.yaml")
			incPath := filepath.Join(t.TempDir(), "inc.csv")
			writeFile(t, mainPath, "x\n")
			if tt.incExists {
				writeFile(t, incPath, "x\n")
			}
			var called atomic.Int32
			paths := pathSets(tt.before(mainPath, incPath), tt.after(mainPath, incPath))
			stop := startWatch(t, func(ctx context.Context) error {
				return WatchPaths(ctx, paths, func() { called.Add(1) })
			})
			defer stop()

			// The hosts-file change that fires also re-reads the path set.
			touch(t, mainPath)
			waitForMore(t, &called, 0, "the first change")
			waitRefresh(t, refreshed)
			before := called.Load()

			writeFile(t, incPath, "changed\n")

			if tt.wantTriggers {
				waitForMore(t, &called, before, "changing the newly listed include file")
			} else if n := settle(t, &called); n != before {
				t.Fatalf("dropped include still triggered onChange (%d → %d calls)", before, n)
			}
		})
	}
}

// TestWatchPaths_RefreshRetriesDirectoryThatCouldNotBeWatched covers a path
// whose directory does not exist yet when the set is refreshed: it is
// skipped without stopping the watcher, and once the directory appears the
// next refresh picks it up.
func TestWatchPaths_RefreshRetriesDirectoryThatCouldNotBeWatched(t *testing.T) {
	refreshed := hookRefreshes(t)
	mainPath := filepath.Join(t.TempDir(), "hosts.yaml")
	lateDir := filepath.Join(t.TempDir(), "later")
	latePath := filepath.Join(lateDir, "inc.csv")
	writeFile(t, mainPath, "x\n")
	var called atomic.Int32
	paths := pathSets([]string{mainPath}, []string{mainPath, latePath})
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchPaths(ctx, paths, func() { called.Add(1) })
	})
	defer stop()

	// The first change lists latePath, whose directory is missing: skipped.
	touch(t, mainPath)
	waitForMore(t, &called, 0, "the first change")
	waitRefresh(t, refreshed)
	before := called.Load()

	// The watcher must still run, and the next refresh must see the directory.
	if err := os.Mkdir(lateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, mainPath)
	waitForMore(t, &called, before, "changing the hosts file after the failed refresh")
	waitRefresh(t, refreshed)
	before = called.Load()

	writeFile(t, latePath, "10.0.0.1\n")

	waitForMore(t, &called, before, "creating a file in the directory that became watchable")
}

// TestWatchPaths_RefreshSkipsPathItCannotResolve covers a refresh that lists
// a relative path while the working directory is unusable: the path is
// skipped, but the watcher keeps reacting to the files it already watches.
func TestWatchPaths_RefreshSkipsPathItCannotResolve(t *testing.T) {
	refreshed := hookRefreshes(t)
	mainPath := filepath.Join(t.TempDir(), "hosts.yaml")
	writeFile(t, mainPath, "x\n")
	var called atomic.Int32
	paths := pathSets([]string{mainPath}, []string{mainPath, "inc.csv"})
	stop := startWatch(t, func(ctx context.Context) error {
		return WatchPaths(ctx, paths, func() { called.Add(1) })
	})
	defer stop()
	breakCwd(t) // after the start, so only the refresh sees the broken directory

	touch(t, mainPath)
	waitForMore(t, &called, 0, "the first change")
	waitRefresh(t, refreshed)
	before := called.Load()

	touch(t, mainPath)

	waitForMore(t, &called, before, "changing the hosts file after the failed refresh")
}
