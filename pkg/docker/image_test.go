// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
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
		rec.AddResult("[{}]\n", "", nil)                                          // exists busybox → true
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // exists nonexistent → false
		rec.AddResult("ephemeral-test\n", "", nil)                                // run ephemeral
	}

	// Exists → true (busybox is pulled by other tests).
	exists, err := c.ImageExists(ctx, "busybox:latest")
	require.NoError(t, err)
	assert.True(t, exists)

	// Exists → false.
	exists, err = c.ImageExists(ctx, "nonexistent-image:v999.999")
	require.NoError(t, err)
	assert.False(t, exists)

	// RunEphemeral.
	stdout, err := c.RunEphemeral(ctx, "busybox:latest", "echo", "ephemeral-test")
	require.NoError(t, err)
	assert.Equal(t, "ephemeral-test\n", stdout)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestImageExists_True(t *testing.T) {
	var m MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	exists, err := c.ImageExists(context.Background(), testImage)
	require.NoError(t, err)
	assert.True(t, exists)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"image", "inspect", testImage}, m.Calls[0].Args)
}

func TestImageExists_False(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: No such image: "+testImage+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.ImageExists(context.Background(), testImage)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestImageExists_OtherError(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	exists, err := c.ImageExists(context.Background(), testImage)
	assert.Error(t, err)
	assert.False(t, exists)
}

func TestRunEphemeral(t *testing.T) {
	var m MockExecutor
	m.AddResult("slurm 25.11.0\n", "", nil)
	c := NewClient(&m)

	stdout, err := c.RunEphemeral(context.Background(), testImage, "scontrol", "--version")
	require.NoError(t, err)
	assert.Equal(t, "slurm 25.11.0\n", stdout)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", testImage, "scontrol", "--version"}, m.Calls[0].Args)
}

func TestRunEphemeral_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Unable to find image\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	stdout, err := c.RunEphemeral(context.Background(), testImage, "scontrol", "--version")
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
