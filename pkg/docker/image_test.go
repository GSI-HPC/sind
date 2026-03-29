// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

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

func TestServerVersion(t *testing.T) {
	var m MockExecutor
	m.AddResult("29.3.0\n", "", nil)
	c := NewClient(&m)

	v, err := c.ServerVersion(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "29.3.0", v)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"version", "--format", "{{.Server.Version}}"}, m.Calls[0].Args)
}

func TestServerVersion_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Cannot connect", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	_, err := c.ServerVersion(t.Context())
	assert.Error(t, err)
}

func TestRunEphemeral(t *testing.T) {
	var m MockExecutor
	m.AddResult("slurm 25.11.0\n", "", nil)
	c := NewClient(&m)

	stdout, err := c.RunEphemeral(t.Context(), testImage, "scontrol", "--version")
	require.NoError(t, err)
	assert.Equal(t, "slurm 25.11.0\n", stdout)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", testImage, "scontrol", "--version"}, m.Calls[0].Args)
}

func TestRunEphemeral_Error(t *testing.T) {
	var m MockExecutor
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
