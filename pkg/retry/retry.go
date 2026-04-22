// SPDX-License-Identifier: LGPL-3.0-or-later

// Package retry runs operations with bounded exponential backoff.
package retry

import (
	"context"
	"time"
)

// Do runs op until it returns nil, retryable returns false, the attempt
// budget is exhausted, or ctx is done. Backoff doubles each attempt.
// attempts counts total attempts including the first, so attempts=1 means
// no retry. Callers bound total wait time with context deadlines.
func Do(ctx context.Context, op func() error, retryable func(error) bool,
	attempts int, initial time.Duration) error {

	backoff := initial
	var lastErr error
	for i := 0; i < attempts; i++ {
		err := op()
		if err == nil {
			return nil
		}
		if !retryable(err) {
			return err
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return lastErr
}
