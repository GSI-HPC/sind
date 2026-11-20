// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/fakedocker"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withDaemon returns ctx with a docker client of d, which answers the
// commands of the realm lock's daemon part.
func withDaemon(ctx context.Context, d *fakedocker.Daemon) context.Context {
	return withClient(ctx, d.Client())
}

// TestAcquireRealmLock covers the CLI's use of state.LockRealm; the lock
// itself is tested in pkg/state.
func TestAcquireRealmLock(t *testing.T) {
	t.Parallel()

	t.Run("creates directory, lock file and daemon lock", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()
		d := fakedocker.New(t)

		unlock, err := acquireRealmLock(withDaemon(context.Background(), d), "test-realm", stateHome)
		require.NoError(t, err)

		lockPath := filepath.Join(stateHome, "sind", "test-realm", "lock")
		_, err = os.Stat(lockPath)
		assert.NoError(t, err)
		assert.Contains(t, d.Networks(), "test-realm-lock")

		unlock()
		assert.Empty(t, d.Networks(), "unlock removes the daemon lock")
	})

	t.Run("contention blocks until released", func(t *testing.T) {
		t.Parallel()
		stateHome := t.TempDir()
		d := fakedocker.New(t)

		var stderr1 bytes.Buffer
		unlock1, err := acquireRealmLock(withDaemon(withStderr(context.Background(), &stderr1), d), "contention", stateHome)
		require.NoError(t, err)
		assert.Empty(t, stderr1.String(), "a free lock gives no warning")

		var stderr2 bytes.Buffer
		acquired := make(chan struct{})
		go func() {
			unlock2, err := acquireRealmLock(withDaemon(withStderr(context.Background(), &stderr2), d), "contention", stateHome)
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
	d := fakedocker.New(t)
	ctx := withDaemon(context.Background(), d)

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
	d := fakedocker.New(t)
	d.Add("evil-lock", map[string]string{
		"sind.lock.token":   "x",
		"sind.lock.command": "sind \x1b]0;owned\x07",
		"sind.lock.host":    "elsewhere",
		"sind.lock.pid":     "7",
	})
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(withDaemon(withStderr(context.Background(), &stderr), d), 200*time.Millisecond)
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
	d := fakedocker.New(t)
	var stderr bytes.Buffer

	unlock, err := acquireRealmLock(withDaemon(withStderr(context.Background(), &stderr), d), "gone", t.TempDir())
	require.NoError(t, err)
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "rm" {
			return &mock.Result{Err: fmt.Errorf("Cannot connect to the Docker daemon\x1b[2J")}
		}
		return nil
	})
	unlock()

	assert.Equal(t, `Warning: releasing the realm lock: Cannot connect to the Docker daemon\x1b[2J; remove it with: docker network rm gone-lock`+"\n", stderr.String())
}

func TestStderrFrom(t *testing.T) {
	assert.Equal(t, os.Stderr, stderrFrom(context.Background()))
	var buf bytes.Buffer
	assert.Equal(t, &buf, stderrFrom(withStderr(context.Background(), &buf)))
}

// TestMutatingCommandsTakeTheRealmLock runs each command that changes a
// realm while another sind command holds the realm's lock, and checks that
// it waits for the lock before any docker call that is not the lock's,
// other than the daemon check of create cluster. The command is
// interrupted while it waits. The realm is not the default one, so that a
// command that locks the default realm fails.
func TestMutatingCommandsTakeTheRealmLock(t *testing.T) {
	const daemonInfo = "info --format {{json .}}"
	const realm, lockNet = "ci", "ci-lock"
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, tt := range []struct {
		args   []string
		before []string // the docker calls the command makes before the lock
	}{
		{[]string{"create", "cluster"}, []string{daemonInfo}},
		{[]string{"create", "worker"}, nil},
		{[]string{"delete", "cluster"}, nil},
		{[]string{"delete", "cluster", "--all"}, nil},
		{[]string{"delete", "realm"}, nil},
		{[]string{"delete", "worker", "worker-0"}, nil},
		{[]string{"connect", "container", "ext"}, nil},
		{[]string{"disconnect", "container", "ext"}, nil},
		{[]string{"power", "on", "worker-0"}, nil},
		{[]string{"power", "reboot", "worker-0"}, nil},
		{[]string{"power", "cycle", "worker-0"}, nil},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			d := fakedocker.New(t)
			d.Add(lockNet, map[string]string{
				"sind.lock.token":   "x",
				"sind.lock.command": "sind create cluster",
				"sind.lock.host":    "elsewhere",
				"sind.lock.pid":     "7",
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var others []string
			lockCalls := 0
			ctx = withClient(ctx, docker.NewClient(&mock.Executor{OnCall: func(args []string, stdin string) mock.Result {
				if args[0] != "network" || args[len(args)-1] != lockNet && args[len(args)-3] != lockNet {
					joined := strings.Join(args, " ")
					others = append(others, joined)
					if joined == daemonInfo {
						return mock.Result{Stdout: `{"ServerVersion":"28.0.0","CgroupVersion":"2"}` + "\n"}
					}
					return mock.Result{Err: fmt.Errorf("unexpected docker call %v", args)}
				}
				if lockCalls++; lockCalls == 4 {
					cancel()
				}
				return d.OnCall(args, stdin)
			}}))
			var stderr bytes.Buffer

			code := run(ctx, append([]string{"--realm", realm}, tt.args...), &stderr)

			assert.Equal(t, exitInterrupted, code, stderr.String())
			assert.Contains(t, stderr.String(), `Warning: waiting for another sind command in realm "ci" to finish: sind create cluster (pid 7 on elsewhere`)
			assert.Contains(t, stderr.String(), "interrupted: taking the realm lock on the Docker daemon: context canceled")
			assert.Equal(t, tt.before, others)
		})
	}
}
