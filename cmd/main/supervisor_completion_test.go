package main

import (
	"testing"
	"time"
)

// newCompletingPinger returns a fakePinger whose probes complete only when
// finish() is called or the pinger is stopped, like a count-limited run.
func newCompletingPinger() *fakePinger {
	p := &fakePinger{workersDone: make(chan struct{})}
	p.onStop = p.finish
	return p
}

func TestSupervisorNotifiesEachCountCompletion(t *testing.T) {
	var created []*fakePinger
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController {
		p := newCompletingPinger()
		created = append(created, p)
		return p
	}})
	sup.Start()
	t.Cleanup(func() { _ = sup.do(cmdTerminate); sup.Shutdown() })
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if i > 0 {
			if err := sup.do(cmdRestart); err != nil {
				t.Fatal(err)
			}
		}
		created[i].finish()
		select {
		case <-sup.finished:
		case <-time.After(time.Second):
			t.Fatal("no count completion notification")
		}
		if sup.stateSnapshot() != stateRunning {
			t.Fatal("count completion must preserve other monitors")
		}
	}
	// A stale completion from the first pinger must not complete a new run.
	if err := sup.do(cmdRestart); err != nil {
		t.Fatal(err)
	}
	reply := make(chan error, 1)
	sup.cmds <- command{kind: cmdProbesFinished, pinger: created[0], reply: reply}
	if err := <-reply; err != nil {
		t.Fatal(err)
	}
	select {
	case <-sup.finished:
		t.Fatal("stale completion notified")
	default:
	}
	sup.stopAll()
	// The completion observer may enqueue after stop. A stale event is rejected
	// by the state check regardless of command ordering.
	sup.cmds <- command{kind: cmdProbesFinished, pinger: created[2], reply: reply}
	<-reply
	select {
	case <-sup.finished:
		t.Fatal("explicit stop notified as count completion")
	default:
	}
}

func TestSupervisorShutdownJoinsCompletionObservers(t *testing.T) {
	p := newCompletingPinger()
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	sup.Shutdown()
	if !p.isReleased() || sup.stateSnapshot() != stateTerminated {
		t.Fatal("measurement still live after Shutdown")
	}
	select {
	case <-sup.loopDone:
	default:
		t.Fatal("command loop still live")
	}
	// Shutdown has already waited for observers; repeated joins must return.
	done := make(chan struct{})
	go func() { sup.observers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("completion observer leaked")
	}
}

// TestCountObserverDoesNotNotifyAfterShutdown pins that a completion arriving
// after Shutdown closed done is dropped rather than queued, even though cmds
// has room for it, and that Shutdown (which joins the observer) still returns.
func TestCountObserverDoesNotNotifyAfterShutdown(t *testing.T) {
	p := &fakePinger{workersDone: make(chan struct{})} // Stop does not release the workers
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	shutdownDone := make(chan struct{})
	go func() { sup.Shutdown(); close(shutdownDone) }()

	<-sup.loopDone // done is closed and nothing drains cmds any more
	p.finish()     // the observer wakes with room in cmds and done closed

	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return: the completion observer is stuck")
	}
	if n := len(sup.cmds); n != 0 {
		t.Fatalf("observer queued %d command(s) after shutdown", n)
	}
}

// TestCountObserverGivesUpWhenShutdownInterruptsASend covers the other exit:
// an observer already blocked sending to a queue nobody drains must return
// once done closes.
func TestCountObserverGivesUpWhenShutdownInterruptsASend(t *testing.T) {
	p := &fakePinger{workersDone: make(chan struct{})}
	sup := newSupervisor(supervisorConfig{})
	sup.cmds = make(chan command) // unbuffered and never read
	sup.done = make(chan struct{})
	sup.startCountObserver(p)
	p.finish()

	joined := make(chan struct{})
	go func() { sup.observers.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("observer returned before shutdown although its send cannot complete")
	case <-time.After(20 * time.Millisecond):
	}
	close(sup.done)
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("the completion observer is stuck sending after shutdown")
	}
}
