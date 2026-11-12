// SPDX-License-Identifier: LGPL-3.0-or-later

// Package detach gives a write that must not be cut short, such as of the
// mesh DNS or of sind-nodes.conf, a context that ignores the cancellation
// and the deadline of the caller's, as Ctrl+C or the --wait limit would
// kill the docker CLI halfway. Under a rollback it keeps the rollback's
// deadline, so that a hung Docker daemon cannot hold the rollback past it.
package detach

import (
	"context"
	"time"
)

// rollbackKey is the key of the deadline that Rollback stores in a
// context.
type rollbackKey struct{}

// Rollback returns ctx, the context of a rollback, with its deadline as
// the bound of the contexts that Context derives from it. Without a
// deadline, ctx is returned as it is.
func Rollback(ctx context.Context) context.Context {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, rollbackKey{}, deadline)
}

// Context returns the context for a write that must not be cut short. It
// ignores the cancellation and the deadline of ctx, but ends at the
// deadline of the rollback that ctx runs under (Rollback), if any. Call
// cancel when the write is done.
func Context(ctx context.Context) (_ context.Context, cancel context.CancelFunc) {
	wctx := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Value(rollbackKey{}).(time.Time); ok {
		return context.WithDeadline(wctx, deadline)
	}
	return wctx, func() {}
}
