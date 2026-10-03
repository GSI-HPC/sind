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
	c, rec := newTestClient(t)
	if !rec.IsIntegration() {
		rec.AddResult(`{"ServerVersion":"29.1.0","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]}`, "", nil)
	}

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
		`"SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"],"ClientInfo":{"Version":"29.1.0"}}`+"\n", "", nil)
	c := NewClient(&m)

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &DaemonInfo{
		ServerVersion:   "29.1.0",
		CgroupVersion:   "2",
		SecurityOptions: []string{"name=seccomp,profile=builtin", "name=cgroupns"},
	}, info)
	assert.Equal(t, []string{"info", "--format", "{{json .}}"}, m.Calls[0].Args)
}

func TestInfo_Errors(t *testing.T) {
	tests := []struct {
		name    string
		result  mock.Result
		wantErr string
	}{
		{"exit status", mock.Result{Stderr: "permission denied\n", Err: fmt.Errorf("exit status 1")}, "exit status 1"},
		{"server errors", mock.Result{Stdout: `{"ServerErrors":["Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"],"ClientInfo":{}}`},
			"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"},
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

func TestDaemonInfo_HasSecurityOption(t *testing.T) {
	info := &DaemonInfo{SecurityOptions: []string{"name=seccomp,profile=builtin", "name=rootless", "name=cgroupns"}}
	assert.True(t, info.HasSecurityOption("rootless"))
	assert.True(t, info.HasSecurityOption("seccomp"))
	assert.False(t, info.HasSecurityOption("userns"))
	assert.False(t, info.HasSecurityOption("profile"))
	assert.False(t, (&DaemonInfo{}).HasSecurityOption("rootless"))
}
