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
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		return docker.MockResult{Err: fmt.Errorf("docker daemon unavailable")}
	}
	client := docker.NewClient(&m)

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
