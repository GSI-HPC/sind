// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/GSI-HPC/sind/pkg/state"
)

// acquireRealmLock takes the realm lock (state.LockRealm) for a command
// that changes the realm. stateHome overrides XDG_STATE_HOME for the lock
// file location; if empty, the realm's state directory is used. The
// function blocks until the lock is acquired or the context is cancelled.
// The returned function releases the lock and must be called when the
// mutating operation completes (typically via defer).
//
// While another sind command holds the lock, a warning on the command's
// stderr (stderrFrom) says why the command waits: the user may have to end
// the other one, and without -v nothing else would show.
func acquireRealmLock(ctx context.Context, realm, stateHome string) (func(), error) {
	opts := state.LockOptions{OnWait: func() {
		_, _ = fmt.Fprintf(stderrFrom(ctx), "Warning: waiting for another sind command in realm %q to finish\n", realm)
	}}
	if stateHome != "" {
		opts.Dir = filepath.Join(stateHome, "sind", realm)
	}
	return state.LockRealm(ctx, realm, &opts)
}
