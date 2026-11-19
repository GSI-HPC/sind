// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorWith(t *testing.T) {
	err := errorWith(ErrNodeNotFound, "node %q not found in cluster %q", "worker-9", "dev")
	assert.EqualError(t, err, `node "worker-9" not found in cluster "dev"`)
	require.ErrorIs(t, err, ErrNodeNotFound)
	require.ErrorIs(t, fmt.Errorf("power: %w", err), ErrNodeNotFound)
	assert.NotErrorIs(t, err, ErrClusterNotFound)
}

func TestWorkerAdd_RollbackFailuresJoined(t *testing.T) {
	// enable slurmd fails, and so does every step of the rollback but the
	// removal of a container that is already gone.
	onCall := workerAddOnCall(t)
	_, _, err := addWorkers(t, func(args []string, stdin string) mock.Result {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "exec" && strings.HasSuffix(joined, "systemctl enable --now slurmd"):
			return mock.Result{Err: fmt.Errorf("enable failed")}
		case args[0] == "rm" && strings.HasSuffix(joined, "sind-dev-worker-1"):
			return mock.Result{Stderr: "Error response from daemon: cannot remove container\n", Err: fmt.Errorf("exit status 2")}
		case args[0] == "rm" && strings.HasSuffix(joined, "sind-dev-worker-2"):
			return mock.Result{Stderr: "Error: No such container: sind-dev-worker-2\n", Err: testutil.ExitCode1(t)}
		}
		return onCall(args, stdin)
	}, WorkerAddOptions{ClusterName: "dev", Count: 2})
	require.ErrorContains(t, err, "enable failed")
	assert.Contains(t, err.Error(), "\nrolling back: removing container sind-dev-worker-1: ")
	assert.NotContains(t, err.Error(), "sind-dev-worker-2:")
}

func TestCleanupWorkers_MeshFailures(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		// Reading the Corefile and known_hosts fails; the containers go.
		if args[0] == "cp" || (args[0] == "exec" && args[1] == "sind-ssh") {
			return mock.Result{Err: fmt.Errorf("relay down")}
		}
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers("worker-1")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := cleanupWorkers(t.Context(), client, mgr, mesh.DefaultRealm, "dev", []nodeRunConfig{{ShortName: "worker-1"}})

	require.ErrorContains(t, err, "removing DNS records: ")
	assert.Contains(t, err.Error(), "removing known hosts: ")
	var removed bool
	for _, c := range m.Calls {
		if c.Args[0] == "rm" {
			removed = true
		}
	}
	assert.True(t, removed)
	assert.False(t, errors.Is(err, ErrNodeNotFound))
}

// barrier holds each docker command whose first argument is a key until
// n of them run at once, or fails it after a while: commands that run one
// at a time never meet.
type barrier struct {
	*mock.Executor
	met     map[string]chan struct{}
	arrived map[string]*sync.WaitGroup
}

func newBarrier(m *mock.Executor, n int, keys ...string) barrier {
	b := barrier{Executor: m, met: map[string]chan struct{}{}, arrived: map[string]*sync.WaitGroup{}}
	for _, k := range keys {
		wg, met := &sync.WaitGroup{}, make(chan struct{})
		wg.Add(n)
		go func() { wg.Wait(); close(met) }()
		b.met[k], b.arrived[k] = met, wg
	}
	return b
}

func (b barrier) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if met, ok := b.met[args[0]]; ok {
		b.arrived[args[0]].Done()
		select {
		case <-met:
		case <-time.After(5 * time.Second):
			return "", "", fmt.Errorf("docker %s ran alone", args[0])
		}
	}
	return b.Executor.Run(ctx, name, args...)
}

func TestCleanupWorkers_Parallel(t *testing.T) {
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect":
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", nil)}
		case args[0] == "cp" && args[1] == "sind-dns:/Corefile":
			return mock.Result{Stdout: testutil.TarArchive("Corefile", corefileWith(nil))}
		case args[0] == "ps":
			return mock.Result{Stdout: workerContainers("worker-1", "worker-2")}
		}
		return mock.Result{}
	}}
	client := docker.NewClient(newBarrier(m, 2, "rm"))
	mgr := mesh.NewManager(docker.NewClient(m), mesh.DefaultRealm)

	err := cleanupWorkers(t.Context(), client, mgr, mesh.DefaultRealm, "dev", []nodeRunConfig{{ShortName: "worker-1"}, {ShortName: "worker-2"}})

	require.NoError(t, err, "the removals run at once")
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"))
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-2"))
}

func TestCleanupWorkers_ListingFails(t *testing.T) {
	// Without a listing, the rollback tries to remove every worker.
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "ps":
			return mock.Result{Err: fmt.Errorf("daemon unreachable")}
		case args[0] == "inspect":
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", nil)}
		case args[0] == "cp" && args[1] == "sind-dns:/Corefile":
			return mock.Result{Stdout: testutil.TarArchive("Corefile", corefileWith(nil))}
		}
		return mock.Result{}
	}}
	client := docker.NewClient(m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := cleanupWorkers(t.Context(), client, mgr, mesh.DefaultRealm, "dev", []nodeRunConfig{{ShortName: "worker-1"}, {ShortName: "worker-2"}})

	require.NoError(t, err)
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"))
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-2"))
}
