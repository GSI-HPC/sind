// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- updateSSHExport ---

// TestUpdateSSHExport_UsesContextFs checks that the export after a command
// writes to the file system of the command's context (fsFrom).
func TestUpdateSSHExport_UsesContextFs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	m := &mock.Executor{}
	m.AddResult("PRIVATE-KEY\x00controller.default.sind.sind ssh-ed25519 AAAA\n", "", nil)
	client := docker.NewClient(m)
	fs := afero.NewMemMapFs()
	var stderr strings.Builder
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)
	cmd.SetContext(withFs(t.Context(), fs))

	updateSSHExport(cmd, client, mesh.NewManager(client, "sind"))
	assert.Empty(t, stderr.String())
	data, err := afero.ReadFile(fs, "/xdg/state/sind/sind/id_ed25519")
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE-KEY", string(data))
}
