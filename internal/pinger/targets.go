package pinger

import (
	"context"
	"fmt"
	"sync"

	"github.com/nagayon-935/mping/internal/stats"
)

// IDAllocator separates echo IDs from route-probe IDs. Echo IDs are never
// recycled in a session, including stop/restart and remove/re-add. Short
// payloads and ICMP errors therefore do not need a payload nonce to reject
// replies for a removed worker. Exhaustion fails safely instead of aliasing.
// Echo IDs occupy [0, 32768); reusable route IDs occupy [32768, 65536).
type IDAllocator struct {
	mu     sync.Mutex
	echo   int
	trace  int
	traces map[int]bool
}

// idPartitionSize splits the 16-bit wire ID space equally between echo and route probes.
const idPartitionSize = 1 << 15

func (a *IDAllocator) echoID() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.echo >= idPartitionSize {
		return 0, fmt.Errorf("session echo IDs exhausted; restart mping")
	}
	id := a.echo
	a.echo++
	return id, nil
}

func (a *IDAllocator) traceID() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.traces == nil {
		a.traces = make(map[int]bool)
	}
	for range idPartitionSize {
		id := idPartitionSize + a.trace%idPartitionSize
		a.trace++
		if !a.traces[id] {
			a.traces[id] = true
			return id
		}
	}
	return -1
}

func (a *IDAllocator) releaseTrace(id int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.traces, id)
}

type targetWorker struct {
	id     int
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *Pinger) allocateWorkerID() (int, error) {
	if p.ids != nil {
		return p.ids.echoID()
	}
	if p.nextWorkerID >= 65536 {
		return 0, fmt.Errorf("echo IDs exhausted")
	}
	id := (p.baseID + p.nextWorkerID) & 0xffff
	p.nextWorkerID++
	return id, nil
}

func (p *Pinger) launchTarget(t *stats.TargetStats, id int) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &targetWorker{id: id, cancel: cancel, done: make(chan struct{})}
	p.mapMu.Lock()
	p.workerStates[t] = w
	p.targetChans[id] = make(chan Reply, replyChanBuffer)
	p.activeWorkers++
	p.mapMu.Unlock()
	p.workers.Add(1)
	p.wg.Go(func() {
		defer p.workers.Done()
		defer close(w.done)
		defer cancel()
		p.runTargetWorker(ctx, t, id, p.interval, p.probeTimeout)
		p.mapMu.Lock()
		p.activeWorkers--
		finished := p.activeWorkers == 0
		p.mapMu.Unlock()
		if finished {
			select {
			case p.workerEvents <- struct{}{}:
			default:
			}
		}
	})
}

// AddTarget/RemoveTarget are called by the supervisor command loop. Wait and
// Stop run only after its final mutation, while the receiver keeps wg nonzero.
func (p *Pinger) AddTarget(t *stats.TargetStats, address string) error {
	p.dynamicMu.Lock()
	defer p.dynamicMu.Unlock()
	if p.stopped() {
		return errPingerStopped
	}
	p.mapMu.RLock()
	_, exists := p.workerStates[t]
	p.mapMu.RUnlock()
	if exists {
		return fmt.Errorf("target already registered")
	}
	id, err := p.allocateWorkerID()
	if err != nil {
		return err
	}
	p.mapMu.Lock()
	p.resolveAddresses[t] = address
	p.mapMu.Unlock()
	p.launchTarget(t, id)
	return nil
}

// RemoveTarget cancels and joins the target worker, then unregisters its state.
// Like AddTarget, it is called by the supervisor before Stop and Wait.
func (p *Pinger) RemoveTarget(t *stats.TargetStats) {
	p.dynamicMu.Lock()
	defer p.dynamicMu.Unlock()
	p.mapMu.Lock()
	w := p.workerStates[t]
	if w != nil {
		delete(p.targetChans, w.id)
	}
	p.mapMu.Unlock()
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
	p.mapMu.Lock()
	delete(p.workerStates, t)
	delete(p.resolveAddresses, t)
	p.mapMu.Unlock()
}

// WorkerEvents signals when the last active worker finishes; notifications coalesce.
func (p *Pinger) WorkerEvents() <-chan struct{} { return p.workerEvents }

// WorkersFinished reports whether no target workers remain active.
func (p *Pinger) WorkersFinished() bool {
	p.mapMu.RLock()
	defer p.mapMu.RUnlock()
	return p.activeWorkers == 0
}

// Done is closed by Stop to signal the end of the pinger lifetime.
func (p *Pinger) Done() <-chan struct{} { return p.done }
