// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listErrorMock returns a mock that fails on any call, simulating docker daemon unavailability.
func listErrorMock() *cmdexec.MockExecutor {
	var m cmdexec.MockExecutor
	m.OnCall = func(_ []string, _ string) cmdexec.MockResult {
		return cmdexec.MockResult{Err: fmt.Errorf("docker daemon unavailable")}
	}
	return &m
}

// powerContainers returns a standard set of cluster containers for power tests.
func powerContainers() string {
	return ndjson(
		psEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller"},
		psEntry{ID: "c2", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
		psEntry{ID: "c3", Names: "sind-dev-worker-1", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
	)
}

// --- PowerShutdown ---

func TestPower_Shutdown(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	// Verify docker stop was called for each node.
	var stops []string
	for _, c := range m.Calls {
		if c.Args[0] == "stop" {
			stops = append(stops, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, stops)
}

func TestPower_Shutdown_NodeNotFound(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-99"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "worker-99")
	assert.Contains(t, err.Error(), "not found")
}

func TestPower_Shutdown_StopError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return cmdexec.MockResult{Err: fmt.Errorf("container already stopped")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")
}

func TestPower_Shutdown_EmptyNodes(t *testing.T) {
	var m cmdexec.MockExecutor
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_Shutdown_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

// --- PowerCut ---

func TestPower_Cut(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "controller"})

	require.NoError(t, err)

	var kills []string
	for _, c := range m.Calls {
		if c.Args[0] == "kill" {
			kills = append(kills, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-controller"}, kills)
}

func TestPower_Cut_EmptyNodes(t *testing.T) {
	var m cmdexec.MockExecutor
	client := docker.NewClient(&m)

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_Cut_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Cut_KillError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return cmdexec.MockResult{Err: fmt.Errorf("container not running")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "killing")
}

// --- PowerOn ---

func TestPower_On(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "start" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	var starts []string
	for _, c := range m.Calls {
		if c.Args[0] == "start" {
			starts = append(starts, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, starts)
}

func TestPower_On_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerOn(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_On_StartError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "start" {
			return cmdexec.MockResult{Err: fmt.Errorf("container already running")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerReboot ---

func TestPower_Reboot(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" || args[0] == "start" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	// Verify per-node stop+start sequence (not batched).
	var ops []string
	for _, c := range m.Calls {
		if c.Args[0] == "stop" || c.Args[0] == "start" {
			ops = append(ops, c.Args[0]+"="+c.Args[1])
		}
	}
	assert.Equal(t, []string{
		"stop=sind-dev-worker-0",
		"start=sind-dev-worker-0",
		"stop=sind-dev-worker-1",
		"start=sind-dev-worker-1",
	}, ops)
}

func TestPower_Reboot_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerReboot(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Reboot_StopError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return cmdexec.MockResult{Err: fmt.Errorf("stop failed")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")
}

func TestPower_Reboot_StartError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return cmdexec.MockResult{}
		}
		if args[0] == "start" {
			return cmdexec.MockResult{Err: fmt.Errorf("start failed")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerCycle ---

func TestPower_Cycle(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" || args[0] == "start" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerCycle(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	// Verify per-node kill+start sequence (not batched).
	var ops []string
	for _, c := range m.Calls {
		if c.Args[0] == "kill" || c.Args[0] == "start" {
			ops = append(ops, c.Args[0]+"="+c.Args[1])
		}
	}
	assert.Equal(t, []string{
		"kill=sind-dev-worker-0",
		"start=sind-dev-worker-0",
		"kill=sind-dev-worker-1",
		"start=sind-dev-worker-1",
	}, ops)
}

func TestPower_Cycle_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerCycle(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Cycle_KillError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return cmdexec.MockResult{Err: fmt.Errorf("kill failed")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerCycle(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "killing")
}

func TestPower_Cycle_StartError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return cmdexec.MockResult{}
		}
		if args[0] == "start" {
			return cmdexec.MockResult{Err: fmt.Errorf("start failed")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerCycle(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerFreeze ---

func TestPower_Freeze(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "pause" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerFreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	var pauses []string
	for _, c := range m.Calls {
		if c.Args[0] == "pause" {
			pauses = append(pauses, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, pauses)
}

func TestPower_Freeze_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerFreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Freeze_PauseError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "pause" {
			return cmdexec.MockResult{Err: fmt.Errorf("container is not running")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerFreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pausing")
}

// --- PowerUnfreeze ---

func TestPower_Unfreeze(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "unpause" {
			return cmdexec.MockResult{}
		}
		return cmdexec.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerUnfreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.NoError(t, err)

	var unpauses []string
	for _, c := range m.Calls {
		if c.Args[0] == "unpause" {
			unpauses = append(unpauses, c.Args[1])
		}
	}
	assert.Equal(t, []string{"sind-dev-worker-0"}, unpauses)
}

func TestPower_Unfreeze_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerUnfreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Unfreeze_UnpauseError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.OnCall = func(args []string, _ string) cmdexec.MockResult {
		if args[0] == "ps" {
			return cmdexec.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "unpause" {
			return cmdexec.MockResult{Err: fmt.Errorf("container is not paused")}
		}
		return cmdexec.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerUnfreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unpausing")
}

// --- Lifecycle ---

func TestPowerLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	cluster := "it-pwr"
	ctrName := ContainerName(mesh.DefaultRealm, cluster, "worker-0")

	if !rec.IsIntegration() {
		// Create container with labels
		rec.AddResult("ctr-id\n", "", nil)

		// Each power op: ListContainers (resolve targets) + the operation itself
		psLine := `{"ID":"ctr-id","Names":"` + string(ctrName) + `","State":"running","Image":"busybox:latest","Labels":"sind.cluster=` + cluster + `,sind.role=worker"}` + "\n"

		// PowerShutdown: ps + stop
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerOn: ps + start
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerFreeze: ps + pause
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerUnfreeze: ps + unpause
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerReboot: ps + stop + start
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerCycle: ps + kill + start
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// Cleanup: kill + rm
		rec.AddResult(string(ctrName)+"\n", "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
	})

	// Create a labeled container.
	_, err := c.CreateContainer(ctx,
		"--name", string(ctrName),
		"--label", LabelCluster+"="+cluster,
		"--label", LabelRole+"=worker",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)

	nodes := []string{"worker-0"}

	// Shutdown (stop) + On (start).
	err = PowerShutdown(ctx, c, mesh.DefaultRealm, cluster, nodes)
	require.NoError(t, err)

	err = PowerOn(ctx, c, mesh.DefaultRealm, cluster, nodes)
	require.NoError(t, err)

	// Freeze (pause) + Unfreeze (unpause).
	err = PowerFreeze(ctx, c, mesh.DefaultRealm, cluster, nodes)
	require.NoError(t, err)

	err = PowerUnfreeze(ctx, c, mesh.DefaultRealm, cluster, nodes)
	require.NoError(t, err)

	// Reboot (stop + start).
	err = PowerReboot(ctx, c, mesh.DefaultRealm, cluster, nodes)
	require.NoError(t, err)

	// Cycle (kill + start).
	err = PowerCycle(ctx, c, mesh.DefaultRealm, cluster, nodes)
	require.NoError(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}
