// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		out    string
		wantOK bool
	}{
		{"HTTP/1.1 200 OK\r\n", true},
		{"HTTP/1.0 204 No Content\r\n", true},
		{"HTTP/1.1 303 See Other\r\n", false},
		{"HTTP/1.1 404 Not Found\r\n", false},
		{"garbage\n", false},
		{"", false},
	}
	for _, tt := range tests {
		var m mock.Executor
		m.AddResult(tt.out, "", nil)
		c := docker.NewClient(&m)

		line, ok, err := HTTPStatus(t.Context(), c, testContainer, "6817", "/livez")
		require.NoError(t, err)
		assert.Equal(t, tt.wantOK, ok, tt.out)
		assert.NotContains(t, line, "\r")
		assert.Equal(t, []string{"exec", string(testContainer), "bash", "-c", httpStatusScript, "bash", "6817", "/livez"}, m.Calls[0].Args)
	}
}

func TestSlurmctldHTTP(t *testing.T) {
	var m mock.Executor
	m.AddResult("HTTP/1.1 200 OK\r\n", "", nil)
	c := docker.NewClient(&m)
	require.NoError(t, SlurmctldHTTP("/metrics/nodes")(t.Context(), c, testContainer))
}

func TestSlurmctldHTTP_NotServed(t *testing.T) {
	var m mock.Executor
	m.AddResult("HTTP/1.1 404 Not Found\r\n", "", nil)
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmctldHTTP("/metrics/nodes")(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Equal(t, `slurmctld serves no HTTP on port 6817 (got "HTTP/1.1 404 Not Found" for GET /metrics/nodes); check CommunicationParameters, or raise SlurmctldDebug to see why the http_parser plugin did not load`, err.Error())

	err = SlurmctldHTTP("/livez")(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmctld serves no HTTP on port 6817 (exit status 1 for GET /livez)")
	var te *TerminalError
	assert.NotErrorAs(t, err, &te, "slurmctld may still be reconfiguring")
}
