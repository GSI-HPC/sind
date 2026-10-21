// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// inLockedRealm runs op, a change to the realm of --realm, under the realm
// lock, with the realm's mesh manager.
func inLockedRealm(cmd *cobra.Command, op func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	unlock, err := acquireRealmLock(ctx, realm, "")
	if err != nil {
		return err
	}
	defer unlock()
	return op(ctx, client, meshMgrFrom(ctx, client, realm))
}

// updateSSHExport brings the SSH config export of the mesh manager's realm
// up to date after a change to its clusters; a failure is a warning.
func updateSSHExport(cmd *cobra.Command, client *docker.Client, meshMgr *mesh.Manager) {
	if dir, err := state.RealmDir(meshMgr.Realm); err == nil {
		if err := syncSSHExport(cmd.Context(), client, meshMgr, afero.NewOsFs(), dir); err != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", termtext.EscapeLines(err.Error()))
		}
	}
}

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
// nothing else would show. The lock's other warnings go there too, through
// the context (withStderr). What they quote of the holder comes from
// another client and is escaped.
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
	}
	if stateHome != "" {
		opts.Dir = filepath.Join(stateHome, "sind", realm)
	}
	return state.LockRealm(ctx, realm, &opts)
}
