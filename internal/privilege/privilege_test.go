package privilege

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIDs replaces the process-credential hooks with an in-memory model and
// records every set call, so the root-only paths run without privileges.
type fakeIDs struct {
	mu                   sync.Mutex
	uid, euid, gid, egid int
	calls                []string
	failSeteuidTo        int // -1: never fail
}

func installFake(t *testing.T, f *fakeIDs) {
	t.Helper()
	orig := sys
	t.Cleanup(func() { sys = orig })
	sys = credentials{
		getuid:  func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.uid },
		geteuid: func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.euid },
		getgid:  func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.gid },
		getegid: func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.egid },
		seteuid: func(id int) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls = append(f.calls, "seteuid "+strconv.Itoa(id))
			if id == f.failSeteuidTo {
				return errors.New("operation not permitted")
			}
			f.euid = id
			return nil
		},
		setegid: func(id int) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls = append(f.calls, "setegid "+strconv.Itoa(id))
			f.egid = id
			return nil
		},
	}
}

func TestAsRealUser_SetuidDropsAndRestores(t *testing.T) {
	f := &fakeIDs{uid: 501, euid: 0, gid: 20, egid: 0, failSeteuidTo: -1}
	installFake(t, f)
	var during [2]int

	err := AsRealUser(func() error {
		during = [2]int{sys.geteuid(), sys.getegid()}
		return nil
	})

	if err != nil {
		t.Fatalf("AsRealUser: %v", err)
	}
	if during != [2]int{501, 20} {
		t.Fatalf("fn ran as euid/egid %v, want [501 20]", during)
	}
	// Drop the group first (needs root), restore the user first (to regain root).
	want := []string{"setegid 20", "seteuid 501", "seteuid 0", "setegid 0"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
	if f.euid != 0 || f.egid != 0 {
		t.Fatalf("not restored: euid=%d egid=%d", f.euid, f.egid)
	}
}

func TestAsRealUser_NoopWhenNotSetuid(t *testing.T) {
	tests := []struct {
		name                 string
		uid, euid, gid, egid int
	}{
		{name: "ordinary user", uid: 501, euid: 501, gid: 20, egid: 20},
		{name: "sudo (real and effective root)", uid: 0, euid: 0, gid: 0, egid: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeIDs{uid: tt.uid, euid: tt.euid, gid: tt.gid, egid: tt.egid, failSeteuidTo: -1}
			installFake(t, f)
			ran := false

			err := AsRealUser(func() error { ran = true; return nil })

			if err != nil || !ran {
				t.Fatalf("err=%v ran=%v, want fn run without error", err, ran)
			}
			if len(f.calls) != 0 {
				t.Fatalf("unexpected credential changes: %v", f.calls)
			}
		})
	}
}

func TestAsRealUser_PropagatesFnError(t *testing.T) {
	f := &fakeIDs{uid: 501, euid: 0, gid: 20, egid: 0, failSeteuidTo: -1}
	installFake(t, f)
	want := errors.New("boom")

	err := AsRealUser(func() error { return want })

	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if f.euid != 0 {
		t.Fatal("privileges not restored after fn error")
	}
}

func TestAsRealUser_DropFailureSkipsFn(t *testing.T) {
	f := &fakeIDs{uid: 501, euid: 0, gid: 20, egid: 0, failSeteuidTo: 501}
	installFake(t, f)
	ran := false

	err := AsRealUser(func() error { ran = true; return nil })

	if err == nil || !strings.Contains(err.Error(), "drop privileges") {
		t.Fatalf("err = %v, want a drop-privileges error", err)
	}
	if ran {
		t.Fatal("fn must not run with privileges still held")
	}
	if f.egid != 0 {
		t.Fatalf("egid not restored after failed drop: %d", f.egid)
	}
}

func TestAsRealUser_RestoreFailureIsReported(t *testing.T) {
	f := &fakeIDs{uid: 501, euid: 0, gid: 20, egid: 0, failSeteuidTo: 0}
	installFake(t, f)

	err := AsRealUser(func() error { return nil })

	if err == nil || !strings.Contains(err.Error(), "restore privileges") {
		t.Fatalf("err = %v, want a restore-privileges error", err)
	}
}

func TestPrivileged_WaitsForAsRealUser(t *testing.T) {
	f := &fakeIDs{uid: 501, euid: 0, gid: 20, egid: 0, failSeteuidTo: -1}
	installFake(t, f)
	inside, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = AsRealUser(func() error { close(inside); <-release; return nil })
	}()
	<-inside

	done := make(chan int, 1)
	go func() {
		_ = Privileged(func() error { done <- sys.geteuid(); return nil })
	}()

	select {
	case <-done:
		t.Fatal("Privileged ran while privileges were dropped")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case euid := <-done:
		if euid != 0 {
			t.Fatalf("Privileged ran with euid %d, want 0", euid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Privileged never ran after AsRealUser finished")
	}
}

func TestPrivileged_SectionsRunConcurrently(t *testing.T) {
	inside, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = Privileged(func() error { close(inside); <-release; return nil })
	}()
	<-inside
	defer close(release)

	done := make(chan struct{})
	go func() { _ = Privileged(func() error { close(done); return nil }) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a second Privileged section was blocked by the first")
	}
}
