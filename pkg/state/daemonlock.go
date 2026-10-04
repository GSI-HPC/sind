// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
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
	labelLockToken   = "sind.lock.token"
	labelLockCommand = "sind.lock.command"
	labelLockHost    = "sind.lock.host"
	labelLockPID     = "sind.lock.pid"
	labelLockBootID  = "sind.lock.boot-id"
	labelLockPIDNS   = "sind.lock.pid-ns"
)

// Timing of the daemon lock.
const (
	// lockPollFirst and lockPollMax bound the wait between two attempts to
	// take a lock that another client holds; it doubles from the first to
	// the most.
	lockPollFirst = 100 * time.Millisecond
	lockPollMax   = 2 * time.Second
	// lockHintAfter is how long LockRealm waits for a holder whose process
	// it cannot check before it says how to remove the lock.
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
}

// String describes the holder for a message: its command, process and host,
// and since when it holds the lock, in local time.
func (h *LockHolder) String() string {
	command, host := h.Command, h.Host
	if command == "" {
		command = "an unknown command"
	}
	if host == "" {
		host = "an unknown host"
	}
	return fmt.Sprintf("%s (pid %d on %s, since %s)", command, h.PID, host, h.Since.Local().Format(time.DateTime))
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

// process identifies a process among the machines and containers that share
// a Docker daemon: by its host name, the boot ID of its kernel, its PID
// namespace and its PID in that namespace. An empty part is one that could
// not be read; it never matches.
type process struct {
	host   string
	bootID string
	pidNS  string
	pid    int
}

// self returns the identity of the calling process. procRoot is /proc
// outside tests.
func self(procRoot string) process {
	host, _ := os.Hostname()
	bootID, _ := os.ReadFile(filepath.Join(procRoot, "sys", "kernel", "random", "boot_id"))
	pidNS, _ := os.Readlink(filepath.Join(procRoot, "self", "ns", "pid"))
	return process{host: host, bootID: strings.TrimSpace(string(bootID)), pidNS: pidNS, pid: os.Getpid()}
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
	command := o.Command
	if command == "" {
		command = commandLine(os.Args)
	}
	labels := me.labels(realm, token, command)

	delay := o.pollFirst
	var waitStart time.Time
	hinted := false
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
		switch {
		case h == nil:
			// Released between the create and the inspect: try again.
		case h.token == "":
			return nil, fmt.Errorf("network %s exists but is not a sind realm lock: remove or rename it", name)
		case me.sees(h) && !o.alive(h.PID):
			o.warn(ctx, fmt.Sprintf("removing the realm lock of %s, which no longer runs", h))
			if err := client.RemoveNetworkByID(ctx, h.id); err != nil && !docker.IsNotFound(err) {
				return nil, fmt.Errorf("removing %s: %w", name, err)
			}
			continue
		case waitStart.IsZero():
			waitStart = time.Now()
			o.waiting(ctx, realm, h)
		case !hinted && !me.sees(h) && time.Since(waitStart) >= o.hintAfter:
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

// warn reports a warning for the user through OnWarning, or else the log.
func (o *LockOptions) warn(ctx context.Context, msg string) {
	if o.OnWarning != nil {
		o.OnWarning(msg)
		return
	}
	sindlog.From(ctx).WarnContext(ctx, msg)
}
