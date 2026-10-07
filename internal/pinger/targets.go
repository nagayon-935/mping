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
type IDAllocator struct {
	mu     sync.Mutex
	echo   int
	trace  int
	traces map[int]bool
}

func (a *IDAllocator) echoID() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.echo >= 32768 {
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
	for n := 0; n < 32768; n++ {
		id := 32768 + a.trace%32768
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
	id      int
	removed <-chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
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
	w := &targetWorker{id: id, removed: ctx.Done(), cancel: cancel, done: make(chan struct{})}
	p.mapMu.Lock()
	p.workerStates[t] = w
	p.targetMap[id] = t
	p.targetChans[id] = make(chan Reply, replyChanBuffer)
	p.activeWorkers++
	p.mapMu.Unlock()
	p.wg.Add(1)
	p.workers.Add(1)
	go func() {
		defer p.wg.Done()
		defer p.workers.Done()
		defer close(w.done)
		defer cancel()
		p.runTargetWorker(t, id, p.interval, p.probeTimeout, ctx)
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
	}()
}

// AddTarget/RemoveTarget are called by the supervisor command loop. Wait and
// Stop run only after its final mutation, while the receiver keeps wg nonzero.
func (p *Pinger) AddTarget(t *stats.TargetStats, address string, dscp *int) error {
	p.dynamicMu.Lock()
	defer p.dynamicMu.Unlock()
	if p.stopped() {
		return fmt.Errorf("pinger stopped")
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
	if dscp != nil {
		if p.TargetDSCP == nil {
			p.TargetDSCP = make(map[*stats.TargetStats]int)
		}
		p.TargetDSCP[t] = *dscp
	}
	p.mapMu.Unlock()
	p.launchTarget(t, id)
	return nil
}

func (p *Pinger) RemoveTarget(t *stats.TargetStats) {
	p.dynamicMu.Lock()
	defer p.dynamicMu.Unlock()
	p.mapMu.Lock()
	w := p.workerStates[t]
	if w != nil {
		delete(p.targetMap, w.id)
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
	delete(p.TargetDSCP, t)
	p.mapMu.Unlock()
}

func (p *Pinger) WorkerEvents() <-chan struct{} { return p.workerEvents }
func (p *Pinger) WorkersFinished() bool {
	p.mapMu.RLock()
	defer p.mapMu.RUnlock()
	return p.activeWorkers == 0
}

func (p *Pinger) Done() <-chan struct{} { return p.done }
