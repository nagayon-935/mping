package pinger

import (
	"context"
	"fmt"
	"time"
)

// lookupContext combines the operation deadline with the pinger lifetime.
// Context-aware DNS runs directly on its owner (worker, metadata task or
// route probe), so it returns before that owner can finish its join.
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

// lookupBoundedContext runs context-aware DNS on the caller, or bounds the
// wait for an explicitly injected context-free hook. It honors the operation
// deadline, parent cancellation, and the pinger lifetime.
func lookupBoundedContext[T any](
	parent context.Context,
	p *Pinger,
	timeout time.Duration,
	kind, query string,
	ctxFn func(context.Context, string) (T, error),
	legacyFn func(string) (T, error),
) (T, error) {
	var zero T
	if p.stopped() {
		return zero, errPingerStopped
	}
	if ctxFn != nil {
		ctx, cancel := p.lookupContext(parent, timeout)
		defer cancel()
		val, err := ctxFn(ctx, query)
		if p.stopped() {
			return zero, errPingerStopped
		}
		return val, err
	}
	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		val, err := legacyFn(query)
		ch <- result{val, err}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-p.done:
		return zero, errPingerStopped
	case <-parent.Done():
		return zero, parent.Err()
	case <-time.After(timeout):
		return zero, fmt.Errorf("%s lookup for %q timed out after %s", kind, query, timeout)
	}
}
