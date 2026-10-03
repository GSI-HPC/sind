// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"golang.org/x/sys/unix"
)

// LockOptions configures LockRealm.
type LockOptions struct {
	// Dir is the directory that holds the lock file; RealmDir(realm) when
	// empty.
	Dir string
	// OnWait, when set, is called once before LockRealm blocks because
	// another process holds the lock. LockRealm logs at info level instead
	// when it is nil.
	OnWait func()

	// flock replaces unix.Flock in tests.
	flock func(fd int, how int) error
}

// LockRealm takes the exclusive advisory lock of realm and returns the
// function that releases it, which the caller must call when its
// operation ends, failed or not.
//
// cluster.Create, cluster.Delete, cluster.WorkerAdd and cluster.WorkerRemove
// change state that all clusters of a realm share: the mesh, its DNS
// records and known_hosts, and a cluster's Slurm node list. They take no
// lock themselves. A caller holds the realm lock from before
// mesh.Manager.EnsureMesh until the call returns, as every sind command
// that changes a realm does; concurrent calls in one realm otherwise lose
// each other's DNS records, host keys and node definitions, or remove each
// other's resources.
//
// The lock is a flock(2) on the file "lock" in the realm's state
// directory, so it serializes the callers that share that directory: the
// goroutines of one process as much as sind commands. Another user, or a
// process with another XDG_STATE_HOME, takes another lock for the same
// daemon's realm; such clients use separate realms.
//
// LockRealm tries the lock without blocking first. When another process
// holds it, it calls opts.OnWait and blocks until the lock is free or ctx
// ends, in which case it returns ctx.Err().
func LockRealm(ctx context.Context, realm string, opts *LockOptions) (unlock func(), err error) {
	var o LockOptions
	if opts != nil {
		o = *opts
	}
	flock := o.flock
	if flock == nil {
		flock = unix.Flock
	}
	dir := o.Dir
	if dir == "" {
		if dir, err = RealmDir(realm); err != nil {
			return nil, fmt.Errorf("resolving state directory: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}

	log := sindlog.From(ctx)
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	fd := int(f.Fd())

	// Try non-blocking first, so that a lock nobody holds gives no notice.
	if err := flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("acquiring lock: %w", err)
		}

		if o.OnWait != nil {
			o.OnWait()
		} else {
			log.InfoContext(ctx, "waiting for another operation to complete", "realm", realm)
		}

		// Block in a goroutine so we can respect context cancellation.
		done := make(chan error, 1)
		go func() { done <- flock(fd, unix.LOCK_EX) }()

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

	log.DebugContext(ctx, "realm lock acquired", "realm", realm)

	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = f.Close()
		log.DebugContext(ctx, "realm lock released", "realm", realm)
	}, nil
}
