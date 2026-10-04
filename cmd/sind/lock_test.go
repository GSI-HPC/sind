// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockDaemon answers the docker commands of the realm lock's daemon part
// like dockerd: it keeps networks by name and refuses a name that is
// taken. Each command's client has its own mock.Executor, so the daemon
// serializes the calls itself.
type lockDaemon struct {
	mu    sync.Mutex
	exit1 error
	nets  map[string]map[string]string // name → labels
	ids   map[string]string            // ID → name
	// rmErr, when set, fails every network rm.
	rmErr error
}

func newLockDaemon(t *testing.T) *lockDaemon {
	return &lockDaemon{exit1: testutil.ExitCode1(t), nets: map[string]map[string]string{}, ids: map[string]string{}}
}

// add creates a network as another client would.
func (d *lockDaemon) add(name string, labels map[string]string) {
	id := fmt.Sprintf("%064d", len(d.ids)+1)
	d.nets[name] = labels
	d.ids[id] = name
}

// withClient returns ctx with a docker client of the daemon.
func (d *lockDaemon) withClient(ctx context.Context) context.Context {
	return withClient(ctx, docker.NewClient(&mock.Executor{OnCall: d.onCall}))
}

func (d *lockDaemon) onCall(args []string, _ string) mock.Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch args[1] {
	case "create":
		name := args[len(args)-1]
		if _, ok := d.nets[name]; ok {
			return mock.Result{Stderr: "Error response from daemon: network with name " + name + " already exists\n", Err: d.exit1}
		}
		labels := map[string]string{}
		for i := 2; i < len(args)-1; i++ {
			if args[i] == "--label" {
				k, v, _ := strings.Cut(args[i+1], "=")
				labels[k] = v
			}
		}
		d.add(name, labels)
		return mock.Result{Stdout: fmt.Sprintf("%064d\n", len(d.ids))}
	case "inspect":
		labels, ok := d.nets[args[2]]
		if !ok {
			return mock.Result{Stderr: testutil.NoSuchNetwork(args[2]), Err: d.exit1}
		}
		var id string
		for i, name := range d.ids {
			if name == args[2] {
				id = i
			}
		}
		out, _ := json.Marshal(map[string]any{"Name": args[2], "Id": id, "Created": "2026-10-04T10:02:03Z", "ConfigOnly": true, "Labels": labels})
		return mock.Result{Stdout: string(out) + "\n"}
	case "rm":
		if d.rmErr != nil {
			return mock.Result{Err: d.rmErr}
		}
		name, ok := d.ids[args[2]]
		if !ok {
			return mock.Result{Stderr: testutil.NoSuchNetwork(args[2]), Err: d.exit1}
		}
		delete(d.ids, args[2])
		delete(d.nets, name)
		return mock.Result{Stdout: args[2] + "\n"}
	}
	return mock.Result{Err: fmt.Errorf("unexpected docker call %v", args)}
}

// TestAcquireRealmLock covers the CLI's use of state.LockRealm; the lock
// itself is tested in pkg/state.
func TestAcquireRealmLock(t *testing.T) {
	t.Parallel()

	t.Run("creates directory, lock file and daemon lock", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()
		d := newLockDaemon(t)

		unlock, err := acquireRealmLock(d.withClient(context.Background()), "test-realm", stateHome)
		require.NoError(t, err)

		lockPath := filepath.Join(stateHome, "sind", "test-realm", "lock")
		_, err = os.Stat(lockPath)
		assert.NoError(t, err)
		assert.Contains(t, d.nets, "test-realm-lock")

		unlock()
		assert.Empty(t, d.nets, "unlock removes the daemon lock")
	})

	t.Run("contention blocks until released", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()
		d := newLockDaemon(t)

		var stderr1 bytes.Buffer
		unlock1, err := acquireRealmLock(d.withClient(withStderr(context.Background(), &stderr1)), "contention", stateHome)
		require.NoError(t, err)
		assert.Empty(t, stderr1.String(), "a free lock gives no warning")

		var stderr2 bytes.Buffer
		acquired := make(chan struct{})
		go func() {
			unlock2, err := acquireRealmLock(d.withClient(withStderr(context.Background(), &stderr2)), "contention", stateHome)
			assert.NoError(t, err)
			close(acquired)
			unlock2()
		}()

		// Second lock should not be acquired while first is held.
		select {
		case <-acquired:
			t.Fatal("second lock acquired while first is held")
		case <-time.After(100 * time.Millisecond):
		}

		unlock1()

		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("second lock not acquired after first was released")
		}
		assert.Equal(t, "Warning: waiting for another sind command in realm \"contention\" to finish\n", stderr2.String())
	})
}

// TestAcquireRealmLock_SharedDaemon checks that sind commands with
// different state directories that share a daemon wait for each other, and
// that the warning names the command that holds the lock.
func TestAcquireRealmLock_SharedDaemon(t *testing.T) {
	t.Parallel()
	d := newLockDaemon(t)
	ctx := d.withClient(context.Background())

	unlock1, err := acquireRealmLock(ctx, "shared", t.TempDir())
	require.NoError(t, err)

	var stderr bytes.Buffer
	acquired := make(chan struct{})
	go func() {
		unlock2, err := acquireRealmLock(withStderr(ctx, &stderr), "shared", t.TempDir())
		assert.NoError(t, err)
		close(acquired)
		unlock2()
	}()

	select {
	case <-acquired:
		t.Fatal("second command took the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	unlock1()
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second command did not take the lock after the first released it")
	}

	host, _ := os.Hostname()
	assert.Regexp(t, `^Warning: waiting for another sind command in realm "shared" to finish: \S.* \(pid `+
		strconv.Itoa(os.Getpid())+` on `+host+`, since 20\d\d-\d\d-\d\d \d\d:\d\d:\d\d\)\n$`, stderr.String())
}

// TestAcquireRealmLock_EscapesHolder checks that what the warning quotes
// of another client's lock cannot reach the terminal as control sequences.
func TestAcquireRealmLock_EscapesHolder(t *testing.T) {
	t.Parallel()
	d := newLockDaemon(t)
	d.add("evil-lock", map[string]string{
		"sind.lock.token":   "x",
		"sind.lock.command": "sind \x1b]0;owned\x07",
		"sind.lock.host":    "elsewhere",
		"sind.lock.pid":     "7",
	})
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(d.withClient(withStderr(context.Background(), &stderr)), 200*time.Millisecond)
	defer cancel()

	_, err := acquireRealmLock(ctx, "evil", t.TempDir())

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, stderr.String(), `to finish: sind \x1b]0;owned\x07 (pid 7 on elsewhere, since `)
	assert.NotContains(t, stderr.String(), "\x1b")
}

// TestAcquireRealmLock_ReleaseWarning checks that a daemon lock that the
// command cannot remove is reported on stderr with the command that does.
func TestAcquireRealmLock_ReleaseWarning(t *testing.T) {
	t.Parallel()
	d := newLockDaemon(t)
	var stderr bytes.Buffer

	unlock, err := acquireRealmLock(d.withClient(withStderr(context.Background(), &stderr)), "gone", t.TempDir())
	require.NoError(t, err)
	d.rmErr = fmt.Errorf("Cannot connect to the Docker daemon\x1b[2J")
	unlock()

	assert.Equal(t, `Warning: releasing the realm lock: Cannot connect to the Docker daemon\x1b[2J; remove it with: docker network rm gone-lock`+"\n", stderr.String())
}

func TestStderrFrom(t *testing.T) {
	assert.Equal(t, os.Stderr, stderrFrom(context.Background()))
	var buf bytes.Buffer
	assert.Equal(t, &buf, stderrFrom(withStderr(context.Background(), &buf)))
}
