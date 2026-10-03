// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAcquireRealmLock covers the CLI's use of state.LockRealm; the lock
// itself is tested in pkg/state.
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

		var stderr1 bytes.Buffer
		unlock1, err := acquireRealmLock(withStderr(context.Background(), &stderr1), "contention", stateHome)
		require.NoError(t, err)
		assert.Empty(t, stderr1.String(), "a free lock gives no warning")

		var stderr2 bytes.Buffer
		acquired := make(chan struct{})
		go func() {
			unlock2, err := acquireRealmLock(withStderr(context.Background(), &stderr2), "contention", stateHome)
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
		assert.Equal(t, "Warning: waiting for another sind command in realm \"contention\" to finish\n", stderr2.String())
	})
}

func TestStderrFrom(t *testing.T) {
	assert.Equal(t, os.Stderr, stderrFrom(context.Background()))
	var buf bytes.Buffer
	assert.Equal(t, &buf, stderrFrom(withStderr(context.Background(), &buf)))
}
