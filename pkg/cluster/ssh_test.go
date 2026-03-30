// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSH_BuildCommand(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "worker-0", "dev", true, nil, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", string(mesh.SSHContainerName),
		"ssh", "worker-0.dev.sind.local",
	}, args)
}

func TestSSH_BuildCommand_NonInteractive(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "worker-0", "dev", false, nil, nil)

	assert.Equal(t, []string{
		"exec", "-i", string(mesh.SSHContainerName),
		"ssh", "worker-0.dev.sind.local",
	}, args)
}

func TestSSH_BuildCommand_WithCommand(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "controller", "default", false, nil, []string{"hostname"})

	assert.Equal(t, []string{
		"exec", "-i", string(mesh.SSHContainerName),
		"ssh", "controller.default.sind.local", "hostname",
	}, args)
}

func TestSSH_BuildCommand_WithMultiWordCommand(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "worker-0", "dev", false, nil, []string{"ls", "-la", "/tmp"})

	assert.Equal(t, []string{
		"exec", "-i", string(mesh.SSHContainerName),
		"ssh", "worker-0.dev.sind.local", "ls", "-la", "/tmp",
	}, args)
}

func TestSSH_PassthroughOptions(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "worker-0", "dev", true, []string{"-v"}, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", string(mesh.SSHContainerName),
		"ssh", "-v", "worker-0.dev.sind.local",
	}, args)
}

func TestSSH_PassthroughOptions_PortForwarding(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "controller", "default", true,
		[]string{"-L", "8080:localhost:80"}, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", string(mesh.SSHContainerName),
		"ssh", "-L", "8080:localhost:80", "controller.default.sind.local",
	}, args)
}

func TestSSH_PassthroughOptions_ForceTTY(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "worker-0", "dev", true,
		[]string{"-t"}, []string{"top"})

	assert.Equal(t, []string{
		"exec", "-i", "-t", string(mesh.SSHContainerName),
		"ssh", "-t", "worker-0.dev.sind.local", "top",
	}, args)
}

func TestSSH_PassthroughOptions_Multiple(t *testing.T) {
	args := BuildSSHArgs(mesh.SSHContainerName, "worker-0", "dev", false,
		[]string{"-v", "-o", "StrictHostKeyChecking=no"},
		[]string{"uptime"})

	assert.Equal(t, []string{
		"exec", "-i", string(mesh.SSHContainerName),
		"ssh", "-v", "-o", "StrictHostKeyChecking=no",
		"worker-0.dev.sind.local", "uptime",
	}, args)
}

// --- BuildContainerExecArgs ---

func TestContainerExec_InteractiveShell(t *testing.T) {
	args := BuildContainerExecArgs("sind-dev-controller", true, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", "-w", "/data", "sind-dev-controller",
		"bash", "-l",
	}, args)
}

func TestContainerExec_NonInteractiveShell(t *testing.T) {
	args := BuildContainerExecArgs("sind-dev-controller", false, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-w", "/data", "sind-dev-controller",
		"bash", "-l",
	}, args)
}

func TestContainerExec_WithCommand(t *testing.T) {
	args := BuildContainerExecArgs("sind-dev-controller", false, []string{"sinfo"})

	assert.Equal(t, []string{
		"exec", "-i", "-w", "/data", "sind-dev-controller",
		"sinfo",
	}, args)
}

func TestContainerExec_WithMultiWordCommand(t *testing.T) {
	args := BuildContainerExecArgs("sind-dev-worker-0", false, []string{"srun", "hostname"})

	assert.Equal(t, []string{
		"exec", "-i", "-w", "/data", "sind-dev-worker-0",
		"srun", "hostname",
	}, args)
}

// --- EnterTarget ---

func TestEnter_TargetSelection_WithSubmitter(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ndjson(
				psEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				psEntry{ID: "c2", Names: "sind-dev-submitter", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=submitter"},
				psEntry{ID: "c3", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	target, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, "submitter", target)
}

func TestEnter_TargetSelection_NoSubmitter(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ndjson(
				psEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				psEntry{ID: "c2", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	target, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, "controller", target)
}

func TestEnter_TargetSelection_NoControllerOrSubmitter(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ndjson(
				psEntry{ID: "c1", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	_, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no submitter or controller")
}

func TestEnter_TargetSelection_EmptyCluster(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ""}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	_, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no submitter or controller")
}

func TestEnter_TargetSelection_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	_, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}
