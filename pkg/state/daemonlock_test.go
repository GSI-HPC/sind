// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemon stands in for dockerd in the daemon lock's unit tests: it
// keeps networks by name and answers the lock's network commands, through
// mock.Executor.OnCall, as dockerd and the docker CLI do.
type fakeDaemon struct {
	exit1 *exec.ExitError

	mu   sync.Mutex
	nets map[string]*fakeNetwork
	ids  int

	// hook, when set, sees each call first; a result it returns answers
	// the call instead of the daemon. mock.Executor holds its lock while it
	// runs, so a hook must not run docker commands.
	hook func(args []string) *mock.Result
}

type fakeNetwork struct {
	id      string
	labels  docker.Labels
	created time.Time
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	return &fakeDaemon{exit1: testutil.ExitCode1(t), nets: map[string]*fakeNetwork{}}
}

// client returns a docker client of the fake daemon.
func (d *fakeDaemon) client() *docker.Client {
	return docker.NewClient(&mock.Executor{OnCall: d.onCall})
}

// fail returns the result of a docker command that exits 1 with stderr.
func (d *fakeDaemon) fail(stderr string) *mock.Result {
	return &mock.Result{Stderr: stderr, Err: d.exit1}
}

// add creates a network, as another client would, and returns its ID.
func (d *fakeDaemon) add(name string, labels docker.Labels) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ids++
	id := fmt.Sprintf("%064x", d.ids)
	d.nets[name] = &fakeNetwork{id: id, labels: labels, created: time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC)}
	return id
}

// remove removes the network name, as another client would.
func (d *fakeDaemon) remove(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.nets, name)
}

// network returns the network name, or nil.
func (d *fakeDaemon) network(name string) *fakeNetwork {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nets[name]
}

func (d *fakeDaemon) onCall(args []string, _ string) mock.Result {
	if d.hook != nil {
		if r := d.hook(args); r != nil {
			return *r
		}
	}
	if len(args) < 3 || args[0] != "network" {
		return mock.Result{Err: fmt.Errorf("fake daemon: unexpected call %v", args)}
	}
	switch args[1] {
	case "create":
		name := args[len(args)-1]
		labels := docker.Labels{}
		for i := 2; i < len(args)-1; i++ {
			if args[i] == "--label" {
				k, v, _ := strings.Cut(args[i+1], "=")
				labels[k] = v
				i++
			}
		}
		if d.network(name) != nil {
			return *d.fail("Error response from daemon: network with name " + name + " already exists\n")
		}
		return mock.Result{Stdout: d.add(name, labels) + "\n"}
	case "inspect":
		n := d.network(args[2])
		if n == nil {
			return *d.fail(testutil.NoSuchNetwork(args[2]))
		}
		out, _ := json.Marshal(map[string]any{
			"Name": args[2], "Id": n.id, "Created": n.created, "ConfigOnly": true, "Labels": n.labels,
		})
		return mock.Result{Stdout: string(out) + "\n"}
	case "rm":
		d.mu.Lock()
		defer d.mu.Unlock()
		for name, n := range d.nets {
			if n.id == args[2] {
				delete(d.nets, name)
				return mock.Result{Stdout: args[2] + "\n"}
			}
		}
		return *d.fail("Error response from daemon: network " + args[2] + " not found\n")
	}
	return mock.Result{Err: fmt.Errorf("fake daemon: unexpected call %v", args)}
}

// fakeProc returns a stand-in for /proc with a boot ID and a PID namespace.
func fakeProc(t *testing.T, bootID, pidNS string) string {
	t.Helper()
	root := t.TempDir()
	random := filepath.Join(root, "sys", "kernel", "random")
	require.NoError(t, os.MkdirAll(random, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(random, "boot_id"), []byte(bootID+"\n"), 0o644))
	ns := filepath.Join(root, "self", "ns")
	require.NoError(t, os.MkdirAll(ns, 0o755))
	require.NoError(t, os.Symlink(pidNS, filepath.Join(ns, "pid")))
	return root
}

const (
	testBootID = "6b1f3a52-0b7a-4d43-9a43-0c2f7f6e3d11"
	testPIDNS  = "pid:[4026531836]"
)

// holderLabels returns the labels of a lock that a process with the given
// host and PID holds, in the fake /proc's boot and PID namespace.
func holderLabels(host string, pid int) docker.Labels {
	return docker.Labels{
		labelRealm:       "sind",
		labelLockToken:   "other-token",
		labelLockCommand: "sind create cluster dev",
		labelLockHost:    host,
		labelLockPID:     strconv.Itoa(pid),
		labelLockBootID:  testBootID,
		labelLockPIDNS:   testPIDNS,
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
	d := newFakeDaemon(t)
	host, _ := os.Hostname()
	var w warnings
	opts := daemonOpts(t, d.client(), fakeProc(t, testBootID, testPIDNS), &w)

	unlock, err := LockRealm(t.Context(), "sind", opts)
	require.NoError(t, err)

	n := d.network("sind-lock")
	require.NotNil(t, n, "the lock is the network <realm>-lock")
	assert.NotEmpty(t, n.labels[labelLockToken])
	delete(n.labels, labelLockToken)
	assert.Equal(t, docker.Labels{
		labelRealm:       "sind",
		labelLockCommand: "sind test",
		labelLockHost:    host,
		labelLockPID:     strconv.Itoa(os.Getpid()),
		labelLockBootID:  testBootID,
		labelLockPIDNS:   testPIDNS,
	}, n.labels)

	unlock()
	assert.Nil(t, d.network("sind-lock"), "unlock removes the lock")
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
	d := newFakeDaemon(t)
	unlock, err := LockRealm(t.Context(), "sind", &LockOptions{Dir: t.TempDir(), Client: d.client()})
	require.NoError(t, err)
	defer unlock()

	assert.Equal(t, commandLine(os.Args), d.network("sind-lock").labels[labelLockCommand])
}

// TestLockRealm_DaemonContention checks that a client waits while another
// one, with another state directory, holds the daemon lock, and is told
// who that is.
func TestLockRealm_DaemonContention(t *testing.T) {
	d := newFakeDaemon(t)
	client := d.client()
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
	d := newFakeDaemon(t)
	host, _ := os.Hostname()
	d.add("sind-lock", holderLabels(host, 4242))
	var w warnings
	opts := daemonOpts(t, d.client(), fakeProc(t, testBootID, testPIDNS), &w)
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
	assert.Equal(t, strconv.Itoa(os.Getpid()), d.network("sind-lock").labels[labelLockPID])
}

// TestLockRealm_DaemonStaleRemovedMeanwhile checks that a stale lock that
// another client removes first is no error.
func TestLockRealm_DaemonStaleRemovedMeanwhile(t *testing.T) {
	d := newFakeDaemon(t)
	host, _ := os.Hostname()
	d.add("sind-lock", holderLabels(host, 4242))
	d.hook = func(args []string) *mock.Result {
		if args[1] == "rm" && d.network("sind-lock") != nil && args[2] == d.network("sind-lock").id {
			d.remove("sind-lock")
			return d.fail("Error response from daemon: network " + args[2] + " not found\n")
		}
		return nil
	}
	var w warnings
	opts := daemonOpts(t, d.client(), fakeProc(t, testBootID, testPIDNS), &w)
	opts.alive = func(int) bool { return false }

	unlock, err := LockRealm(t.Context(), "sind", opts)
	require.NoError(t, err)
	unlock()
}

func TestLockRealm_DaemonStaleRemoveFails(t *testing.T) {
	d := newFakeDaemon(t)
	host, _ := os.Hostname()
	d.add("sind-lock", holderLabels(host, 4242))
	d.hook = func(args []string) *mock.Result {
		if args[1] == "rm" {
			return &mock.Result{Err: fmt.Errorf("connection reset")}
		}
		return nil
	}
	var w warnings
	opts := daemonOpts(t, d.client(), fakeProc(t, testBootID, testPIDNS), &w)
	opts.alive = func(int) bool { return false }

	_, err := LockRealm(t.Context(), "sind", opts)
	require.ErrorContains(t, err, "taking the realm lock on the Docker daemon: removing sind-lock: connection reset")
}

// TestLockRealm_DaemonLiveHolderHere checks that a lock held by a process
// that runs on this host is waited for, with no removal hint.
func TestLockRealm_DaemonLiveHolderHere(t *testing.T) {
	d := newFakeDaemon(t)
	host, _ := os.Hostname()
	d.add("sind-lock", holderLabels(host, 4242))
	var w warnings
	opts := daemonOpts(t, d.client(), fakeProc(t, testBootID, testPIDNS), &w)
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
	d.remove("sind-lock")
	<-acquired

	require.Len(t, w.waits(), 1)
	assert.Equal(t, 4242, w.waits()[0].PID)
	assert.Empty(t, w.warnings())
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
			d := newFakeDaemon(t)
			d.add("ci-7-lock", labels)
			var w warnings
			opts := daemonOpts(t, d.client(), fakeProc(t, testBootID, testPIDNS), &w)
			opts.alive = func(int) bool {
				t.Error("checked a process it cannot see")
				return false
			}
			opts.hintAfter = 20 * time.Millisecond

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			_, err := LockRealm(ctx, "ci-7", opts)

			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.NotNil(t, d.network("ci-7-lock"), "the lock stays")
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

func TestLockRealm_DaemonNotALock(t *testing.T) {
	d := newFakeDaemon(t)
	d.add("sind-lock", docker.Labels{"com.example": "mine"})

	_, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.client(), t.TempDir(), &warnings{}))

	require.ErrorContains(t, err, "network sind-lock exists but is not a sind realm lock: remove or rename it")
	assert.NotNil(t, d.network("sind-lock"))
}

// TestLockRealm_DaemonReleasedMeanwhile covers a lock released between the
// failed create and the inspect.
func TestLockRealm_DaemonReleasedMeanwhile(t *testing.T) {
	d := newFakeDaemon(t)
	d.add("sind-lock", holderLabels("elsewhere", 1))
	d.hook = func(args []string) *mock.Result {
		if args[1] == "inspect" {
			d.remove("sind-lock")
		}
		return nil
	}
	var w warnings

	unlock, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.client(), t.TempDir(), &w))
	require.NoError(t, err)
	unlock()
	assert.Empty(t, w.waits())
}

func TestLockRealm_DaemonCreateFails(t *testing.T) {
	d := newFakeDaemon(t)
	d.hook = func(args []string) *mock.Result {
		if args[1] == "create" {
			return d.fail("failed to connect to the docker API at unix:///var/run/docker.sock\n")
		}
		return nil
	}
	opts := daemonOpts(t, d.client(), t.TempDir(), &warnings{})

	_, err := LockRealm(t.Context(), "sind", opts)

	require.ErrorContains(t, err, "taking the realm lock on the Docker daemon: creating sind-lock: exit status 1: failed to connect")
	unlock, err := LockRealm(t.Context(), "sind", &LockOptions{Dir: opts.Dir})
	require.NoError(t, err, "the file lock was released")
	unlock()
}

// TestLockRealm_DaemonCreateFailsAfterCreating checks that a lock which
// the daemon created for a create that failed nonetheless is removed.
func TestLockRealm_DaemonCreateFailsAfterCreating(t *testing.T) {
	d := newFakeDaemon(t)
	other := d.add("other-lock", docker.Labels{labelLockToken: "x"})
	d.hook = func(args []string) *mock.Result {
		if args[1] == "create" {
			labels := docker.Labels{}
			for i := 2; i < len(args)-1; i++ {
				if args[i] == "--label" {
					k, v, _ := strings.Cut(args[i+1], "=")
					labels[k] = v
				}
			}
			d.add(args[len(args)-1], labels)
			return &mock.Result{Err: fmt.Errorf("unexpected EOF")}
		}
		return nil
	}

	_, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.client(), t.TempDir(), &warnings{}))

	require.ErrorContains(t, err, "creating sind-lock: unexpected EOF")
	assert.Nil(t, d.network("sind-lock"), "the lock this attempt created is removed")
	assert.Equal(t, other, d.network("other-lock").id)
}

// TestLockRealm_DaemonCreateCancelled checks that a create that ctx ends
// returns the context's error.
func TestLockRealm_DaemonCreateCancelled(t *testing.T) {
	d := newFakeDaemon(t)
	ctx, cancel := context.WithCancel(t.Context())
	d.hook = func(args []string) *mock.Result {
		if args[1] == "create" {
			cancel()
			return &mock.Result{Err: fmt.Errorf("signal: killed")}
		}
		return nil
	}

	_, err := LockRealm(ctx, "sind", daemonOpts(t, d.client(), t.TempDir(), &warnings{}))

	require.ErrorIs(t, err, context.Canceled)
}

func TestLockRealm_DaemonInspectFails(t *testing.T) {
	d := newFakeDaemon(t)
	d.add("sind-lock", holderLabels("elsewhere", 1))
	d.hook = func(args []string) *mock.Result {
		if args[1] == "inspect" {
			return &mock.Result{Err: fmt.Errorf("connection reset")}
		}
		return nil
	}

	_, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.client(), t.TempDir(), &warnings{}))

	require.ErrorContains(t, err, "inspecting sind-lock: connection reset")
}

// TestLockRealm_DaemonReleaseFails checks that a lock that unlock cannot
// remove gives a warning with the command that does.
func TestLockRealm_DaemonReleaseFails(t *testing.T) {
	d := newFakeDaemon(t)
	var w warnings
	unlock, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.client(), t.TempDir(), &w))
	require.NoError(t, err)

	d.hook = func(args []string) *mock.Result {
		if args[1] == "rm" {
			return &mock.Result{Err: fmt.Errorf("connection reset")}
		}
		return nil
	}
	unlock()

	assert.Equal(t, []string{"releasing the realm lock: connection reset; remove it with: docker network rm sind-lock"}, w.warnings())
}

// TestLockRealm_DaemonReleaseAfterCancel checks that unlock removes the
// lock after ctx ended, as after Ctrl+C.
func TestLockRealm_DaemonReleaseAfterCancel(t *testing.T) {
	d := newFakeDaemon(t)
	client := docker.NewClient(&ctxExecutor{Executor: &mock.Executor{OnCall: d.onCall}})
	ctx, cancel := context.WithCancel(t.Context())
	unlock, err := LockRealm(ctx, "sind", daemonOpts(t, client, t.TempDir(), &warnings{}))
	require.NoError(t, err)

	cancel()
	unlock()

	assert.Nil(t, d.network("sind-lock"))
}

// ctxExecutor fails every command run with an ended context, as the real
// executor does when it kills docker.
type ctxExecutor struct {
	*mock.Executor
}

func (e *ctxExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	return e.Executor.Run(ctx, name, args...)
}

// TestLockRealm_DaemonRemovedBeforeRelease checks that a lock removed by
// hand while it was held is released without a warning.
func TestLockRealm_DaemonRemovedBeforeRelease(t *testing.T) {
	d := newFakeDaemon(t)
	var w warnings
	unlock, err := LockRealm(t.Context(), "sind", daemonOpts(t, d.client(), t.TempDir(), &w))
	require.NoError(t, err)

	d.remove("sind-lock")
	other := d.add("sind-lock", holderLabels("elsewhere", 1))
	unlock()

	assert.Empty(t, w.warnings())
	assert.Equal(t, other, d.network("sind-lock").id, "unlock leaves another holder's lock alone")
}

// TestLockRealm_DaemonLogs checks that without OnWait and OnWarning the
// wait and the warnings go to the log.
func TestLockRealm_DaemonLogs(t *testing.T) {
	d := newFakeDaemon(t)
	d.add("sind-lock", holderLabels("elsewhere", 4242))
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(sindlog.With(t.Context(), slog.New(slog.NewTextHandler(&logs, nil))), 100*time.Millisecond)
	defer cancel()

	_, err := LockRealm(ctx, "sind", &LockOptions{
		Dir:       t.TempDir(),
		Client:    d.client(),
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
		id: "net-id", token: "other-token", bootID: testBootID, pidNS: testPIDNS,
	}, h)

	h = holderOf(&docker.NetworkMeta{Labels: docker.Labels{labelLockPID: "x"}})
	assert.Zero(t, h.PID, "a PID that is not a number is unknown")
}

func TestSelf(t *testing.T) {
	host, _ := os.Hostname()
	p := self(fakeProc(t, testBootID, testPIDNS))
	assert.Equal(t, process{host: host, bootID: testBootID, pidNS: testPIDNS, pid: os.Getpid()}, p)

	// What cannot be read stays empty.
	p = self(t.TempDir())
	assert.Equal(t, process{host: host, pid: os.Getpid()}, p)
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
	rec.SetOnCall(newFakeDaemon(t).onCall)
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

	select {
	case <-acquired:
		t.Fatal("second client took the lock while the first held it")
	case <-time.After(500 * time.Millisecond):
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
	rec.SetOnCall(newFakeDaemon(t).onCall)
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
	rec.SetOnCall(newFakeDaemon(t).onCall)
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
