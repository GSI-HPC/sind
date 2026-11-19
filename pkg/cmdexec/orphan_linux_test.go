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
