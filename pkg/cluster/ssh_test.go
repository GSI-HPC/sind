// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSSHArgs(t *testing.T) {
	ssh := string(mesh.SSHContainerName)
	tests := []struct {
		name          string
		node, cluster string
		tty           bool
		opts, command []string
		want          []string
	}{
		{"interactive", "worker-0", "dev", true, nil, nil, []string{"exec", "-i", "-t", ssh, "ssh", "worker-0.dev.sind.sind"}},
		{"non-interactive", "worker-0", "dev", false, nil, nil, []string{"exec", "-i", ssh, "ssh", "worker-0.dev.sind.sind"}},
		{"command", "controller", "default", false, nil, []string{"hostname"},
			[]string{"exec", "-i", ssh, "ssh", "controller.default.sind.sind", "hostname"}},
		{"multi-word command", "worker-0", "dev", false, nil, []string{"ls", "-la", "/tmp"},
			[]string{"exec", "-i", ssh, "ssh", "worker-0.dev.sind.sind", "ls", "-la", "/tmp"}},
		{"option", "worker-0", "dev", true, []string{"-v"}, nil, []string{"exec", "-i", "-t", ssh, "ssh", "-v", "worker-0.dev.sind.sind"}},
		{"port forwarding", "controller", "default", true, []string{"-L", "8080:localhost:80"}, nil,
			[]string{"exec", "-i", "-t", ssh, "ssh", "-L", "8080:localhost:80", "controller.default.sind.sind"}},
		{"force tty", "worker-0", "dev", true, []string{"-t"}, []string{"top"},
			[]string{"exec", "-i", "-t", ssh, "ssh", "-t", "worker-0.dev.sind.sind", "top"}},
		{"options and command", "worker-0", "dev", false, []string{"-v", "-o", "StrictHostKeyChecking=no"}, []string{"uptime"},
			[]string{"exec", "-i", ssh, "ssh", "-v", "-o", "StrictHostKeyChecking=no", "worker-0.dev.sind.sind", "uptime"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildSSHArgs(mesh.SSHContainerName, tt.node, tt.cluster, mesh.DefaultRealm, tt.tty, tt.opts, tt.command))
		})
	}
}

func TestBuildContainerExecArgs(t *testing.T) {
	tests := []struct {
		name            string
		container, user string
		workDir         string
		tty             bool
		command         []string
		want            []string
	}{
		{"interactive shell", "sind-dev-controller", "", DefaultDataMountPath, true, nil,
			[]string{"exec", "-i", "-t", "-w", "/data", "sind-dev-controller", "bash", "-l"}},
		{"non-interactive shell", "sind-dev-controller", "", DefaultDataMountPath, false, nil,
			[]string{"exec", "-i", "-w", "/data", "sind-dev-controller", "bash", "-l"}},
		{"command", "sind-dev-controller", "", DefaultDataMountPath, false, []string{"sinfo"},
			[]string{"exec", "-i", "-w", "/data", "sind-dev-controller", "sinfo"}},
		{"multi-word command", "sind-dev-worker-0", "", DefaultDataMountPath, false, []string{"srun", "hostname"},
			[]string{"exec", "-i", "-w", "/data", "sind-dev-worker-0", "srun", "hostname"}},
		{"custom work dir", "sind-dev-controller", "", "/shared", false, []string{"ls"},
			[]string{"exec", "-i", "-w", "/shared", "sind-dev-controller", "ls"}},
		// A cluster user starts in its home directory, not the data mount.
		{"user", "sind-dev-submitter", "alice", DefaultDataMountPath, true, nil,
			[]string{"exec", "-i", "-t", "-u", "alice", "-w", "/home/alice", "sind-dev-submitter", "bash", "-l"}},
		// root is the default user; its home is not under /home.
		{"root", "sind-dev-submitter", "root", DefaultDataMountPath, false, []string{"id"},
			[]string{"exec", "-i", "-w", "/data", "sind-dev-submitter", "id"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildContainerExecArgs(docker.ContainerName(tt.container), tt.user, tt.workDir, tt.tty, tt.command))
		})
	}
}

// --- EnterTarget ---

func TestEnter_TargetSelection_WithSubmitter(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller"},
		testutil.PsEntry{ID: "c2", Names: "sind-dev-submitter", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=submitter"},
		testutil.PsEntry{ID: "c3", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
	), "", nil)
	client := docker.NewClient(&m)

	target, _, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, "submitter", target)
}

func TestEnter_WorkDir(t *testing.T) {
	tests := []struct {
		name   string
		labels string
		want   string
	}{
		{"default", "sind.cluster=dev,sind.role=submitter", "/data"},
		{"custom mount path", "sind.cluster=dev,sind.role=submitter,sind.data.mountpath=/shared", "/shared"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(testutil.NDJSON(
				testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				testutil.PsEntry{ID: "c2", Names: "sind-dev-submitter", State: "running", Image: "img:1",
					Labels: tt.labels},
			), "", nil)

			target, workDir, err := EnterTarget(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

			require.NoError(t, err)
			assert.Equal(t, "submitter", target)
			assert.Equal(t, tt.want, workDir)
		})
	}
}

func TestEnter_TargetSelection_NoSubmitter(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller"},
		testutil.PsEntry{ID: "c2", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
	), "", nil)
	client := docker.NewClient(&m)

	target, _, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, "controller", target)
}

func TestEnter_TargetSelection_ControllerPair(t *testing.T) {
	tests := []struct {
		name          string
		primaryState  string
		backupState   string
		heartbeat     mock.Result
		want          string
		wantHeartbeat bool
	}{
		{name: "primary in control", primaryState: "running", backupState: "running",
			heartbeat: mock.Result{Stdout: heartbeatOutput(1000, 995, 0)}, want: "controller", wantHeartbeat: true},
		{name: "backup in control after takeover", primaryState: "running", backupState: "running",
			heartbeat: mock.Result{Stdout: heartbeatOutput(1000, 995, 1)}, want: "controller-backup", wantHeartbeat: true},
		{name: "stale backup heartbeat", primaryState: "running", backupState: "running",
			heartbeat: mock.Result{Stdout: heartbeatOutput(1000, 800, 1)}, want: "controller", wantHeartbeat: true},
		{name: "heartbeat unreadable", primaryState: "running", backupState: "running",
			heartbeat: mock.Result{Err: fmt.Errorf("exit status 1")}, want: "controller", wantHeartbeat: true},
		{name: "only backup runs", primaryState: "exited", backupState: "running", want: "controller-backup"},
		{name: "backup stopped", primaryState: "running", backupState: "exited", want: "controller"},
		{name: "both stopped falls back to controller", primaryState: "exited", backupState: "exited", want: "controller"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			var readHB bool
			m.OnCall = func(args []string, _ string) mock.Result {
				if args[0] == "ps" {
					return mock.Result{Stdout: testutil.NDJSON(
						testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: tt.primaryState, Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=controller"},
						testutil.PsEntry{ID: "c2", Names: "sind-dev-controller-backup", State: tt.backupState, Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=controller"},
						testutil.PsEntry{ID: "c3", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=worker"},
					)}
				}
				if isHeartbeatRead(args) {
					readHB = true
					return tt.heartbeat
				}
				return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
			}

			target, _, err := EnterTarget(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

			require.NoError(t, err)
			assert.Equal(t, tt.want, target)
			assert.Equal(t, tt.wantHeartbeat, readHB)
		})
	}
}

func TestEnter_TargetSelection_UnmanagedPair(t *testing.T) {
	tests := []struct {
		name         string
		primaryState string
		backupState  string
		want         string
	}{
		{name: "both running", primaryState: "running", backupState: "running", want: "controller"},
		{name: "only backup runs", primaryState: "exited", backupState: "running", want: "controller-backup"},
		{name: "both stopped", primaryState: "exited", backupState: "exited", want: "controller"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.OnCall = func(args []string, _ string) mock.Result {
				if args[0] == "ps" {
					return mock.Result{Stdout: testutil.NDJSON(
						testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: tt.primaryState, Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=controller,sind.managed=false"},
						testutil.PsEntry{ID: "c2", Names: "sind-dev-controller-backup", State: tt.backupState, Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=controller,sind.managed=false"},
					)}
				}
				return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
			}

			target, _, err := EnterTarget(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

			require.NoError(t, err)
			assert.Equal(t, tt.want, target)
			assert.Len(t, m.Calls, 1, "no heartbeat read")
		})
	}
}

func TestEnter_TargetSelection_NoControllerOrSubmitter(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "c1", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
	), "", nil)
	client := docker.NewClient(&m)

	_, _, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no submitter or controller")
}

func TestEnter_TargetSelection_EmptyCluster(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	client := docker.NewClient(&m)

	_, _, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.EqualError(t, err, `cluster "dev" not found in realm "sind"`)
	assert.ErrorIs(t, err, ErrClusterNotFound)
}

func TestEnter_TargetSelection_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	_, _, err := EnterTarget(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}
