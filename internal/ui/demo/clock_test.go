// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveClock(t *testing.T) {
	c := liveClock{speed: 1000}
	before := time.Now()
	require.NoError(t, c.Sleep(context.Background(), 10*time.Second))
	assert.Less(t, time.Since(before), 5*time.Second, "sped up")
	assert.WithinDuration(t, time.Now(), c.Now(), time.Second)
}

func TestLiveClock_Ended(t *testing.T) {
	c := liveClock{speed: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, c.Sleep(ctx, time.Hour), context.Canceled, "ended before")

	ctx, cancel = context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	assert.ErrorIs(t, c.Sleep(ctx, time.Hour), context.Canceled, "ended while sleeping")
}

func TestManualClock(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c := &manualClock{t: start}
	require.NoError(t, c.Sleep(context.Background(), time.Minute))
	assert.Equal(t, start.Add(time.Minute), c.Now())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.ErrorIs(t, c.Sleep(ctx, time.Minute), context.Canceled)
	assert.Equal(t, start.Add(time.Minute), c.Now(), "a context that ended stops time")
}

func TestManualClock_CancelAt(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &manualClock{t: start, cancelAt: start.Add(10 * time.Second), cancel: cancel}

	require.NoError(t, c.Sleep(ctx, 4*time.Second))
	assert.NoError(t, ctx.Err())
	require.ErrorIs(t, c.Sleep(ctx, 8*time.Second), context.Canceled, "the interrupt comes in the sleep")
	assert.Equal(t, start.Add(10*time.Second), c.Now(), "time stops at the interrupt")

	// Once it came, it does not come again.
	c.mu.Lock()
	assert.Nil(t, c.cancel)
	c.mu.Unlock()
}

func TestManualClock_CancelAtInPause(t *testing.T) {
	// A sleep that cannot be interrupted still lets the interrupt arrive.
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &manualClock{t: start, cancelAt: start.Add(time.Second), cancel: cancel}
	require.NoError(t, c.Sleep(context.WithoutCancel(ctx), 5*time.Second))
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}
