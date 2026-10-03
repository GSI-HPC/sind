// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorWith(t *testing.T) {
	err := errorWith(ErrNodeNotFound, "node %q not found in cluster %q", "worker-9", "dev")
	assert.Equal(t, `node "worker-9" not found in cluster "dev"`, err.Error())
	require.ErrorIs(t, err, ErrNodeNotFound)
	require.ErrorIs(t, fmt.Errorf("power: %w", err), ErrNodeNotFound)
	assert.NotErrorIs(t, err, ErrClusterNotFound)
}

func TestWorkerAdd_RollbackFailuresJoined(t *testing.T) {
	// enable slurmd fails, and so does every step of the rollback but the
	// removal of a container that is already gone.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	onCall := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "exec" && strings.HasSuffix(joined, "systemctl enable --now slurmd"):
			return mock.Result{Err: fmt.Errorf("enable failed")}
		case args[0] == "rm" && strings.HasSuffix(joined, "sind-dev-worker-1"):
			return mock.Result{Stderr: "Error response from daemon: cannot remove container\n", Err: fmt.Errorf("exit status 2")}
		case args[0] == "rm" && strings.HasSuffix(joined, "sind-dev-worker-2"):
			return mock.Result{Stderr: "Error: No such container: sind-dev-worker-2\n", Err: notFoundErr(t)}
		}
		return onCall(args, stdin)
	}
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 2}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enable failed")
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
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := cleanupWorkers(t.Context(), client, mgr, mesh.DefaultRealm, "dev", []RunConfig{{ShortName: "worker-1"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing DNS records: ")
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
