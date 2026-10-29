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
	failSeteuidTo        int // seteuid(id) fails when id equals this; -1: never fail
	failSetegidTo        int // setegid(id) fails when id equals this; -1: never fail
}

// newFakeIDs returns credentials that never fail; tests set failSeteuidTo or
// failSetegidTo to inject a failure.
func newFakeIDs(uid, euid, gid, egid int) *fakeIDs {
	return &fakeIDs{uid: uid, euid: euid, gid: gid, egid: egid, failSeteuidTo: -1, failSetegidTo: -1}
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
			if id == f.failSetegidTo {
				return errors.New("operation not permitted")
			}
			f.egid = id
			return nil
		},
	}
}

func TestAsRealUser_SetuidDropsAndRestores(t *testing.T) {
	f := newFakeIDs(501, 0, 20, 0)
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
			f := newFakeIDs(tt.uid, tt.euid, tt.gid, tt.egid)
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
	f := newFakeIDs(501, 0, 20, 0)
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

// TestAsRealUser_CredentialFailures covers every credential call failing in
// turn: while dropping, fn must not run and the original IDs must be
// regained where possible; while restoring, fn has run and the failure is
// reported. The fake starts as setuid root (uid 501/gid 20, euid/egid 0).
func TestAsRealUser_CredentialFailures(t *testing.T) {
	const never = -1
	fnErr := errors.New("fn failed")
	tests := []struct {
		name          string
		failSeteuidTo int
		failSetegidTo int
		fnErr         error
		wantRan       bool
		wantErr       []string // substrings the error must contain
		wantNotErr    []string // substrings the error must not contain
		wantCalls     []string
		wantEuid      int
		wantEgid      int
	}{
		{
			name: "seteuid fails while dropping", failSeteuidTo: 501, failSetegidTo: never,
			wantErr:    []string{"drop privileges"},
			wantNotErr: []string{"restoring group also failed"},
			wantCalls:  []string{"setegid 20", "seteuid 501", "setegid 0"},
			wantEuid:   0, wantEgid: 0, // the group is handed back
		},
		{
			name: "setegid fails while dropping", failSeteuidTo: never, failSetegidTo: 20,
			wantErr:   []string{"drop privileges"},
			wantCalls: []string{"setegid 20"}, // the user switch is never attempted
			wantEuid:  0, wantEgid: 0,
		},
		{
			name: "group restore also fails after a failed drop", failSeteuidTo: 501, failSetegidTo: 0,
			wantErr:   []string{"drop privileges", "restoring group also failed"},
			wantCalls: []string{"setegid 20", "seteuid 501", "setegid 0"},
			wantEuid:  0, wantEgid: 20,
		},
		{
			name: "seteuid fails while restoring", failSeteuidTo: 0, failSetegidTo: never,
			wantRan:   true,
			wantErr:   []string{"restore privileges"},
			wantCalls: []string{"setegid 20", "seteuid 501", "seteuid 0"}, // setegid is not attempted
			wantEuid:  501, wantEgid: 20,
		},
		{
			name: "setegid fails while restoring", failSeteuidTo: never, failSetegidTo: 0,
			wantRan:   true,
			wantErr:   []string{"restore privileges"},
			wantCalls: []string{"setegid 20", "seteuid 501", "seteuid 0", "setegid 0"},
			wantEuid:  0, wantEgid: 20,
		},
		{
			name: "restore failure is joined with fn's own error", failSeteuidTo: 0, failSetegidTo: never,
			fnErr:     fnErr,
			wantRan:   true,
			wantErr:   []string{"restore privileges", "fn failed"},
			wantCalls: []string{"setegid 20", "seteuid 501", "seteuid 0"},
			wantEuid:  501, wantEgid: 20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeIDs(501, 0, 20, 0)
			f.failSeteuidTo, f.failSetegidTo = tt.failSeteuidTo, tt.failSetegidTo
			installFake(t, f)
			ran := false

			err := AsRealUser(func() error { ran = true; return tt.fnErr })

			if err == nil {
				t.Fatal("AsRealUser returned nil, want an error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %q, want it to contain %q", err, want)
				}
			}
			for _, bad := range tt.wantNotErr {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("err = %q, must not contain %q", err, bad)
				}
			}
			if tt.fnErr != nil && !errors.Is(err, tt.fnErr) {
				t.Errorf("err = %v, want it to wrap fn's error %v", err, tt.fnErr)
			}
			if ran != tt.wantRan {
				t.Errorf("fn ran = %v, want %v (fn must not run with privileges still held)", ran, tt.wantRan)
			}
			if !reflect.DeepEqual(f.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", f.calls, tt.wantCalls)
			}
			if f.euid != tt.wantEuid || f.egid != tt.wantEgid {
				t.Errorf("final euid/egid = %d/%d, want %d/%d", f.euid, f.egid, tt.wantEuid, tt.wantEgid)
			}
		})
	}
}

func TestPrivileged_WaitsForAsRealUser(t *testing.T) {
	f := newFakeIDs(501, 0, 20, 0)
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
