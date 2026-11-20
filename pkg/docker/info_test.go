// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInfoLifecycle reads a real daemon in integration mode: the rootful
// cgroup v2 daemon that sind needs.
func TestInfoLifecycle(t *testing.T) {
	t.Parallel()
	skipWithoutDocker(t)
	c, rec := newTestClient(t)

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, info.ServerVersion)
	assert.Equal(t, "2", info.CgroupVersion)
	assert.False(t, info.HasSecurityOption("rootless"))
	assert.False(t, info.HasSecurityOption("userns"))

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestInfo(t *testing.T) {
	var m mock.Executor
	m.AddResult(`{"ID":"x","ServerVersion":"29.1.0","CgroupVersion":"2","CgroupDriver":"systemd",`+
		`"SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"],"OperatingSystem":"Ubuntu 24.04.3 LTS",`+
		`"Name":"runner","KernelVersion":"6.8.0-1017-azure","MemTotal":16766226432,"NCPU":4,"ClientInfo":{"Version":"29.1.0"}}`+"\n", "", nil)
	c := NewClient(&m)

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &DaemonInfo{
		ServerVersion:   "29.1.0",
		CgroupVersion:   "2",
		CgroupDriver:    "systemd",
		SecurityOptions: []string{"name=seccomp,profile=builtin", "name=cgroupns"},
		OperatingSystem: "Ubuntu 24.04.3 LTS",
		Name:            "runner",
		KernelVersion:   "6.8.0-1017-azure",
		MemTotal:        16766226432,
		NCPU:            4,
		ClientVersion:   "29.1.0",
	}, info)
	assert.False(t, info.DockerDesktop())
	assert.Equal(t, []string{"info", "--format", "{{json .}}"}, m.Calls[0].Args)
}

func TestInfo_Errors(t *testing.T) {
	tests := []struct {
		name    string
		result  mock.Result
		wantErr string
	}{
		{"exit status", mock.Result{Stderr: "permission denied\n", Err: fmt.Errorf("exit status 1")}, "exit status 1"},
		{"server errors", mock.Result{Stdout: `{"ServerErrors":["Error response from daemon: client version 1.52 is too new"],"ClientInfo":{}}`},
			"Error response from daemon: client version 1.52 is too new"},
		{"no server version", mock.Result{Stdout: `{"ClientInfo":{}}`}, "docker info reported no server version"},
		{"not JSON", mock.Result{Stdout: "Client: Docker Engine"}, "parsing docker info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(tt.result.Stdout, tt.result.Stderr, tt.result.Err)
			info, err := NewClient(&m).Info(t.Context())
			require.ErrorContains(t, err, tt.wantErr)
			assert.Nil(t, info)
		})
	}
}

// TestInfo_ServerErrorsUnreachable checks that a daemon the CLI cannot
// reach, which docker info --format reports in ServerErrors with exit
// status 0, is an *UnreachableError like that of a failed command, in
// whichever entry it is.
func TestInfo_ServerErrorsUnreachable(t *testing.T) {
	for name, serverErrors := range map[string]string{
		"only":   `["Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"]`,
		"second": `["WARNING: something else","Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"]`,
	} {
		t.Run(name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(`{"ServerErrors":`+serverErrors+`,"ClientInfo":{}}`, "", nil)
			info, err := NewClient(&m).Info(t.Context())
			assert.Nil(t, info)
			require.ErrorIs(t, err, ErrUnreachable)
			var unreachable *UnreachableError
			require.ErrorAs(t, err, &unreachable)
			assert.Equal(t, "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", unreachable.Reason)
			assert.EqualError(t, err, "Docker is not reachable: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"+DoctorHint)
		})
	}
}

func TestDaemonInfo_HasSecurityOption(t *testing.T) {
	info := &DaemonInfo{SecurityOptions: []string{"name=seccomp,profile=builtin", "name=rootless", "name=cgroupns"}}
	assert.True(t, info.HasSecurityOption("rootless"))
	assert.True(t, info.HasSecurityOption("seccomp"))
	assert.False(t, info.HasSecurityOption("userns"))
	assert.False(t, info.HasSecurityOption("profile"))
	assert.False(t, (&DaemonInfo{}).HasSecurityOption("rootless"))
}

// TestDaemonInfo_DockerDesktop checks the two marks of Docker Desktop's
// VM in `docker info`.
func TestDaemonInfo_DockerDesktop(t *testing.T) {
	assert.True(t, (&DaemonInfo{OperatingSystem: "Docker Desktop", Name: "docker-desktop"}).DockerDesktop())
	assert.True(t, (&DaemonInfo{OperatingSystem: "Docker Desktop"}).DockerDesktop())
	assert.True(t, (&DaemonInfo{Name: "docker-desktop"}).DockerDesktop())
	assert.False(t, (&DaemonInfo{OperatingSystem: "Ubuntu 24.04.3 LTS", Name: "build-host"}).DockerDesktop())
	assert.False(t, (&DaemonInfo{}).DockerDesktop())
}
