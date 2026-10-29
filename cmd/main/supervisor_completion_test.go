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
