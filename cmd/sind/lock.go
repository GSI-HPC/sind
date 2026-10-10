// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/state"
)

// acquireRealmLock takes the realm lock (state.LockRealm) for a command
// that changes the realm: the file lock in the state directory and the
// lock on the Docker daemon of the context's client (clientFrom).
// stateHome overrides XDG_STATE_HOME for the lock file location; if empty,
// the realm's state directory is used. The function blocks until the lock
// is acquired or the context is cancelled. The returned function releases
// the lock and must be called when the mutating operation completes
// (typically via defer).
//
// While another sind command holds the lock, a warning on the command's
// stderr (stderrFrom) says why the command waits, naming the holder of the
// daemon lock: the user may have to end the other one, and without -v
// nothing else would show. The lock's other warnings go there too. What
// they quote of the holder comes from another client and is escaped.
//
// The wait is a progress span too, "realm lock", from the first wait to
// the moment the lock is taken or the command gives up, whose message
// names the holder of the daemon lock once it is known.
func acquireRealmLock(ctx context.Context, realm, stateHome string) (unlock func(), err error) {
	stderr := stderrFrom(ctx)
	var wait *progress.Span
	defer func() { wait.End(err) }()
	opts := state.LockOptions{
		Client: clientFrom(ctx),
		OnWait: func(holder *state.LockHolder) {
			msg := fmt.Sprintf("Warning: waiting for another sind command in realm %q to finish", realm)
			var who string
			if holder != nil {
				who = holder.String()
				msg += ": " + termtext.EscapeLines(who)
			}
			_, _ = fmt.Fprintln(stderr, msg)
			// LockRealm waits for the file lock, then for the daemon lock,
			// on the goroutine that called it.
			if wait == nil {
				_, wait = progress.Start(ctx, progress.KindWait, "realm lock", progress.Message(who))
			} else {
				wait.Update(progress.Message(who))
			}
		},
		OnWarning: func(msg string) {
			_, _ = fmt.Fprintln(stderr, "Warning:", termtext.EscapeLines(msg))
		},
	}
	if stateHome != "" {
		opts.Dir = filepath.Join(stateHome, "sind", realm)
	}
	return state.LockRealm(ctx, realm, &opts)
}
