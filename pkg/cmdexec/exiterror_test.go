// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec_test

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitError returns the *exec.ExitError of a process that exited with code.
func exitError(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

func TestWrapExitError_CarriesStderr(t *testing.T) {
	inner := exitError(t, 1)
	err := cmdexec.WrapExitError(inner, "Error response from daemon: No such container: x\n")

	var exitErr *cmdexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.Equal(t, "Error response from daemon: No such container: x\n", exitErr.Stderr)
	assert.Equal(t, "exit status 1: Error response from daemon: No such container: x", err.Error())
	assert.ErrorIs(t, err, inner, "the *exec.ExitError stays reachable")
}

func TestWrapExitError_EmptyStderr(t *testing.T) {
	err := cmdexec.WrapExitError(exitError(t, 2), " \n")
	assert.Equal(t, "exit status 2", err.Error())
}

func TestWrapExitError_OtherErrorsUnchanged(t *testing.T) {
	assert.NoError(t, cmdexec.WrapExitError(nil, "ignored"))

	other := errors.New("exec: \"docker\": executable file not found in $PATH")
	assert.Same(t, other, cmdexec.WrapExitError(other, "ignored"))

	wrapped := cmdexec.WrapExitError(exitError(t, 1), "first")
	assert.Same(t, wrapped, cmdexec.WrapExitError(wrapped, "second"), "an ExitError is not wrapped twice")
}
