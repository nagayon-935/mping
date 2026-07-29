package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nagayon-935/mping/internal/stats"
)

func newLoopTestSupervisor(t *testing.T) *supervisor {
	t.Helper()
	return newSupervisor(supervisorConfig{
		makePinger: func(size int) pingerController { return &lifecycleFakePinger{} },
		targets:    []*stats.TargetStats{stats.NewTargetStats("example.com")},
		interval:   time.Second,
		timeout:    time.Second,
		logCh:      make(chan string, 8),
	})
}

// TestLoop_ExecutesCommandsInOrder pins the FIFO contract: every command is
// executed, in the order it was enqueued, with no coalescing.
func TestLoop_ExecutesCommandsInOrder(t *testing.T) {
	sup := newLoopTestSupervisor(t)
	sup.Start()
	defer sup.Shutdown()

	if err := sup.do(cmdStart); err != nil {
		t.Fatalf("cmdStart: %v", err)
	}
	if err := sup.do(cmdStop); err != nil {
		t.Fatalf("cmdStop: %v", err)
	}
	if got := sup.stateSnapshot(); got != stateStopped {
		t.Fatalf("after stop: state = %v, want stopped", got)
	}
	if err := sup.do(cmdRestart); err != nil {
		t.Fatalf("cmdRestart: %v", err)
	}
	if got := sup.stateSnapshot(); got != stateRunning {
		t.Fatalf("after restart: state = %v, want running", got)
	}
}

// TestDo_AfterShutdownReturnsSentinel covers the send-side race: a UI
// goroutine still in flight when run() shuts the loop down must get a clean
// error instead of blocking forever or panicking on a closed channel.
func TestDo_AfterShutdownReturnsSentinel(t *testing.T) {
	sup := newLoopTestSupervisor(t)
	sup.Start()
	sup.Shutdown()

	if err := sup.do(cmdRestart); !errors.Is(err, errSupervisorShutDown) {
		t.Fatalf("err = %v, want errSupervisorShutDown", err)
	}
}

// TestDo_ConcurrentSendersDuringShutdown is the stress form: many senders
// racing a Shutdown must all return and none may panic or hang.
func TestDo_ConcurrentSendersDuringShutdown(t *testing.T) {
	for i := 0; i < 50; i++ {
		sup := newLoopTestSupervisor(t)
		sup.Start()

		var wg sync.WaitGroup
		start := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_ = sup.do(cmdResetPort)
			}()
		}
		close(start)
		sup.Shutdown()

		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: senders did not all return after Shutdown", i)
		}
	}
}

// TestShutdown_IsIdempotent — run()'s error path and its normal cleanup path
// can both reach Shutdown.
func TestShutdown_IsIdempotent(t *testing.T) {
	sup := newLoopTestSupervisor(t)
	sup.Start()
	sup.Shutdown()
	sup.Shutdown()
}
