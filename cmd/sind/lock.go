// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// acquireRealmLock acquires an exclusive advisory file lock for the given realm.
// stateHome overrides XDG_STATE_HOME for the lock file location; if empty,
// the default from sindStateDir is used. The function blocks until the lock
// is acquired or the context is cancelled.
// The returned function releases the lock and must be called when the
// mutating operation completes (typically via defer).
func acquireRealmLock(ctx context.Context, realm, stateHome string) (func(), error) {
	var dir string
	if stateHome != "" {
		dir = filepath.Join(stateHome, "sind", realm)
	} else {
		var err error
		dir, err = sindStateDir(realm)
		if err != nil {
			return nil, fmt.Errorf("resolving state directory: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}

	lockPath := filepath.Join(dir, "lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	fd := int(f.Fd())

	// Try non-blocking first to avoid printing the wait message when
	// there is no contention.
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("acquiring lock: %w", err)
		}

		fmt.Fprintln(os.Stderr, "sind: waiting for another operation to complete...")

		// Block in a goroutine so we can respect context cancellation.
		done := make(chan error, 1)
		go func() { done <- unix.Flock(fd, unix.LOCK_EX) }()

		select {
		case err := <-done:
			if err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("acquiring lock: %w", err)
			}
		case <-ctx.Done():
			_ = f.Close() // unblocks the goroutine (Flock returns EBADF)
			return nil, ctx.Err()
		}
	}

	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
