// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/fakedocker"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProc returns a stand-in for /proc with a boot ID, a PID namespace,
// and this process as self, which started at testStart.
func fakeProc(t *testing.T, bootID, pidNS string) string {
	t.Helper()
	root := t.TempDir()
	random := filepath.Join(root, "sys", "kernel", "random")
	require.NoError(t, os.MkdirAll(random, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(random, "boot_id"), []byte(bootID+"\n"), 0o644))
	procStat(t, root, os.Getpid(), testStart)
	ns := filepath.Join(root, strconv.Itoa(os.Getpid()), "ns")
	require.NoError(t, os.MkdirAll(ns, 0o755))
	require.NoError(t, os.Symlink(pidNS, filepath.Join(ns, "pid")))
	require.NoError(t, os.Symlink(strconv.Itoa(os.Getpid()), filepath.Join(root, "self")))
	return root
}

// procStat writes the stat file of process pid into the fake /proc root,
// with the start time start in field 22, and a command name with the
// parentheses and spaces that the parsing must skip.
func procStat(t *testing.T, root string, pid int, start string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	stat := strconv.Itoa(pid) + " (sind (x) 1) S 1 2 2 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 9 0 " + start + " 1000 200 18446744073709551615\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644))
}

const (
	testBootID = "6b1f3a52-0b7a-4d43-9a43-0c2f7f6e3d11"
	testPIDNS  = "pid:[4026531836]"
	// testStart is the start time of this process in the fake /proc,
	// testHolderStart that of the holders of holderLabels.
	testStart       = "618810"
	testHolderStart = "500100"
)

// holderLabels returns the labels of a lock that a process with the given
// host and PID holds, in the fake /proc's boot and PID namespace, which
// started at testHolderStart.
func holderLabels(host string, pid int) docker.Labels {
	return docker.Labels{
		labelRealm:       "sind",
		labelLockToken:   "other-token",
		labelLockCommand: "sind create cluster dev",
		labelLockHost:    host,
		labelLockPID:     strconv.Itoa(pid),
		labelLockBootID:  testBootID,
		labelLockPIDNS:   testPIDNS,
		labelLockStart:   testHolderStart,
	}
}

// warnings collects what LockRealm reports through OnWait and OnWarning.
type warnings struct {
	mu      sync.Mutex
	holders []*LockHolder
	msgs    []string
}

func (w *warnings) onWait(h *LockHolder) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holders = append(w.holders, h)
}

func (w *warnings) onWarning(msg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msg)
}

func (w *warnings) waits() []*LockHolder {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*LockHolder(nil), w.holders...)
}

func (w *warnings) warnings() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.msgs...)
}

// daemonOpts returns options that take the lock on client with fast polls,
// in the fake /proc procRoot, reporting to w.
func daemonOpts(t *testing.T, client *docker.Client, procRoot string, w *warnings) *LockOptions {
	return &LockOptions{
		Dir:       t.TempDir(),
		Client:    client,
		Command:   "sind test",
		OnWait:    w.onWait,
		OnWarning: w.onWarning,
		procRoot:  procRoot,
		pollFirst: time.Millisecond,
		pollMax:   5 * time.Millisecond,
	}
}

func TestLockRealm_DaemonAcquireRelease(t *testing.T) {
	d := fakedocker.New(t)
	host, _ := os.Hostname()
	var w warnings
	opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)

	unlock, err := LockRealm(t.Context(), "sind", opts)
	require.NoError(t, err)

	n := d.Network("sind-lock")
	require.NotNil(t, n, "the lock is the network <realm>-lock")
	assert.NotEmpty(t, n.Labels[labelLockToken])
	delete(n.Labels, labelLockToken)
	assert.Equal(t, docker.Labels{
		labelRealm:       "sind",
		labelLockCommand: "sind test",
		labelLockHost:    host,
		labelLockPID:     strconv.Itoa(os.Getpid()),
		labelLockBootID:  testBootID,
		labelLockPIDNS:   testPIDNS,
		labelLockStart:   testStart,
	}, n.Labels)

	unlock()
	assert.Nil(t, d.Network("sind-lock"), "unlock removes the lock")
	assert.Empty(t, w.waits())
	assert.Empty(t, w.warnings())

	// The file lock is free again too.
	unlock, err = LockRealm(t.Context(), "sind", &LockOptions{Dir: opts.Dir})
	require.NoError(t, err)
	unlock()
}

// TestLockRealm_DaemonCommandDefault checks that the lock records the
// process's command line when the caller names none.
func TestLockRealm_DaemonCommandDefault(t *testing.T) {
	d := fakedocker.New(t)
	unlock, err := LockRealm(t.Context(), "sind", &LockOptions{Dir: t.TempDir(), Client: d.Client()})
	require.NoError(t, err)
	defer unlock()

	assert.Equal(t, commandLine(os.Args), d.Network("sind-lock").Labels[labelLockCommand])
}

// TestLockRealm_DaemonContention checks that a client waits while another
// one, with another state directory, holds the daemon lock, and is told
// who that is.
func TestLockRealm_DaemonContention(t *testing.T) {
	d := fakedocker.New(t)
	client := d.Client()
	proc := fakeProc(t, testBootID, testPIDNS)
	var w1, w2 warnings
	opts1 := daemonOpts(t, client, proc, &w1)
	opts1.Command = "sind create cluster first"

	unlock1, err := LockRealm(t.Context(), "sind", opts1)
	require.NoError(t, err)

	acquired := make(chan struct{})
	go func() {
		unlock2, err := LockRealm(t.Context(), "sind", daemonOpts(t, client, proc, &w2))
		assert.NoError(t, err)
		close(acquired)
		unlock2()
	}()

	select {
	case <-acquired:
		t.Fatal("second client took the daemon lock while the first held it")
	case <-time.After(100 * time.Millisecond):
	}
	waits := w2.waits()
	require.Len(t, waits, 1, "OnWait is called once")
	assert.Equal(t, "sind create cluster first", waits[0].Command)
	assert.Equal(t, os.Getpid(), waits[0].PID)

	unlock1()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second client did not take the daemon lock after the first released it")
	}
	assert.Empty(t, w2.warnings(), "a live holder on this host gets no removal hint")
}

// TestLockRealm_DaemonStale checks that a lock whose holder ran in this
// PID namespace and no longer runs is taken over, with a warning.
func TestLockRealm_DaemonStale(t *testing.T) {
	d := fakedocker.New(t)
	host, _ := os.Hostname()
	d.Add("sind-lock", holderLabels(host, 4242))
	var w warnings
	opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)
	var checked []int
	opts.alive = func(pid int) bool { checked = append(checked, pid); return false }

	unlock, err := LockRealm(t.Context(), "sind", opts)
	require.NoError(t, err)
	defer unlock()

	assert.Equal(t, []int{4242}, checked)
	assert.Empty(t, w.waits(), "a stale lock is no wait")
	require.Len(t, w.warnings(), 1)
	assert.Contains(t, w.warnings()[0], "removing the realm lock of sind create cluster dev (pid 4242 on "+host)
	assert.Contains(t, w.warnings()[0], "which no longer runs")
	assert.Equal(t, strconv.Itoa(os.Getpid()), d.Network("sind-lock").Labels[labelLockPID])
}

// TestLockRealm_DaemonStaleRemovedMeanwhile checks that a stale lock that
// another client removes first is no error.
func TestLockRealm_DaemonStaleRemovedMeanwhile(t *testing.T) {
	d := fakedocker.New(t)
	host, _ := os.Hostname()
	d.Add("sind-lock", holderLabels(host, 4242))
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "rm" && d.Network("sind-lock") != nil && args[2] == d.Network("sind-lock").ID {
			d.Remove("sind-lock")
			return d.Fail("Error response from daemon: network " + args[2] + " not found\n")
		}
		return nil
	})
	var w warnings
	opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)
	opts.alive = func(int) bool { return false }

	unlock, err := LockRealm(t.Context(), "sind", opts)
	require.NoError(t, err)
	unlock()
}

func TestLockRealm_DaemonStaleRemoveFails(t *testing.T) {
	d := fakedocker.New(t)
	host, _ := os.Hostname()
	d.Add("sind-lock", holderLabels(host, 4242))
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "rm" {
			return &mock.Result{Err: fmt.Errorf("connection reset")}
		}
		return nil
	})
	var w warnings
	opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)
	opts.alive = func(int) bool { return false }

	_, err := LockRealm(t.Context(), "sind", opts)
	require.ErrorContains(t, err, "taking the realm lock on the Docker daemon: removing sind-lock: connection reset")
}

// TestLockRealm_DaemonLiveHolderHere checks that a lock held by a process
// that runs on this host is waited for, with no removal hint.
func TestLockRealm_DaemonLiveHolderHere(t *testing.T) {
	d := fakedocker.New(t)
	host, _ := os.Hostname()
	d.Add("sind-lock", holderLabels(host, 4242))
	var w warnings
	proc := fakeProc(t, testBootID, testPIDNS)
	procStat(t, proc, 4242, testHolderStart)
	opts := daemonOpts(t, d.Client(), proc, &w)
	opts.alive = func(int) bool { return true }
	opts.hintAfter = time.Millisecond

	acquired := make(chan struct{})
	go func() {
		unlock, err := LockRealm(t.Context(), "sind", opts)
		assert.NoError(t, err)
		close(acquired)
		unlock()
	}()

	time.Sleep(50 * time.Millisecond)
	select {
	case <-acquired:
		t.Fatal("took a lock whose holder runs")
	default:
	}
	d.Remove("sind-lock")
	<-acquired

	require.Len(t, w.waits(), 1)
	assert.Equal(t, 4242, w.waits()[0].PID)
	assert.Empty(t, w.warnings())
}

// TestLockRealm_DaemonReusedPID checks that a lock whose holder's PID
// another process has taken since, or this one, is taken over: the process
// with the PID started at another time than the holder.
func TestLockRealm_DaemonReusedPID(t *testing.T) {
	host, _ := os.Hostname()
	for name, pid := range map[string]int{"another process": 4242, "this process": os.Getpid()} {
		t.Run(name, func(t *testing.T) {
			d := fakedocker.New(t)
			d.Add("sind-lock", holderLabels(host, pid))
			var w warnings
			proc := fakeProc(t, testBootID, testPIDNS)
			procStat(t, proc, 4242, "999999")
			opts := daemonOpts(t, d.Client(), proc, &w)
			opts.alive = func(int) bool { return true }

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			unlock, err := LockRealm(ctx, "sind", opts)
			require.NoError(t, err)
			defer unlock()

			assert.Empty(t, w.waits(), "a lock whose holder is gone is no wait")
			require.Len(t, w.warnings(), 1)
			assert.Contains(t, w.warnings()[0], fmt.Sprintf("removing the realm lock of sind create cluster dev (pid %d on %s", pid, host))
			assert.Equal(t, strconv.Itoa(os.Getpid()), d.Network("sind-lock").Labels[labelLockPID])
		})
	}
}

// TestLockRealm_DaemonUnsureHolderHere checks that a holder on this host
// whose PID is taken, but which cannot be told from a later process of that
// PID, keeps its lock, and that a client that waits for it long enough is
// told how to remove it.
func TestLockRealm_DaemonUnsureHolderHere(t *testing.T) {
	host, _ := os.Hostname()
	earlier := holderLabels(host, 4242)
	delete(earlier, labelLockStart)
	d := fakedocker.New(t)
	d.Add("sind-lock", earlier)
	var w warnings
	opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)
	opts.alive = func(int) bool { return true }
	opts.hintAfter = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err := LockRealm(ctx, "sind", opts)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotNil(t, d.Network("sind-lock"), "the lock stays")
	require.Len(t, w.warnings(), 1, "the hint is given once")
	assert.Contains(t, w.warnings()[0], "the realm lock is still held by sind create cluster dev (pid 4242 on "+host)
}

func TestHolderRuns(t *testing.T) {
	host, _ := os.Hostname()
	proc := fakeProc(t, testBootID, testPIDNS)
	procStat(t, proc, 4242, testHolderStart)
	me := self(proc)
	// A /proc whose self is another PID gives this process no start time.
	noStart := me
	noStart.start = ""
	holder := func(pid int, start string) *LockHolder {
		h := holderOf(&docker.NetworkMeta{Labels: holderLabels(host, pid)})
		h.start = start
		return h
	}
	alive := func(int) bool { return true }
	dead := func(int) bool { return false }

	for name, tt := range map[string]struct {
		me         process
		h          *LockHolder
		alive      func(int) bool
		runs, sure bool
	}{
		"elsewhere":                     {me, holder(4242, testHolderStart), nil, true, false},
		"same start":                    {me, holder(4242, testHolderStart), alive, true, true},
		"other start":                   {me, holder(4242, "1"), alive, false, true},
		"PID free":                      {me, holder(4242, testHolderStart), dead, false, true},
		"earlier sind":                  {me, holder(4242, ""), alive, true, false},
		"stat hidden":                   {me, holder(4343, testHolderStart), alive, true, false},
		"no start of its own":           {noStart, holder(4242, testHolderStart), alive, true, false},
		"another goroutine of this one": {me, holder(os.Getpid(), testStart), nil, true, true},
		"own PID, other start":          {me, holder(os.Getpid(), "1"), nil, false, true},
		"own PID, earlier sind":         {me, holder(os.Getpid(), ""), nil, false, true},
		"own PID, no start of its own":  {noStart, holder(os.Getpid(), ""), nil, true, false},
	} {
		if name == "elsewhere" {
			tt.h.Host = "ci-runner-7"
		}
		o := &LockOptions{procRoot: proc, alive: tt.alive}
		if tt.alive == nil {
			o.alive = func(int) bool { t.Errorf("%s: checked a PID it need not check", name); return true }
		}
		runs, sure := o.holderRuns(tt.me, tt.h)
		assert.Equal(t, tt.runs, runs, name+": runs")
		assert.Equal(t, tt.sure, sure, name+": sure")
	}
}

// TestLockRealm_DaemonHolderElsewhere checks that a lock held from another
// host is never removed, and that a client that waits for it long enough
// is told how to remove it, once.
func TestLockRealm_DaemonHolderElsewhere(t *testing.T) {
	for name, labels := range map[string]docker.Labels{
		"other host": holderLabels("ci-runner-7", 4242),
		"other boot": func() docker.Labels {
			host, _ := os.Hostname()
			l := holderLabels(host, 4242)
			l[labelLockBootID] = "another-boot"
			return l
		}(),
		"other PID namespace": func() docker.Labels {
			host, _ := os.Hostname()
			l := holderLabels(host, 4242)
			l[labelLockPIDNS] = "pid:[4026532999]"
			return l
		}(),
		"no identity": {labelLockToken: "other-token"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := fakedocker.New(t)
			d.Add("ci-7-lock", labels)
			var w warnings
			opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)
			opts.alive = func(int) bool {
				t.Error("checked a process it cannot see")
				return false
			}
			opts.hintAfter = 20 * time.Millisecond

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			_, err := LockRealm(ctx, "ci-7", opts)

			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.NotNil(t, d.Network("ci-7-lock"), "the lock stays")
			require.Len(t, w.waits(), 1)
			require.Len(t, w.warnings(), 1, "the hint is given once")
			assert.Contains(t, w.warnings()[0], "the realm lock is still held by ")
			assert.True(t, strings.HasSuffix(w.warnings()[0],
				"; if that command no longer runs, remove the lock with: docker network rm ci-7-lock"), w.warnings()[0])

			// The file lock was released.
			unlock, err := LockRealm(t.Context(), "ci-7", &LockOptions{Dir: opts.Dir})
			require.NoError(t, err)
			unlock()
		})
	}
}

// TestLockRealm_DaemonHolderChanges checks that a lock that changes hands
// while a client waits is reported again, with its new holder, and that the
// hint waits a minute for that holder, not for the first.
func TestLockRealm_DaemonHolderChanges(t *testing.T) {
	d := fakedocker.New(t)
	d.Add("ci-7-lock", holderLabels("ci-runner-7", 4242))
	var w warnings
	opts := daemonOpts(t, d.Client(), fakeProc(t, testBootID, testPIDNS), &w)
	opts.hintAfter = 500 * time.Millisecond
	// When the waiter reports the new holder, and when it warns.
	var mu sync.Mutex
	var waited, warned time.Time
	onWait, onWarning := opts.OnWait, opts.OnWarning
	opts.OnWait = func(h *LockHolder) {
		mu.Lock()
		waited = time.Now()
		mu.Unlock()
		onWait(h)
	}
	opts.OnWarning = func(msg string) {
		mu.Lock()
		warned = time.Now()
		mu.Unlock()
		onWarning(msg)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error)
	go func() {
		_, err := LockRealm(ctx, "ci-7", opts)
		done <- err
	}()

	// The lock changes hands well before the first holder's hint is due.
	require.Eventually(t, func() bool { return len(w.waits()) == 1 }, 2*time.Second, time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	d.Add("ci-7-lock", holderLabels("ci-runner-8", 4343))
	require.Eventually(t, func() bool { return len(w.warnings()) > 0 }, 4*time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	waits := w.waits()
	require.Len(t, waits, 2, "OnWait is called again for the new holder")
	assert.Equal(t, "ci-runner-7", waits[0].Host)
	assert.Equal(t, "ci-runner-8", waits[1].Host)
	require.Len(t, w.warnings(), 1)
	assert.Contains(t, w.warnings()[0], "still held by sind create cluster dev (pid 4343 on ci-runner-8,")
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, warned.Sub(waited), opts.hintAfter-50*time.Millisecond,
		"the hint waits for the new holder, not from the first wait")
}

func TestLockRealm_DaemonNotALock(t *testing.T) {
	d := fakedocker.New(t)
	d.Add("sind-lock", docker.Labels{"com.example": "mine"})

	_, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.Client(), t.TempDir(), &warnings{}))

	require.ErrorContains(t, err, "network sind-lock exists but is not a sind realm lock: remove or rename it")
	assert.NotNil(t, d.Network("sind-lock"))
}

// TestLockRealm_DaemonReleasedMeanwhile covers a lock released between the
// failed create and the inspect.
func TestLockRealm_DaemonReleasedMeanwhile(t *testing.T) {
	d := fakedocker.New(t)
	d.Add("sind-lock", holderLabels("elsewhere", 1))
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "inspect" {
			d.Remove("sind-lock")
		}
		return nil
	})
	var w warnings

	unlock, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.Client(), t.TempDir(), &w))
	require.NoError(t, err)
	unlock()
	assert.Empty(t, w.waits())
}

func TestLockRealm_DaemonCreateFails(t *testing.T) {
	d := fakedocker.New(t)
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "create" {
			return d.Fail("failed to connect to the docker API at unix:///var/run/docker.sock\n")
		}
		return nil
	})
	opts := daemonOpts(t, d.Client(), t.TempDir(), &warnings{})

	_, err := LockRealm(t.Context(), "sind", opts)

	require.ErrorContains(t, err, "taking the realm lock on the Docker daemon: creating sind-lock: Docker is not reachable: failed to connect")
	unlock, err := LockRealm(t.Context(), "sind", &LockOptions{Dir: opts.Dir})
	require.NoError(t, err, "the file lock was released")
	unlock()
}

// TestLockRealm_DaemonCreateFailsAfterCreating checks that a lock which
// the daemon created for a create that failed nonetheless is removed.
func TestLockRealm_DaemonCreateFailsAfterCreating(t *testing.T) {
	d := fakedocker.New(t)
	other := d.Add("other-lock", docker.Labels{labelLockToken: "x"})
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "create" {
			labels := docker.Labels{}
			for i := 2; i < len(args)-1; i++ {
				if args[i] == "--label" {
					k, v, _ := strings.Cut(args[i+1], "=")
					labels[k] = v
				}
			}
			d.Add(args[len(args)-1], labels)
			return &mock.Result{Err: fmt.Errorf("unexpected EOF")}
		}
		return nil
	})

	_, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.Client(), t.TempDir(), &warnings{}))

	require.ErrorContains(t, err, "creating sind-lock: unexpected EOF")
	assert.Nil(t, d.Network("sind-lock"), "the lock this attempt created is removed")
	assert.Equal(t, other, d.Network("other-lock").ID)
}

// TestLockRealm_DaemonCreateCancelled checks that a create that ctx ends
// returns the context's error.
func TestLockRealm_DaemonCreateCancelled(t *testing.T) {
	d := fakedocker.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "create" {
			cancel()
			return &mock.Result{Err: fmt.Errorf("signal: killed")}
		}
		return nil
	})

	_, err := LockRealm(ctx, "sind", daemonOpts(t, d.Client(), t.TempDir(), &warnings{}))

	require.ErrorIs(t, err, context.Canceled)
}

func TestLockRealm_DaemonInspectFails(t *testing.T) {
	d := fakedocker.New(t)
	d.Add("sind-lock", holderLabels("elsewhere", 1))
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "inspect" {
			return &mock.Result{Err: fmt.Errorf("connection reset")}
		}
		return nil
	})

	_, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.Client(), t.TempDir(), &warnings{}))

	require.ErrorContains(t, err, "inspecting sind-lock: connection reset")
}

// TestLockRealm_DaemonReleaseFails checks that a lock that unlock cannot
// remove gives a warning with the command that does.
func TestLockRealm_DaemonReleaseFails(t *testing.T) {
	d := fakedocker.New(t)
	var w warnings
	unlock, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.Client(), t.TempDir(), &w))
	require.NoError(t, err)

	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "rm" {
			return &mock.Result{Err: fmt.Errorf("connection reset")}
		}
		return nil
	})
	unlock()

	assert.Equal(t, []string{"releasing the realm lock: connection reset; remove it with: docker network rm sind-lock"}, w.warnings())
}

// TestLockRealm_DaemonReleaseAfterCancel checks that unlock removes the
// lock after ctx ended, as after Ctrl+C.
func TestLockRealm_DaemonReleaseAfterCancel(t *testing.T) {
	d := fakedocker.New(t)
	client := docker.NewClient(&mock.Executor{OnCall: d.OnCall})
	ctx, cancel := context.WithCancel(t.Context())
	unlock, err := LockRealm(ctx, "sind", daemonOpts(t, client, t.TempDir(), &warnings{}))
	require.NoError(t, err)

	cancel()
	unlock()

	assert.Nil(t, d.Network("sind-lock"))
}

// TestLockRealm_DaemonRemovedBeforeRelease checks that a lock removed by
// hand while it was held is released without a warning.
func TestLockRealm_DaemonRemovedBeforeRelease(t *testing.T) {
	d := fakedocker.New(t)
	var w warnings
	unlock, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.Client(), t.TempDir(), &w))
	require.NoError(t, err)

	d.Remove("sind-lock")
	other := d.Add("sind-lock", holderLabels("elsewhere", 1))
	unlock()

	assert.Empty(t, w.warnings())
	assert.Equal(t, other, d.Network("sind-lock").ID, "unlock leaves another holder's lock alone")
}

// TestLockRealm_DaemonLogs checks that without OnWait and OnWarning the
// wait and the warnings go to the log.
func TestLockRealm_DaemonLogs(t *testing.T) {
	d := fakedocker.New(t)
	d.Add("sind-lock", holderLabels("elsewhere", 4242))
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(sindlog.With(t.Context(), slog.New(slog.NewTextHandler(&logs, nil))), 100*time.Millisecond)
	defer cancel()

	_, err := LockRealm(ctx, "sind", &LockOptions{
		Dir:       t.TempDir(),
		Client:    d.Client(),
		pollFirst: time.Millisecond,
		hintAfter: time.Millisecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, logs.String(), `level=INFO msg="waiting for another operation to complete" realm=sind holder="sind create cluster dev (pid 4242 on elsewhere, since `)
	assert.Contains(t, logs.String(), `level=WARN msg="the realm lock is still held by sind create cluster dev`)
}

func TestLockHolder_String(t *testing.T) {
	since := time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC)
	local := since.Local().Format(time.DateTime)

	h := &LockHolder{Command: "sind delete cluster", Host: "box", PID: 7, Since: since}
	assert.Equal(t, "sind delete cluster (pid 7 on box, since "+local+")", h.String())

	h = &LockHolder{Since: since}
	assert.Equal(t, "an unknown command (pid 0 on an unknown host, since "+local+")", h.String())
}

func TestHolderOf(t *testing.T) {
	since := time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC)
	h := holderOf(&docker.NetworkMeta{ID: "net-id", Created: since, Labels: holderLabels("box", 42)})
	assert.Equal(t, &LockHolder{
		Command: "sind create cluster dev", Host: "box", PID: 42, Since: since,
		id: "net-id", token: "other-token", bootID: testBootID, pidNS: testPIDNS, start: testHolderStart,
	}, h)

	h = holderOf(&docker.NetworkMeta{Labels: docker.Labels{labelLockPID: "x"}})
	assert.Zero(t, h.PID, "a PID that is not a number is unknown")
}

func TestSelf(t *testing.T) {
	host, _ := os.Hostname()
	p := self(fakeProc(t, testBootID, testPIDNS))
	assert.Equal(t, process{host: host, bootID: testBootID, pidNS: testPIDNS, pid: os.Getpid(), start: testStart}, p)

	// What cannot be read stays empty.
	p = self(t.TempDir())
	assert.Equal(t, process{host: host, pid: os.Getpid()}, p)

	// A /proc of another PID namespace, whose self is another PID, gives no
	// start time, as its PIDs are not the ones this process signals.
	proc := t.TempDir()
	procStat(t, proc, os.Getpid(), testStart)
	require.NoError(t, os.Symlink("7", filepath.Join(proc, "self")))
	assert.Empty(t, self(proc).start)

	p = self("/proc")
	assert.NotEmpty(t, p.start, "the start time of this process")
	assert.Equal(t, p.start, startTime("/proc", os.Getpid()))
}

func TestStartTime(t *testing.T) {
	proc := t.TempDir()
	procStat(t, proc, 42, "123456")
	assert.Equal(t, "123456", startTime(proc, 42))

	assert.Empty(t, startTime(proc, 43), "no such process")
	for name, stat := range map[string]string{
		"no command name": "42 S 1 2 2 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 9 0 123456\n",
		"too few fields":  "42 (sind) S 1 2 2 0\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(proc, "42", "stat"), []byte(stat), 0o644))
		assert.Empty(t, startTime(proc, 42), name)
	}
}

func TestProcessSees(t *testing.T) {
	me := process{host: "box", bootID: "b", pidNS: "ns", pid: 1}
	same := LockHolder{Host: "box", PID: 9, bootID: "b", pidNS: "ns"}
	assert.True(t, me.sees(&same))

	for name, change := range map[string]func(h *LockHolder){
		"other host":  func(h *LockHolder) { h.Host = "other" },
		"other boot":  func(h *LockHolder) { h.bootID = "c" },
		"other PIDNS": func(h *LockHolder) { h.pidNS = "ns2" },
		"no PID":      func(h *LockHolder) { h.PID = 0 },
	} {
		h := same
		change(&h)
		assert.False(t, me.sees(&h), name)
	}

	// Parts that could not be read match nothing, not even each other.
	unknown := process{host: "box", pid: 1}
	assert.False(t, unknown.sees(&LockHolder{Host: "box", PID: 9}))
	assert.False(t, process{host: "box", bootID: "b", pid: 1}.sees(&LockHolder{Host: "box", PID: 9, bootID: "b"}))
}

func TestProcessAlive(t *testing.T) {
	assert.True(t, processAlive(os.Getpid()))
	// Above the largest pid_max (2^22), so no process has it.
	assert.False(t, processAlive(1<<30))
}

func TestCommandLine(t *testing.T) {
	assert.Empty(t, commandLine(nil))
	assert.Equal(t, "sind create cluster dev", commandLine([]string{"/usr/local/bin/sind", "create", "cluster", "dev"}))
}

// Holder reads who holds the realm's lock on the daemon: nobody before the
// lock is taken and after it is released, and the process that took it
// between, with the command it recorded.
func TestHolder(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	rec.SetOnCall(fakedocker.New(t).OnCall)
	realm := testutil.Realm("it-holder")
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), LockNetworkName(realm)) })

	h, err := Holder(t.Context(), c, realm)
	require.NoError(t, err)
	assert.Nil(t, h)

	unlock, err := LockRealm(t.Context(), realm, &LockOptions{Dir: t.TempDir(), Client: c, Command: "sind create cluster dev"})
	require.NoError(t, err)
	h, err = Holder(t.Context(), c, realm)
	require.NoError(t, err)
	require.NotNil(t, h)
	host, _ := os.Hostname()
	assert.Equal(t, "sind create cluster dev", h.Command)
	assert.Equal(t, host, h.Host)
	assert.Equal(t, os.Getpid(), h.PID)
	assert.False(t, h.Since.IsZero())

	unlock()
	h, err = Holder(t.Context(), c, realm)
	require.NoError(t, err)
	assert.Nil(t, h)
}

// Holder reads the lock of the realm it is asked for, from the labels of
// its network, and fails with docker's error when it cannot inspect it. A
// network of the lock's name that sind did not make is a holder that is
// unknown.
func TestHolder_Inspect(t *testing.T) {
	created := time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC)
	inspect := func(labels string) string {
		return `{"Id":"net-id","Name":"ci-42-lock","Created":"` + created.Format(time.RFC3339Nano) + `","ConfigOnly":true,"Labels":` + labels + `}`
	}
	for _, tc := range []struct {
		name   string
		result mock.Result
		want   *LockHolder
		errMsg string
	}{
		{"held", mock.Result{Stdout: inspect(`{"sind.lock.token":"t","sind.lock.command":"sind delete cluster","sind.lock.host":"box","sind.lock.pid":"7"}`)},
			&LockHolder{Command: "sind delete cluster", Host: "box", PID: 7, Since: created, id: "net-id", token: "t"}, ""},
		{"not a sind lock", mock.Result{Stdout: inspect(`null`)},
			&LockHolder{Since: created, id: "net-id"}, ""},
		{"not held", mock.Result{Stderr: testutil.NoSuchNetwork("ci-42-lock"), Err: testutil.ExitCode1(t)}, nil, ""},
		{"daemon unreachable", mock.Result{Err: errors.New("cannot connect")}, nil, "inspecting ci-42-lock: cannot connect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inspected []string
			c := docker.NewClient(&mock.Executor{OnCall: func(args []string, _ string) mock.Result {
				inspected = args
				return tc.result
			}})

			h, err := Holder(t.Context(), c, "ci-42")

			if tc.errMsg != "" {
				require.EqualError(t, err, tc.errMsg)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, h)
			assert.Equal(t, []string{"network", "inspect", "ci-42-lock", "--format", "{{json .}}"}, inspected)
		})
	}
}

func TestLockNetworkName(t *testing.T) {
	assert.Equal(t, docker.NetworkName("ci-42-lock"), LockNetworkName("ci-42"))
}

// --- Against the daemon (fake daemon in unit mode, dockerd in integration mode) ---

// TestLockRealm_DaemonTwoClients checks that a client with another state
// directory waits while the first holds the realm's lock on the daemon,
// that the lock is a configuration-only network, and that nothing is left
// once both have released it.
func TestLockRealm_DaemonTwoClients(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	rec.SetOnCall(fakedocker.New(t).OnCall)
	realm := testutil.Realm("it-lock")
	name := LockNetworkName(realm)
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), name) })

	unlock1, err := LockRealm(t.Context(), realm, &LockOptions{Dir: t.TempDir(), Client: c, Command: "sind create cluster first"})
	require.NoError(t, err)

	meta, err := c.InspectNetworkMeta(t.Context(), name)
	require.NoError(t, err)
	assert.True(t, meta.ConfigOnly, "the lock takes no address pool and no bridge")
	assert.Equal(t, realm, meta.Labels[labelRealm])

	var w warnings
	opts := &LockOptions{Dir: t.TempDir(), Client: c, OnWait: w.onWait, OnWarning: w.onWarning, pollFirst: 10 * time.Millisecond, pollMax: 50 * time.Millisecond}
	acquired := make(chan func())
	go func() {
		unlock2, err := LockRealm(t.Context(), realm, opts)
		assert.NoError(t, err)
		acquired <- unlock2
	}()

	// The second client reports the holder once it finds the lock taken,
	// which takes a create and an inspect on a daemon that other tests keep
	// busy, then keeps waiting.
	deadline := time.After(10 * time.Second)
	for len(w.waits()) == 0 {
		select {
		case <-acquired:
			t.Fatal("second client took the lock while the first held it")
		case <-deadline:
			t.Fatal("second client did not report the holder it waits for")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-acquired:
		t.Fatal("second client took the lock while the first held it")
	case <-time.After(200 * time.Millisecond):
	}
	waits := w.waits()
	require.Len(t, waits, 1)
	assert.Equal(t, "sind create cluster first", waits[0].Command)
	assert.Equal(t, os.Getpid(), waits[0].PID)
	assert.False(t, waits[0].Since.IsZero())

	unlock1()
	var unlock2 func()
	select {
	case unlock2 = <-acquired:
	case <-time.After(10 * time.Second):
		t.Fatal("second client did not take the lock after the first released it")
	}
	unlock2()
	assert.Empty(t, w.warnings())

	_, err = c.InspectNetworkMeta(t.Context(), name)
	assert.True(t, docker.IsNotFound(err), "the lock is gone: %v", err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// TestLockRealm_DaemonMutualExclusion checks that clients that take the
// lock at the same time hold it one at a time.
func TestLockRealm_DaemonMutualExclusion(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	rec.SetOnCall(fakedocker.New(t).OnCall)
	realm := testutil.Realm("it-lock")
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), LockNetworkName(realm)) })

	const clients, rounds = 4, 3
	var done sync.WaitGroup
	var mu sync.Mutex
	holders := 0
	done.Add(clients)
	for range clients {
		go func() {
			defer done.Done()
			opts := &LockOptions{Dir: t.TempDir(), Client: c, OnWait: func(*LockHolder) {}, pollFirst: 5 * time.Millisecond, pollMax: 20 * time.Millisecond}
			for range rounds {
				unlock, err := LockRealm(t.Context(), realm, opts)
				if !assert.NoError(t, err) {
					return
				}
				mu.Lock()
				holders++
				assert.Equal(t, 1, holders, "two clients hold the lock")
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				holders--
				mu.Unlock()
				unlock()
			}
		}()
	}
	done.Wait()

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// TestLockRealm_DaemonStaleTakeover checks that a lock left behind by a
// killed process of this host is taken over.
func TestLockRealm_DaemonStaleTakeover(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	rec.SetOnCall(fakedocker.New(t).OnCall)
	realm := testutil.Realm("it-lock")
	name := LockNetworkName(realm)
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), name) })

	proc := fakeProc(t, testBootID, testPIDNS)
	killed := self(proc)
	killed.pid = 1 << 30 // no process has it
	_, err := c.CreateConfigOnlyNetwork(t.Context(), name, killed.labels(realm, "killed-token", "sind create cluster killed"))
	require.NoError(t, err)

	var w warnings
	unlock, err := LockRealm(t.Context(), realm, &LockOptions{Dir: t.TempDir(), Client: c, OnWait: w.onWait, OnWarning: w.onWarning, procRoot: proc})
	require.NoError(t, err)

	meta, err := c.InspectNetworkMeta(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), meta.Labels[labelLockPID], "the lock is this process's now")
	unlock()

	assert.Empty(t, w.waits())
	require.Len(t, w.warnings(), 1)
	assert.Contains(t, w.warnings()[0], "removing the realm lock of sind create cluster killed (pid 1073741824 on ")

	t.Logf("docker I/O:\n%s", rec.Dump())
}
