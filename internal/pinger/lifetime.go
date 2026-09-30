package pinger

import (
	"context"
	"time"
)

// lookupContext combines the operation deadline with the pinger lifetime.
// Context-aware DNS runs directly on its owner (worker, metadata task or
// traceroute), so it returns before that owner can finish its join.
func (p *Pinger) lookupContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	if p.ctx == nil { // manually constructed test fixtures
		return ctx, cancel
	}
	if p.ctx.Err() != nil {
		cancel()
	}
	stop := context.AfterFunc(p.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (p *Pinger) stopped() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
