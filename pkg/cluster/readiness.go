// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotReady reports that the nodes or Slurm of a Create or WorkerAdd did
// not become ready within its wait limit (config.Cluster.Wait,
// WorkerAddOptions.Wait).
var ErrNotReady = errors.New("not ready")

// errWaitExpired is the cause of a readiness context that its wait limit
// ended.
var errWaitExpired = errors.New("readiness wait limit reached")

// readiness bounds how long one Create or WorkerAdd waits for its nodes and
// Slurm. Each node gets wait from when its container has started, so that
// image pulls do not count, and the steps after setupNodes end wait after
// the last node container started. A wait of zero or less sets no limit.
type readiness struct {
	interval time.Duration // delay between probe rounds
	wait     time.Duration

	mu   sync.Mutex
	last time.Time         // the latest deadline handed out
	ctxs []context.Context // the contexts handed out, to tell expiry
}

// nodeContext returns the context for the steps that follow the start of a
// node's container: ctx, ending wait from now.
func (r *readiness) nodeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.wait <= 0 {
		return ctx, func() {}
	}
	deadline := time.Now().Add(r.wait)
	nctx, cancel := context.WithDeadlineCause(ctx, deadline, errWaitExpired)
	r.mu.Lock()
	defer r.mu.Unlock()
	if deadline.After(r.last) {
		r.last = deadline
	}
	r.ctxs = append(r.ctxs, nctx)
	return nctx, cancel
}

// context returns the context for the steps after setupNodes: ctx, ending
// with the last node's wait.
func (r *readiness) context(ctx context.Context) (context.Context, context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last.IsZero() {
		return ctx, func() {}
	}
	rctx, cancel := context.WithDeadlineCause(ctx, r.last, errWaitExpired)
	r.ctxs = append(r.ctxs, rctx)
	return rctx, cancel
}

// expired reports whether the wait limit ended one of the contexts handed
// out, rather than an error or the parent context.
func (r *readiness) expired() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ctx := range r.ctxs {
		if errors.Is(context.Cause(ctx), errWaitExpired) {
			return true
		}
	}
	return false
}
