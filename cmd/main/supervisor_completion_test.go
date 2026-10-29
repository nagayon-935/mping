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

// TestCountObserverGivesUpAfterShutdownWhenQueueIsFull pins the observer's
// escape hatch: a completion notification that cannot be queued (command loop
// gone, buffer full) must not block Shutdown, which joins the observer. The
// full buffer makes the select deterministic; with free space the send and
// the closed done channel would both be ready and either could win.
func TestCountObserverGivesUpAfterShutdownWhenQueueIsFull(t *testing.T) {
	p := &fakePinger{workersDone: make(chan struct{})} // Stop does not release the workers
	sup := newSupervisor(supervisorConfig{countLimited: true, makePinger: func(int) pingerController { return p }})
	sup.Start()
	if err := sup.startPinger(); err != nil {
		t.Fatal(err)
	}
	shutdownDone := make(chan struct{})
	go func() { sup.Shutdown(); close(shutdownDone) }()

	<-sup.loopDone // the loop has exited, so nothing drains cmds any more
	for len(sup.cmds) < cap(sup.cmds) {
		sup.cmds <- command{kind: cmdStop}
	}
	p.finish() // the observer now tries to notify and must take the done branch

	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return: the completion observer is stuck sending")
	}
}
