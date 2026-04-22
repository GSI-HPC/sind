// SPDX-License-Identifier: LGPL-3.0-or-later

package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	calls := 0
	err := Do(context.Background(),
		func() error { calls++; return nil },
		func(error) bool { return true },
		3, time.Millisecond,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestDo_RetriesUntilSuccess(t *testing.T) {
	transient := errors.New("transient")
	calls := 0
	err := Do(context.Background(),
		func() error {
			calls++
			if calls < 3 {
				return transient
			}
			return nil
		},
		func(e error) bool { return errors.Is(e, transient) },
		5, time.Millisecond,
	)
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestDo_StopsOnNonRetryableError(t *testing.T) {
	fatal := errors.New("fatal")
	calls := 0
	err := Do(context.Background(),
		func() error { calls++; return fatal },
		func(error) bool { return false },
		5, time.Millisecond,
	)
	require.ErrorIs(t, err, fatal)
	assert.Equal(t, 1, calls)
}

func TestDo_ReturnsLastErrorAfterAttemptBudget(t *testing.T) {
	transient := errors.New("transient")
	calls := 0
	err := Do(context.Background(),
		func() error { calls++; return transient },
		func(error) bool { return true },
		3, time.Millisecond,
	)
	require.ErrorIs(t, err, transient)
	assert.Equal(t, 3, calls)
}

func TestDo_ContextCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := Do(ctx,
		func() error { calls++; return errors.New("transient") },
		func(error) bool { return true },
		10, 100*time.Millisecond,
	)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}
