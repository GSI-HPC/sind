// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"sync"
	"time"
)

// clock is the time a script plays in. A live run plays in real time, sped
// up, so that a create that takes minutes takes seconds in the demo. A
// recorded run plays on a clock that only moves when the script sleeps, so
// that its log is the same on every run.
type clock interface {
	Now() time.Time
	// Sleep lets d of the script's time pass. It returns the error of ctx
	// when ctx ends first, or has ended.
	Sleep(ctx context.Context, d time.Duration) error
}

// liveClock is real time, speed times faster.
type liveClock struct{ speed float64 }

func (liveClock) Now() time.Time { return time.Now() }

func (c liveClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(time.Duration(float64(d) / c.speed))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// manualClock is a clock that moves by sleeping. It may end a context at a
// set time, as an interrupt would.
type manualClock struct {
	mu sync.Mutex
	t  time.Time
	// cancelAt is when cancel is called, once; zero for never.
	cancelAt time.Time
	cancel   context.CancelFunc
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	end := c.t.Add(d)
	cancel := c.cancel
	if cancel != nil && !c.cancelAt.IsZero() && !end.Before(c.cancelAt) {
		c.t, c.cancel = c.cancelAt, nil
		c.mu.Unlock()
		cancel()
		return ctx.Err()
	}
	c.t = end
	c.mu.Unlock()
	return nil
}
