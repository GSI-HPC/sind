// SPDX-License-Identifier: LGPL-3.0-or-later

// Package testutil provides shared test helpers used across sind packages.
package testutil

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// ExitCode1 returns an *exec.ExitError with exit code 1.
// This is used to mock Docker inspect commands that return exit code 1
// for missing resources.
func ExitCode1(t *testing.T) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}
