// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testImage = "ghcr.io/gsi-hpc/sind-node:25.11"

func TestImageLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()

	if !rec.IsIntegration() {
		rec.AddResult("ephemeral-test\n", "", nil) // run ephemeral
	}

	stdout, err := c.RunEphemeral(ctx, "busybox:latest", "echo", "ephemeral-test")
	require.NoError(t, err)
	assert.Equal(t, "ephemeral-test\n", stdout)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestImageLabels(t *testing.T) {
	var m mock.Executor
	m.AddResult(`{"org.opencontainers.image.title":"sind-node"}`+"\n", "", nil)
	c := NewClient(&m)

	labels, exists, err := c.ImageLabels(t.Context(), testImage)

	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, Labels{"org.opencontainers.image.title": "sind-node"}, labels)
	assert.Equal(t, []string{"image", "inspect", testImage, "--format", "{{json .Config.Labels}}"}, m.Calls[0].Args)
}

func TestImageLabels_NotFound(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such image: "+testImage+"\n", &exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	labels, exists, err := c.ImageLabels(t.Context(), testImage)

	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, labels)
}

func TestImageExists(t *testing.T) {
	var m mock.Executor
	m.AddResult("sha256:abc\n", "", nil)
	m.AddResult("", "Error: No such image: "+testImage+"\n", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("", "Cannot connect to the Docker daemon\n", &exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.ImageExists(t.Context(), testImage)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, []string{"image", "inspect", "--format", "{{.Id}}", testImage}, m.Calls[0].Args)

	exists, err = c.ImageExists(t.Context(), testImage)
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = c.ImageExists(t.Context(), testImage)
	require.ErrorContains(t, err, "Cannot connect to the Docker daemon")
}

func TestServerVersion(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.3.0\n", "", nil)
	c := NewClient(&m)

	v, err := c.ServerVersion(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "29.3.0", v)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"version", "--format", "{{.Server.Version}}"}, m.Calls[0].Args)
}

func TestServerVersion_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	_, err := c.ServerVersion(t.Context())
	assert.Error(t, err)
}

func TestRunEphemeral(t *testing.T) {
	var m mock.Executor
	m.AddResult("slurm 25.11.0\n", "", nil)
	c := NewClient(&m)

	stdout, err := c.RunEphemeral(t.Context(), testImage, "scontrol", "--version")
	require.NoError(t, err)
	assert.Equal(t, "slurm 25.11.0\n", stdout)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", testImage, "scontrol", "--version"}, m.Calls[0].Args)
}

func TestPullImage(t *testing.T) {
	var m mock.Executor
	m.AddResult("ghcr.io/gsi-hpc/sind-node:latest\n", "", nil)
	m.AddResult("", "Error response from daemon: manifest unknown\n", exitError(t, 1, "Error response from daemon: manifest unknown\n"))
	c := NewClient(&m)

	require.NoError(t, c.PullImage(t.Context(), "ghcr.io/gsi-hpc/sind-node:latest"))
	assert.Equal(t, []string{"pull", "--quiet", "ghcr.io/gsi-hpc/sind-node:latest"}, m.Calls[0].Args)

	err := c.PullImage(t.Context(), "ghcr.io/gsi-hpc/sind-node:nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest unknown")
}

func TestRunEphemeralWith(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	_, err := c.RunEphemeralWith(t.Context(), []string{"--mount", "type=bind,source=/x,target=/x"}, testImage, "true")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", "--mount", "type=bind,source=/x,target=/x", testImage, "true"}, m.Calls[0].Args)
}

func TestRunEphemeral_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Unable to find image\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	stdout, err := c.RunEphemeral(t.Context(), testImage, "scontrol", "--version")
	assert.Error(t, err)
	assert.Empty(t, stdout)
}

// exitCode1 runs a command that exits with code 1 and returns its ProcessState.
func exitCode1(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ProcessState
}
