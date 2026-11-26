// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrphanSignal(t *testing.T) {
	cmd := exec.Command("true")
	orphanSignal(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	assert.Equal(t, syscall.SIGINT, cmd.SysProcAttr.Pdeathsig)

	// It keeps what the process group set.
	grouped := exec.Command("true")
	grouped.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	orphanSignal(grouped)
	assert.True(t, grouped.SysProcAttr.Setpgid)
	assert.Equal(t, syscall.SIGINT, grouped.SysProcAttr.Pdeathsig)
}

func TestStartTied(t *testing.T) {
	cmd := exec.Command("true")
	release, err := startTied(cmd)
	require.NoError(t, err)
	assert.Equal(t, syscall.SIGKILL, cmd.SysProcAttr.Pdeathsig)
	require.NoError(t, cmd.Wait())
	release()
	release() // a second call does nothing

	// It keeps what the process group set, and reports a command that
	// does not start.
	missing := exec.Command("/nonexistent/sind-test-command")
	missing.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	release, err = startTied(missing)
	require.Error(t, err)
	assert.True(t, missing.SysProcAttr.Setpgid)
	assert.Equal(t, syscall.SIGKILL, missing.SysProcAttr.Pdeathsig)
	release()
}
