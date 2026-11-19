// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestLockRealm(t *testing.T) {
	t.Run("creates directory and lock file", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "sind", "test-realm")

		unlock, err := LockRealm(t.Context(), "test-realm", &LockOptions{Dir: dir})
		require.NoError(t, err)
		defer unlock()

		info, err := os.Stat(filepath.Join(dir, "lock"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("contention blocks until released", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		unlock1, err := LockRealm(t.Context(), "contention", &LockOptions{Dir: dir})
		require.NoError(t, err)

		var waited atomic.Int32
		acquired := make(chan struct{})
		go func() {
			unlock2, err := LockRealm(context.Background(), "contention", &LockOptions{
				Dir:    dir,
				OnWait: func(h *LockHolder) { assert.Nil(t, h, "the file lock has no known holder"); waited.Add(1) },
			})
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
		assert.Equal(t, int32(1), waited.Load(), "OnWait is called once before blocking")

		unlock1()

		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("second lock not acquired after first was released")
		}
	})

	t.Run("context cancellation unblocks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		unlock1, err := LockRealm(t.Context(), "cancel", &LockOptions{Dir: dir})
		require.NoError(t, err)
		defer unlock1()

		// Without OnWait, the wait is logged at info level.
		var logs bytes.Buffer
		ctx, cancel := context.WithCancel(sindlog.With(context.Background(), slog.New(slog.NewTextHandler(&logs, nil))))
		done := make(chan error, 1)
		go func() {
			_, err := LockRealm(ctx, "cancel", &LockOptions{Dir: dir})
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
		assert.Contains(t, logs.String(), "waiting for another operation to complete")
	})

	t.Run("different realms do not contend", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()

		unlock1, err := LockRealm(t.Context(), "realm-a", &LockOptions{Dir: filepath.Join(base, "realm-a")})
		require.NoError(t, err)
		defer unlock1()

		unlock2, err := LockRealm(t.Context(), "realm-b", &LockOptions{Dir: filepath.Join(base, "realm-b")})
		require.NoError(t, err)
		defer unlock2()
	})
}

// TestLockRealm_DefaultDir checks that the lock file is in the realm's
// state directory when no Dir is given, as the sind CLI takes it.
func TestLockRealm_DefaultDir(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	unlock, err := LockRealm(t.Context(), "ci-42", nil)
	require.NoError(t, err)
	defer unlock()

	_, err = os.Stat(filepath.Join(stateHome, "sind", "ci-42", "lock"))
	require.NoError(t, err)
}

func TestLockRealm_Errors(t *testing.T) {
	t.Run("no state directory", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		_, err := LockRealm(t.Context(), "ci-42", nil)
		require.ErrorContains(t, err, "resolving state directory")
	})

	t.Run("directory cannot be created", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o600))
		_, err := LockRealm(t.Context(), "ci-42", &LockOptions{Dir: filepath.Join(file, "ci-42")})
		require.ErrorContains(t, err, "creating state directory")
	})

	t.Run("lock file cannot be opened", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "lock"), 0o700))
		_, err := LockRealm(t.Context(), "ci-42", &LockOptions{Dir: dir})
		require.ErrorContains(t, err, "opening lock file")
	})

	t.Run("flock fails", func(t *testing.T) {
		_, err := LockRealm(t.Context(), "ci-42", &LockOptions{
			Dir:   t.TempDir(),
			flock: func(int, int) error { return unix.ENOLCK },
		})
		require.ErrorIs(t, err, unix.ENOLCK)
		assert.ErrorContains(t, err, "acquiring lock")
	})

	t.Run("flock fails while waiting", func(t *testing.T) {
		calls := 0
		_, err := LockRealm(t.Context(), "ci-42", &LockOptions{
			Dir:       t.TempDir(),
			OnWait:    func(*LockHolder) {},
			pollFirst: time.Millisecond,
			flock: func(int, int) error {
				if calls++; calls == 1 {
					return unix.EWOULDBLOCK
				}
				return unix.EINTR
			},
		})
		require.ErrorIs(t, err, unix.EINTR)
		assert.ErrorContains(t, err, "acquiring lock")
	})
}

// TestLockRealm_PollsTheFileLock checks that a wait for the file lock
// tries again at growing intervals and never blocks in flock(2), which
// would not end when ctx does.
func TestLockRealm_PollsTheFileLock(t *testing.T) {
	var calls []time.Time
	waits := 0
	ctx, cancel := context.WithCancel(t.Context())
	_, err := LockRealm(ctx, "ci-42", &LockOptions{
		Dir:       t.TempDir(),
		OnWait:    func(*LockHolder) { waits++ },
		pollFirst: 5 * time.Millisecond,
		pollMax:   20 * time.Millisecond,
		flock: func(_ int, how int) error {
			assert.Equal(t, unix.LOCK_EX|unix.LOCK_NB, how, "never a blocking flock")
			if calls = append(calls, time.Now()); len(calls) == 6 {
				cancel()
			}
			return unix.EWOULDBLOCK
		},
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, waits, "OnWait is called once")
	require.Len(t, calls, 6)
	assert.GreaterOrEqual(t, calls[2].Sub(calls[1]), 10*time.Millisecond, "the interval doubles")
	assert.Less(t, calls[5].Sub(calls[4]), 200*time.Millisecond, "up to pollMax")
}

// TestLockRealm_UnlockTwice checks that a second call of the unlock
// function does not release the lock of a later holder, which likely has
// the same file descriptor number.
func TestLockRealm_UnlockTwice(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(fmt.Sprintf("daemon=%v", daemon), func(t *testing.T) {
			dir := t.TempDir()
			d := newFakeDaemon(t)
			opts := func() *LockOptions {
				o := &LockOptions{Dir: dir, pollFirst: time.Millisecond}
				if daemon {
					o.Client = d.client()
				}
				return o
			}
			unlock1, err := LockRealm(t.Context(), "twice", opts())
			require.NoError(t, err)
			unlock1()
			unlock2, err := LockRealm(t.Context(), "twice", opts())
			require.NoError(t, err)
			defer unlock2()

			unlock1()

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			o := opts()
			o.Client = nil
			waited := false
			o.OnWait = func(*LockHolder) { waited = true }
			_, err = LockRealm(ctx, "twice", o)
			require.ErrorIs(t, err, context.DeadlineExceeded, "the second holder keeps the lock")
			assert.True(t, waited)
			assert.Equal(t, daemon, d.network("twice-lock") != nil, "and its daemon lock")
		})
	}
}
