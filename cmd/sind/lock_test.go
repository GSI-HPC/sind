// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireRealmLock(t *testing.T) {
	t.Parallel()

	t.Run("creates directory and lock file", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()

		unlock, err := acquireRealmLock(context.Background(), "test-realm", stateHome)
		require.NoError(t, err)
		defer unlock()

		lockPath := filepath.Join(stateHome, "sind", "test-realm", "lock")
		_, err = os.Stat(lockPath)
		assert.NoError(t, err)
	})

	t.Run("contention blocks until released", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()

		unlock1, err := acquireRealmLock(context.Background(), "contention", stateHome)
		require.NoError(t, err)

		acquired := make(chan struct{})
		go func() {
			unlock2, err := acquireRealmLock(context.Background(), "contention", stateHome)
			assert.NoError(t, err)
			close(acquired)
			unlock2()
		}()

		// Second lock should not be acquired while first is held.
		select {
		case <-acquired:
			t.Fatal("second lock acquired while first is held")
		case <-time.After(100 * time.Millisecond):
		}

		unlock1()

		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("second lock not acquired after first was released")
		}
	})

	t.Run("context cancellation unblocks", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()

		unlock1, err := acquireRealmLock(context.Background(), "cancel", stateHome)
		require.NoError(t, err)
		defer unlock1()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := acquireRealmLock(ctx, "cancel", stateHome)
			done <- err
		}()

		// Give the goroutine time to start blocking.
		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("context cancellation did not unblock lock acquisition")
		}
	})

	t.Run("different realms do not contend", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()

		unlock1, err := acquireRealmLock(context.Background(), "realm-a", stateHome)
		require.NoError(t, err)
		defer unlock1()

		unlock2, err := acquireRealmLock(context.Background(), "realm-b", stateHome)
		require.NoError(t, err)
		defer unlock2()
	})
}
