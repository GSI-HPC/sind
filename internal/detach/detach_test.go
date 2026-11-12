// SPDX-License-Identifier: LGPL-3.0-or-later

package detach

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextIgnoresCancelAndDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	cancel()

	wctx, wcancel := Context(ctx)
	defer wcancel()

	require.NoError(t, wctx.Err())
	_, ok := wctx.Deadline()
	assert.False(t, ok, "the caller's deadline does not bound the write")
}

func TestContextKeepsTheRollbackDeadline(t *testing.T) {
	rctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	want, _ := rctx.Deadline()
	rctx = Rollback(rctx)

	// A step of the rollback with a shorter deadline of its own, then
	// Ctrl+C: the write still ends at the rollback's deadline only.
	step, stepCancel := context.WithTimeout(rctx, time.Minute)
	stepCancel()

	wctx, wcancel := Context(step)
	require.NoError(t, wctx.Err())
	got, ok := wctx.Deadline()
	require.True(t, ok)
	assert.Equal(t, want, got)

	wcancel()
	assert.ErrorIs(t, wctx.Err(), context.Canceled)
}

func TestContextEndsWithAnExpiredRollback(t *testing.T) {
	rctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	wctx, wcancel := Context(Rollback(rctx))
	defer wcancel()

	assert.ErrorIs(t, wctx.Err(), context.DeadlineExceeded)
}

func TestRollbackWithoutDeadline(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, ctx, Rollback(ctx))

	wctx, wcancel := Context(Rollback(ctx))
	defer wcancel()
	_, ok := wctx.Deadline()
	assert.False(t, ok)
}
