package main

import (
	"sync"
	"testing"
	"time"
)

// fakeCheckerStopper is a minimal checkerStopper double for exercising
// resetChecker's locking behavior in isolation from the real port/HTTP
// checkers, which can't easily be swapped into supervisor's concretely
// typed fields.
type fakeCheckerStopper struct {
	stopped bool
	waited  bool
	// slow, when true, makes Stop() sleep briefly. This widens the window
	// in which a concurrent reader could observe a torn (nil) field value
	// if resetChecker didn't hold mu across its whole body — without it,
	// the race window is too narrow to reliably hit even across many
	// iterations.
	slow bool
}

func (f *fakeCheckerStopper) Stop() {
	if f.slow {
		time.Sleep(5 * time.Millisecond)
	}
	f.stopped = true
}

func (f *fakeCheckerStopper) Wait() { f.waited = true }

// TestResetChecker_NoObservableNilWindow verifies the F3 fix: a concurrent
// reader that takes mu, reads the field, and releases mu — mirroring
// stopAll's `mu.Lock(); curPort := s.portChecker; mu.Unlock()` pattern —
// must never observe the zero value while a resetChecker call is in
// flight. Before the fix, resetChecker briefly zeroed *field while mu was
// released (during the old checker's Stop/Wait and the new one's create),
// which a concurrent stopAll could observe and mistakenly treat as "nothing
// to stop", letting the newly-created checker's goroutines leak (TD-46②).
func TestResetChecker_NoObservableNilWindow(t *testing.T) {
	var mu sync.Mutex
	field := &fakeCheckerStopper{slow: true}

	const iterations = 200
	for i := 0; i < iterations; i++ {
		var wg sync.WaitGroup
		var observedNil bool

		wg.Add(2)
		go func() {
			defer wg.Done()
			resetChecker(&mu, &field, func() *fakeCheckerStopper {
				return &fakeCheckerStopper{slow: true}
			})
		}()
		go func() {
			defer wg.Done()
			mu.Lock()
			cur := field
			mu.Unlock()
			if cur == nil {
				observedNil = true
			}
		}()
		wg.Wait()

		if observedNil {
			t.Fatalf("iteration %d: concurrent reader observed a nil field mid-reset", i)
		}
	}
}

// TestResetChecker_StopsOldAndInstallsNew is a basic behavioral regression
// check: resetChecker must still stop the previous value and install a
// freshly created one, independent of the locking change above.
func TestResetChecker_StopsOldAndInstallsNew(t *testing.T) {
	var mu sync.Mutex
	old := &fakeCheckerStopper{}
	field := old
	created := &fakeCheckerStopper{}

	resetChecker(&mu, &field, func() *fakeCheckerStopper { return created })

	if !old.stopped || !old.waited {
		t.Errorf("expected old checker to be Stop()/Wait()ed, got stopped=%v waited=%v", old.stopped, old.waited)
	}
	if field != created {
		t.Errorf("expected field to hold the newly created checker, got %p want %p", field, created)
	}
}

// TestResetChecker_NilFieldSkipsStop verifies resetChecker doesn't call
// Stop/Wait on a nil previous value (the "first setup" case).
func TestResetChecker_NilFieldSkipsStop(t *testing.T) {
	var mu sync.Mutex
	var field *fakeCheckerStopper
	created := &fakeCheckerStopper{}

	resetChecker(&mu, &field, func() *fakeCheckerStopper { return created })

	if field != created {
		t.Errorf("expected field to hold the newly created checker, got %p want %p", field, created)
	}
}
