// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listErrorMock returns a mock that fails on any call, simulating docker daemon unavailability.
func listErrorMock() *docker.MockExecutor {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		return docker.MockResult{Err: fmt.Errorf("docker daemon unavailable")}
	}
	return &m
}

// powerContainers returns a standard set of cluster containers for power tests.
func powerContainers() string {
	return ndjson(
		psEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller"},
		psEntry{ID: "c2", Names: "sind-dev-compute-0", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=compute"},
		psEntry{ID: "c3", Names: "sind-dev-compute-1", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=compute"},
	)
}

// --- PowerShutdown ---

func TestPower_Shutdown(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(context.Background(), client, "dev", []string{"compute-0", "compute-1"})

	require.NoError(t, err)

	// Verify docker stop was called for each node.
	var stops []string
	for _, c := range m.Calls {
		if c.Args[0] == "stop" {
			stops = append(stops, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-compute-0", "sind-dev-compute-1"}, stops)
}

func TestPower_Shutdown_NodeNotFound(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(context.Background(), client, "dev", []string{"compute-99"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "compute-99")
	assert.Contains(t, err.Error(), "not found")
}

func TestPower_Shutdown_StopError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return docker.MockResult{Err: fmt.Errorf("container already stopped")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")
}

func TestPower_Shutdown_EmptyNodes(t *testing.T) {
	var m docker.MockExecutor
	client := docker.NewClient(&m)

	err := PowerShutdown(context.Background(), client, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_Shutdown_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerShutdown(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

// --- PowerCut ---

func TestPower_Cut(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerCut(context.Background(), client, "dev", []string{"compute-0", "controller"})

	require.NoError(t, err)

	var kills []string
	for _, c := range m.Calls {
		if c.Args[0] == "kill" {
			kills = append(kills, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-compute-0", "sind-dev-controller"}, kills)
}

func TestPower_Cut_EmptyNodes(t *testing.T) {
	var m docker.MockExecutor
	client := docker.NewClient(&m)

	err := PowerCut(context.Background(), client, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_Cut_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerCut(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Cut_KillError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return docker.MockResult{Err: fmt.Errorf("container not running")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerCut(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "killing")
}

// --- PowerOn ---

func TestPower_On(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "start" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerOn(context.Background(), client, "dev", []string{"compute-0", "compute-1"})

	require.NoError(t, err)

	var starts []string
	for _, c := range m.Calls {
		if c.Args[0] == "start" {
			starts = append(starts, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-compute-0", "sind-dev-compute-1"}, starts)
}

func TestPower_On_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerOn(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_On_StartError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "start" {
			return docker.MockResult{Err: fmt.Errorf("container already running")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerOn(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerReboot ---

func TestPower_Reboot(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" || args[0] == "start" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerReboot(context.Background(), client, "dev", []string{"compute-0", "compute-1"})

	require.NoError(t, err)

	// Verify per-node stop+start sequence (not batched).
	var ops []string
	for _, c := range m.Calls {
		if c.Args[0] == "stop" || c.Args[0] == "start" {
			ops = append(ops, c.Args[0]+"="+c.Args[1])
		}
	}
	assert.Equal(t, []string{
		"stop=sind-dev-compute-0",
		"start=sind-dev-compute-0",
		"stop=sind-dev-compute-1",
		"start=sind-dev-compute-1",
	}, ops)
}

func TestPower_Reboot_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerReboot(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Reboot_StopError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return docker.MockResult{Err: fmt.Errorf("stop failed")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerReboot(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")
}

func TestPower_Reboot_StartError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return docker.MockResult{}
		}
		if args[0] == "start" {
			return docker.MockResult{Err: fmt.Errorf("start failed")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerReboot(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerCycle ---

func TestPower_Cycle(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" || args[0] == "start" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerCycle(context.Background(), client, "dev", []string{"compute-0", "compute-1"})

	require.NoError(t, err)

	// Verify per-node kill+start sequence (not batched).
	var ops []string
	for _, c := range m.Calls {
		if c.Args[0] == "kill" || c.Args[0] == "start" {
			ops = append(ops, c.Args[0]+"="+c.Args[1])
		}
	}
	assert.Equal(t, []string{
		"kill=sind-dev-compute-0",
		"start=sind-dev-compute-0",
		"kill=sind-dev-compute-1",
		"start=sind-dev-compute-1",
	}, ops)
}

func TestPower_Cycle_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerCycle(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Cycle_KillError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return docker.MockResult{Err: fmt.Errorf("kill failed")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerCycle(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "killing")
}

func TestPower_Cycle_StartError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return docker.MockResult{}
		}
		if args[0] == "start" {
			return docker.MockResult{Err: fmt.Errorf("start failed")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerCycle(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerFreeze ---

func TestPower_Freeze(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "pause" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerFreeze(context.Background(), client, "dev", []string{"compute-0", "compute-1"})

	require.NoError(t, err)

	var pauses []string
	for _, c := range m.Calls {
		if c.Args[0] == "pause" {
			pauses = append(pauses, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-compute-0", "sind-dev-compute-1"}, pauses)
}

func TestPower_Freeze_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerFreeze(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Freeze_PauseError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "pause" {
			return docker.MockResult{Err: fmt.Errorf("container is not running")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerFreeze(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pausing")
}

// --- PowerUnfreeze ---

func TestPower_Unfreeze(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "unpause" {
			return docker.MockResult{}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerUnfreeze(context.Background(), client, "dev", []string{"compute-0"})

	require.NoError(t, err)

	var unpauses []string
	for _, c := range m.Calls {
		if c.Args[0] == "unpause" {
			unpauses = append(unpauses, c.Args[1])
		}
	}
	assert.Equal(t, []string{"sind-dev-compute-0"}, unpauses)
}

func TestPower_Unfreeze_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerUnfreeze(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Unfreeze_UnpauseError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: powerContainers()}
		}
		if args[0] == "unpause" {
			return docker.MockResult{Err: fmt.Errorf("container is not paused")}
		}
		return docker.MockResult{}
	}
	client := docker.NewClient(&m)

	err := PowerUnfreeze(context.Background(), client, "dev", []string{"compute-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unpausing")
}
