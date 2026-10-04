// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndpointLifecycle asks the docker CLI in integration mode, where the
// tests run next to the daemon: on its own machine, through a unix socket.
func TestEndpointLifecycle(t *testing.T) {
	c, rec := newTestClient(t)
	if !rec.IsIntegration() {
		t.Setenv("DOCKER_HOST", "")
		rec.AddResult(`{"Name":"default","Metadata":{},"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock","SkipTLSVerify":false}},`+
			`"TLSMaterial":{},"Storage":{"MetadataPath":"\u003cIN MEMORY\u003e","TLSPath":"\u003cIN MEMORY\u003e"}}`+"\n", "", nil)
	}

	ep, err := c.Endpoint(t.Context())
	require.NoError(t, err)
	assert.True(t, ep.UnixSocket(), ep.Host)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestEndpoint_DockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://build-host:2376")
	var m mock.Executor

	ep, err := NewClient(&m).Endpoint(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Endpoint{Host: "tcp://build-host:2376"}, ep)
	assert.False(t, ep.UnixSocket())
	assert.Empty(t, m.Calls, "DOCKER_HOST wins over any context")
}

func TestEndpoint_Context(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	var m mock.Executor
	m.AddResult(`{"Name":"build","Metadata":{"Description":"CI"},"Endpoints":{"docker":{"Host":"ssh://ci@build-host","SkipTLSVerify":false}}}`+"\n", "", nil)

	ep, err := NewClient(&m).Endpoint(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Endpoint{Host: "ssh://ci@build-host", Context: "build"}, ep)
	assert.False(t, ep.UnixSocket())
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"context", "inspect", "--format", "{{json .}}"}, m.Calls[0].Args,
		"no context name: the CLI resolves DOCKER_CONTEXT and the current context")
}

func TestEndpoint_Errors(t *testing.T) {
	tests := []struct {
		name    string
		result  mock.Result
		wantErr string
	}{
		{"unknown context", mock.Result{Stderr: "context \"gone\": context not found\n", Err: fmt.Errorf("exit status 1")},
			"inspecting the docker context: exit status 1"},
		{"not JSON", mock.Result{Stdout: "default unix:///var/run/docker.sock"}, "parsing docker context inspect"},
		{"no docker endpoint", mock.Result{Stdout: `{"Name":"k8s","Endpoints":{"kubernetes":{"Host":"https://k8s:6443"}}}`},
			`docker context "k8s" has no docker endpoint`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", "")
			var m mock.Executor
			m.AddResult(tt.result.Stdout, tt.result.Stderr, tt.result.Err)
			_, err := NewClient(&m).Endpoint(t.Context())
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestEndpoint_UnixSocket(t *testing.T) {
	assert.True(t, Endpoint{Host: "unix:///var/run/docker.sock"}.UnixSocket())
	assert.True(t, Endpoint{Host: "unix:///run/user/1000/docker.sock", Context: "rootless"}.UnixSocket())
	assert.False(t, Endpoint{Host: "tcp://127.0.0.1:2375"}.UnixSocket())
	assert.False(t, Endpoint{Host: "ssh://ci@build-host"}.UnixSocket())
	assert.False(t, Endpoint{Host: "/var/run/docker.sock"}.UnixSocket(), "the CLI takes a host without a scheme for TCP")
}
