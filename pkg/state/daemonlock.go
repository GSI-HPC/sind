// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"golang.org/x/sys/unix"
)

// Labels of a realm's lock on the Docker daemon. sind.realm is the label of
// every realm resource; the others name the process that holds the lock,
// for the wait notice of other clients, and identify it well enough that a
// client on the same machine can tell that it no longer runs.
const (
	labelRealm       = "sind.realm"
	labelLockToken   = "sind.lock.token" //nolint:gosec // G101: the name of the label, not a token
	labelLockCommand = "sind.lock.command"
	labelLockHost    = "sind.lock.host"
	labelLockPID     = "sind.lock.pid"
	labelLockBootID  = "sind.lock.boot-id"
	labelLockPIDNS   = "sind.lock.pid-ns"
	labelLockStart   = "sind.lock.start"
)

// Timing of the daemon lock.
const (
	// lockPollFirst and lockPollMax bound the wait between two attempts to
	// take a lock that another client holds; it doubles from the first to
	// the most.
	lockPollFirst = 100 * time.Millisecond
	lockPollMax   = 2 * time.Second
	// lockHintAfter is how long LockRealm waits for a holder that it cannot
	// tell for sure still runs before it says how to remove the lock.
	lockHintAfter = time.Minute
	// lockCleanupTimeout bounds each removal of the lock, which runs after
	// the caller's context may have ended.
	lockCleanupTimeout = 30 * time.Second
)

// LockNetworkName returns the name of realm's lock on the Docker daemon,
// <realm>-lock: a configuration-only network that exists while a sind
// command changes the realm.
func LockNetworkName(realm string) docker.NetworkName {
	return docker.NetworkName(realm + "-lock")
}

// LockHolder is the process that holds a realm's lock on the Docker
// daemon, as the lock's labels describe it.
type LockHolder struct {
	// Command is the holder's command line, e.g. "sind create cluster dev".
	Command string
	// Host is the host name of the machine, or container, it runs on.
	Host string
	// PID is its process ID there, 0 when unknown.
	PID int
	// Since is when it took the lock, by the daemon's clock.
	Since time.Time

	id     docker.NetworkID
	token  string
	bootID string
	pidNS  string
	start  string
}

// String describes the holder for a message: its command, process and host,
// and since when it holds the lock, in local time.
func (h *LockHolder) String() string {
	return fmt.Sprintf("%s (pid %d on %s, since %s)",
		cmp.Or(h.Command, "an unknown command"), h.PID,
		cmp.Or(h.Host, "an unknown host"), h.Since.Local().Format(time.DateTime))
}

// holderOf reads the holder of a lock from its network.
func holderOf(meta *docker.NetworkMeta) *LockHolder {
	pid, _ := strconv.Atoi(meta.Labels[labelLockPID])
	return &LockHolder{
		Command: meta.Labels[labelLockCommand],
		Host:    meta.Labels[labelLockHost],
		PID:     pid,
		Since:   meta.Created,
		id:      meta.ID,
		token:   meta.Labels[labelLockToken],
		bootID:  meta.Labels[labelLockBootID],
		pidNS:   meta.Labels[labelLockPIDNS],
		start:   meta.Labels[labelLockStart],
	}
}

// lockHolder returns the holder of the lock name, or nil when nobody holds
// it.
func lockHolder(ctx context.Context, client *docker.Client, name docker.NetworkName) (*LockHolder, error) {
	meta, err := client.InspectNetworkMeta(ctx, name)
	if docker.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return holderOf(meta), nil
}

// Holder returns the process that holds realm's lock on the Docker daemon
// of client, as the lock's labels describe it, or nil, with no error, when
// nobody holds it: it is how a command that does not take the lock, such as
// a display of what a realm is busy with, tells that a sind command is
// changing the realm, and which. It reads the daemon part of LockRealm's
// lock alone, the one every client of the daemon shares; a process that
// waits for the file lock of a state directory holds nothing yet. The
// holder of a network named <realm>-lock that no sind command made has no
// command and no host, which LockHolder.String words as unknown.
func Holder(ctx context.Context, client *docker.Client, realm string) (*LockHolder, error) {
	name := LockNetworkName(realm)
	h, err := lockHolder(ctx, client, name)
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", name, err)
	}
	return h, nil
}

// process identifies a process among the machines and containers that share
// a Docker daemon: by its host name, the boot ID of its kernel, its PID
// namespace, its PID in that namespace and its start time, which tells it
// from a later process that got the same PID. An empty part is one that
// could not be read; it never matches.
type process struct {
	host   string
	bootID string
	pidNS  string
	pid    int
	start  string
}

// self returns the identity of the calling process. procRoot is /proc
// outside tests. It reads the start time only from a procRoot of its own
// PID namespace, whose self is its PID, as only there the start times of
// other PIDs are those of the processes that it can signal.
func self(procRoot string) process {
	host, _ := os.Hostname()
	bootID, _ := os.ReadFile(filepath.Join(procRoot, "sys", "kernel", "random", "boot_id"))
	pidNS, _ := os.Readlink(filepath.Join(procRoot, "self", "ns", "pid"))
	p := process{host: host, bootID: strings.TrimSpace(string(bootID)), pidNS: pidNS, pid: os.Getpid()}
	if link, _ := os.Readlink(filepath.Join(procRoot, "self")); link == strconv.Itoa(p.pid) {
		p.start = startTime(procRoot, p.pid)
	}
	return p
}

// startTime returns the start time of process pid, in clock ticks since
// boot, from field 22 of procRoot/<pid>/stat, or "" when it cannot read it.
func startTime(procRoot string, pid int) string {
	stat, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	// Field 2, the command name, is in parentheses and may hold spaces and
	// parentheses; field 3 follows the last ')'.
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// labels returns the labels of the lock that p takes in realm.
func (p process) labels(realm, token, command string) docker.Labels {
	return docker.Labels{
		labelRealm:       realm,
		labelLockToken:   token,
		labelLockCommand: command,
		labelLockHost:    p.host,
		labelLockPID:     strconv.Itoa(p.pid),
		labelLockBootID:  p.bootID,
		labelLockPIDNS:   p.pidNS,
		labelLockStart:   p.start,
	}
}

// sees reports whether p can check that h still runs: h runs in p's PID
// namespace, on p's host and kernel since its last boot, so h.PID names
// the same process for both. A namespace that has ended can pass its inode
// number on to a new one; h's process has ended with it then, and if the
// PID is taken in the new namespace, h only counts as running.
func (p process) sees(h *LockHolder) bool {
	return h.PID > 0 && h.Host == p.host &&
		p.bootID != "" && h.bootID == p.bootID &&
		p.pidNS != "" && h.pidNS == p.pidNS
}

// holderRuns reports whether the holder h of a lock still runs, as far as
// me can tell, and whether it can tell for sure. A holder that me cannot
// see runs. One that it sees is gone when its PID is free, or taken by a
// process that started at another time, such as me: me does not hold the
// lock it tries to take, but another goroutine of its process may, which
// started when me did. A holder without a start time, of an earlier sind,
// runs while its PID is taken, but not for sure, as the PID may have been
// given to another process.
func (o *LockOptions) holderRuns(me process, h *LockHolder) (runs, sure bool) {
	switch {
	case !me.sees(h):
		return true, false
	case h.PID == me.pid:
		return me.start == "" || h.start == me.start, me.start != ""
	case !o.alive(h.PID):
		return false, true
	case me.start == "" || h.start == "":
		return true, false
	}
	start := startTime(o.procRoot, h.PID)
	if start == "" {
		// It ended meanwhile, or its /proc entry is hidden (hidepid).
		return true, false
	}
	return start == h.start, true
}

// processAlive reports whether a process with the PID runs. EPERM means
// that it runs as another user.
func processAlive(pid int) bool {
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

// commandLine joins a program's arguments into the command line the lock
// records, with the program's base name.
func commandLine(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.Join(append([]string{filepath.Base(args[0])}, args[1:]...), " ")
}

// lockDaemon takes realm's lock on the Docker daemon of o.Client and returns
// the function that releases it (see LockRealm).
//
// The lock is the network LockNetworkName(realm): the daemon refuses to
// create a network whose name another network has, and checks and creates
// under one lock per name, so of the clients that create it at once one
// succeeds. A configuration-only network takes no address pool and no
// bridge device, and needs no image.
func lockDaemon(ctx context.Context, realm string, o *LockOptions) (func(), error) {
	client := o.Client
	name := LockNetworkName(realm)
	me := self(o.procRoot)
	token := rand.Text()
	labels := me.labels(realm, token, cmp.Or(o.Command, commandLine(os.Args)))

	delay := o.pollFirst
	// The wait notice and the hint are about one holder: the lock can
	// change hands while this client waits, as clients take a freed lock in
	// no particular order.
	var (
		waitFor   docker.NetworkID
		waitStart time.Time
		hinted    bool
	)
	for {
		id, err := client.CreateConfigOnlyNetwork(ctx, name, labels)
		if err == nil {
			sindlog.From(ctx).DebugContext(ctx, "daemon realm lock acquired", "realm", realm, "lock", name)
			return func() { o.removeDaemonLock(ctx, name, id) }, nil
		}
		if !docker.IsAlreadyExists(err) {
			// docker may have failed, or ctx ended, after the daemon
			// created the network.
			o.removeOwnDaemonLock(ctx, name, token)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("creating %s: %w", name, err)
		}

		h, err := lockHolder(ctx, client, name)
		if err != nil {
			return nil, fmt.Errorf("inspecting %s: %w", name, err)
		}
		runs, sure := true, false
		if h != nil && h.token != "" {
			runs, sure = o.holderRuns(me, h)
		}
		switch {
		case h == nil:
			// Released between the create and the inspect: try again.
		case h.token == "":
			return nil, fmt.Errorf("network %s exists but is not a sind realm lock: remove or rename it", name)
		case !runs:
			o.warn(ctx, fmt.Sprintf("removing the realm lock of %s, which no longer runs", h))
			if err := docker.IgnoreNotFound(client.RemoveNetworkByID(ctx, h.id)); err != nil {
				return nil, fmt.Errorf("removing %s: %w", name, err)
			}
			continue
		case h.id != waitFor:
			waitFor, waitStart, hinted = h.id, time.Now(), false
			o.waiting(ctx, realm, h)
		case !hinted && !sure && time.Since(waitStart) >= o.hintAfter:
			hinted = true
			o.warn(ctx, fmt.Sprintf("the realm lock is still held by %s; if that command no longer runs, remove the lock with: docker network rm %s", h, name))
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(2*delay, o.pollMax)
	}
}

// removeDaemonLock releases a daemon lock that this process holds, by its
// ID, so that it never removes another holder's lock, even when its own
// has been removed by hand. The caller's context may have ended, as after
// Ctrl+C, so the removal runs without it, for at most lockCleanupTimeout.
func (o *LockOptions) removeDaemonLock(ctx context.Context, name docker.NetworkName, id docker.NetworkID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockCleanupTimeout)
	defer cancel()
	log := sindlog.From(ctx)
	err := o.Client.RemoveNetworkByID(ctx, id)
	switch {
	case err == nil:
		log.DebugContext(ctx, "daemon realm lock released", "lock", name)
	case docker.IsNotFound(err):
		log.DebugContext(ctx, "daemon realm lock was removed before its release", "lock", name)
	default:
		o.warn(ctx, fmt.Sprintf("releasing the realm lock: %v; remove it with: docker network rm %s", err, name))
	}
}

// removeOwnDaemonLock removes the lock name if this attempt created it,
// which its token tells. A create that failed, because docker failed or
// ctx ended while docker waited for the daemon, may have created it.
func (o *LockOptions) removeOwnDaemonLock(ctx context.Context, name docker.NetworkName, token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockCleanupTimeout)
	defer cancel()
	if h, err := lockHolder(ctx, o.Client, name); err == nil && h != nil && h.token == token {
		_ = o.Client.RemoveNetworkByID(ctx, h.id)
	}
}

// waiting reports that LockRealm waits for holder, nil for another process
// that holds the file lock, through OnWait or else the log.
func (o *LockOptions) waiting(ctx context.Context, realm string, holder *LockHolder) {
	if o.OnWait != nil {
		o.OnWait(holder)
		return
	}
	args := []any{"realm", realm}
	if holder != nil {
		args = append(args, "holder", holder.String())
	}
	sindlog.From(ctx).InfoContext(ctx, "waiting for another operation to complete", args...)
}

// warn reports a warning for the user through OnWarning, or else through
// the context (sindlog.Warn).
func (o *LockOptions) warn(ctx context.Context, msg string) {
	if o.OnWarning != nil {
		o.OnWarning(msg)
		return
	}
	sindlog.Warn(ctx, msg)
}
