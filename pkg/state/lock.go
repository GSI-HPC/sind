// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"golang.org/x/sys/unix"
)

// LockOptions configures LockRealm.
type LockOptions struct {
	// Dir is the directory that holds the lock file; RealmDir(realm) when
	// empty.
	Dir string
	// Client, when set, is the Docker daemon on which LockRealm also takes
	// the realm's lock, after the file lock. Every caller that changes a
	// realm sets it: the realm lives on the daemon, which clients with
	// other state directories share.
	Client *docker.Client
	// Command is the command line that the daemon lock records for the
	// wait notices of other clients; the process's arguments when empty.
	Command string
	// OnWait, when set, is called once before LockRealm waits for each of
	// the two locks: with a nil holder for the file lock, whose holder it
	// does not know, and with the holder of the daemon lock. LockRealm logs
	// at info level instead when it is nil.
	OnWait func(holder *LockHolder)
	// OnWarning, when set, receives the warnings meant for the user: a
	// daemon lock whose holder no longer runs, which LockRealm removes; a
	// holder that LockRealm cannot check and that keeps the lock for long,
	// with the command that removes the lock; and a lock that the unlock
	// function could not remove. LockRealm logs them at warn level instead
	// when it is nil.
	OnWarning func(msg string)

	// Test hooks; the zero value selects the default.
	flock     func(fd int, how int) error // unix.Flock
	procRoot  string                      // "/proc"
	alive     func(pid int) bool          // processAlive
	pollFirst time.Duration               // lockPollFirst
	pollMax   time.Duration               // lockPollMax
	hintAfter time.Duration               // lockHintAfter
}

// withDefaults returns a copy of o, or of the zero options when o is nil,
// with the defaults of the test hooks that are not set.
func (o *LockOptions) withDefaults() LockOptions {
	var r LockOptions
	if o != nil {
		r = *o
	}
	if r.flock == nil {
		r.flock = unix.Flock
	}
	if r.procRoot == "" {
		r.procRoot = "/proc"
	}
	if r.alive == nil {
		r.alive = processAlive
	}
	if r.pollFirst == 0 {
		r.pollFirst = lockPollFirst
	}
	if r.pollMax == 0 {
		r.pollMax = lockPollMax
	}
	if r.hintAfter == 0 {
		r.hintAfter = lockHintAfter
	}
	return r
}

// LockRealm takes the exclusive lock of realm and returns the function
// that releases it, which the caller must call when its operation ends,
// failed or not.
//
// cluster.Create, cluster.Delete, cluster.DeleteAll, cluster.WorkerAdd,
// cluster.WorkerRemove, cluster.PowerOn, cluster.PowerReboot and
// cluster.PowerCycle change state that all clusters of a realm share: the mesh, its DNS
// records and known_hosts, and a cluster's Slurm node list. They take no
// lock themselves. A caller holds the realm lock from before
// mesh.Manager.EnsureMesh until the call returns, as every sind command
// that changes a realm does; concurrent calls in one realm otherwise lose
// each other's DNS records, host keys and node definitions, or remove each
// other's resources.
//
// The lock has two parts. The first is a flock(2) on the file "lock" in
// the realm's state directory. It serializes the callers that share that
// directory, the goroutines of one process as much as sind commands,
// without a call to the daemon. With opts.Client, LockRealm then takes the
// realm's lock on that Docker daemon, the configuration-only network
// LockNetworkName(realm), which serializes all clients of the daemon:
// other users, CI jobs that share the host's Docker socket, processes with
// another XDG_STATE_HOME. Without opts.Client, such clients are not
// serialized against each other.
//
// LockRealm tries each part without waiting first. When another process
// holds the file lock, it calls opts.OnWait and blocks until the lock is
// free. When another client holds the daemon lock, it calls opts.OnWait
// with the holder and tries again, at intervals that grow to two seconds;
// clients take a freed lock in no particular order. A holder that runs on
// this host, in this PID namespace and since this boot, but no longer
// exists, was killed before it could release the lock: LockRealm removes
// its lock and says so through opts.OnWarning. Whether a holder elsewhere
// still runs it cannot tell, so it leaves that lock alone, and after a
// minute names the command that removes it, through opts.OnWarning. The
// waits have no timeout; when ctx ends, LockRealm returns ctx.Err().
//
// The returned function removes the daemon lock, without ctx, so that it
// works after Ctrl+C as well, and then releases the file lock.
func LockRealm(ctx context.Context, realm string, opts *LockOptions) (unlock func(), err error) {
	o := opts.withDefaults()
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
	if err := o.flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("acquiring lock: %w", err)
		}

		o.waiting(ctx, realm, nil)

		// Block in a goroutine so we can respect context cancellation.
		done := make(chan error, 1)
		go func() { done <- o.flock(fd, unix.LOCK_EX) }()

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
	unlockFile := func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = f.Close()
		log.DebugContext(ctx, "realm lock released", "realm", realm)
	}
	if o.Client == nil {
		return unlockFile, nil
	}

	unlockDaemon, err := lockDaemon(ctx, realm, &o)
	if err != nil {
		unlockFile()
		return nil, fmt.Errorf("taking the realm lock on the Docker daemon: %w", err)
	}
	return func() {
		unlockDaemon()
		unlockFile()
	}, nil
}
