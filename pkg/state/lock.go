// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
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
	// does not know, and with the holder of the daemon lock, again each
	// time that lock changes hands. LockRealm logs at info level instead
	// when it is nil.
	OnWait func(holder *LockHolder)
	// OnWarning, when set, receives the warnings meant for the user: a
	// daemon lock whose holder no longer runs, which LockRealm removes; a
	// holder that LockRealm cannot tell for sure still runs and that keeps
	// the lock for long, with the command that removes the lock; and a lock
	// that the unlock function could not remove. LockRealm reports them through the
	// context (sindlog.Warn) instead when it is nil.
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
	if r.alive == nil {
		r.alive = processAlive
	}
	r.procRoot = cmp.Or(r.procRoot, "/proc")
	r.pollFirst = cmp.Or(r.pollFirst, lockPollFirst)
	r.pollMax = cmp.Or(r.pollMax, lockPollMax)
	r.hintAfter = cmp.Or(r.hintAfter, lockHintAfter)
	return r
}

// LockRealm takes the exclusive lock of realm and returns the function
// that releases it, which the caller must call when its operation ends,
// failed or not.
//
// cluster.Create, cluster.Delete, cluster.DeleteAll, cluster.WorkerAdd,
// cluster.WorkerRemove, cluster.Connect, cluster.Disconnect,
// cluster.PowerOn, cluster.PowerReboot and cluster.PowerCycle change state
// that all clusters of a realm share: the mesh, its DNS records and
// known_hosts, the networks that its external containers join, and a
// cluster's Slurm node list. They take no lock themselves. A caller holds
// the realm lock from before mesh.Manager.EnsureMesh until the call
// returns, as every sind command that changes a realm does; concurrent
// calls in one realm otherwise lose each other's DNS records, host keys
// and node definitions, or remove each other's resources, and a
// cluster.Create can leave a container that cluster.Connect attaches
// meanwhile off its new cluster network, or put one that
// cluster.Disconnect detaches on it.
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
// holds the file lock, it calls opts.OnWait, and when another client holds
// the daemon lock, it calls opts.OnWait with the holder, and again with
// each new holder; then it tries again, at intervals that grow to two
// seconds. Callers take a freed lock in no particular order. A holder that
// ran on this host, in this PID namespace and since this boot, but whose
// PID is free, or taken by a process that started at another time, was
// killed before it could release the lock: LockRealm removes its lock and
// says so through opts.OnWarning. Whether a holder elsewhere still runs it
// cannot tell, nor whether a holder of an earlier sind, which records no
// start time, is the process that has its PID now. It leaves such a lock
// alone, and once that holder has had it for a minute of the wait, names
// the command that removes it, through opts.OnWarning. The waits have no
// timeout; when ctx ends, LockRealm returns ctx.Err().
//
// The returned function removes the daemon lock, without ctx, so that it
// works after Ctrl+C as well, and then releases the file lock. A second
// call does nothing. LockRealm refuses a realm name that config.CheckName
// refuses, also with opts.Dir.
func LockRealm(ctx context.Context, realm string, opts *LockOptions) (unlock func(), err error) {
	if err := config.CheckName("realm", realm); err != nil {
		return nil, err
	}
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
	if err := o.lockFile(ctx, realm, int(f.Fd())); err != nil {
		_ = f.Close()
		return nil, err
	}

	log.DebugContext(ctx, "realm lock acquired", "realm", realm)
	// Closing f, the only descriptor of the lock file's open file
	// description, releases the file lock. A flock(LOCK_UN) of its number
	// would reach, on a second call, the file that has the number since.
	unlockFile := sync.OnceFunc(func() {
		_ = f.Close()
		log.DebugContext(ctx, "realm lock released", "realm", realm)
	})
	if o.Client == nil {
		return unlockFile, nil
	}

	unlockDaemon, err := lockDaemon(ctx, realm, &o)
	if err != nil {
		unlockFile()
		return nil, fmt.Errorf("taking the realm lock on the Docker daemon: %w", err)
	}
	return sync.OnceFunc(func() {
		unlockDaemon()
		unlockFile()
	}), nil
}

// lockFile takes the flock of fd, the open lock file. It tries without
// waiting first, so that a lock nobody holds gives no notice, and while
// another process holds the lock, again at intervals that grow from
// pollFirst to pollMax, until ctx ends. It never blocks in flock(2): a
// blocked flock does not end when its file is closed, so a wait that ctx
// ends would keep a goroutine and its thread until the holder lets go.
func (o *LockOptions) lockFile(ctx context.Context, realm string, fd int) error {
	delay := o.pollFirst
	for waited := false; ; waited = true {
		err := o.flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		if !waited {
			o.waiting(ctx, realm, nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, o.pollMax)
	}
}
