// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- syncSSHExport ---

func TestSyncSSHExport_ExportsWhenContainerExists(t *testing.T) {
	mock := &mock.Executor{}
	// ExportConfig: one exec prints the private key and known_hosts
	mock.AddResult("PRIVATE-KEY\x00controller.default.sind.sind ssh-ed25519 AAAA\n", "", nil)

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")
	fs := afero.NewMemMapFs()
	dir := "/state/sind/sind"

	err := syncSSHExport(t.Context(), client, meshMgr, fs, dir)
	require.NoError(t, err)

	// Verify files were written.
	data, err := afero.ReadFile(fs, filepath.Join(dir, "id_ed25519"))
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE-KEY", string(data))

	data, err = afero.ReadFile(fs, filepath.Join(dir, "known_hosts"))
	require.NoError(t, err)
	assert.Equal(t, "controller.default.sind.sind ssh-ed25519 AAAA\n", string(data))

	data, err = afero.ReadFile(fs, filepath.Join(dir, "ssh_config"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Host *.sind.sind")
	assert.Contains(t, string(data), "Host controller.default.sind.sind\n    ProxyCommand docker exec -i sind-ssh ")

	// The export's exec is the only docker call: no separate existence
	// probe of the relay container.
	require.Len(t, mock.Calls, 1)
	assert.Equal(t, []string{"exec", "sind-ssh"}, mock.Calls[0].Args[:2])
}

func TestSyncSSHExport_CleansFilesWhenContainerGone(t *testing.T) {
	mock := &mock.Executor{}
	// ExportConfig: the relay container is gone
	mock.AddResult("", testutil.NoSuchContainer("sind-ssh"), testutil.ExitCode1(t))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")
	fs := afero.NewMemMapFs()
	dir := "/state/sind/sind"

	// Pre-populate files.
	require.NoError(t, fs.MkdirAll(dir, 0700))
	require.NoError(t, afero.WriteFile(fs, filepath.Join(dir, "ssh_config"), []byte("old"), 0644))
	require.NoError(t, afero.WriteFile(fs, filepath.Join(dir, "id_ed25519"), []byte("key"), 0600))
	require.NoError(t, afero.WriteFile(fs, filepath.Join(dir, "known_hosts"), []byte("hosts"), 0644))

	err := syncSSHExport(t.Context(), client, meshMgr, fs, dir)
	require.NoError(t, err)

	// Files should be removed.
	for _, name := range []string{"ssh_config", "id_ed25519", "known_hosts"} {
		exists, err := afero.Exists(fs, filepath.Join(dir, name))
		require.NoError(t, err)
		assert.False(t, exists, "%s should be removed", name)
	}

	// Empty realm directory should be removed.
	exists, err := afero.DirExists(fs, dir)
	require.NoError(t, err)
	assert.False(t, exists, "empty realm directory should be removed")
}

func TestSyncSSHExport_NoFilesToClean(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult("", testutil.NoSuchContainer("sind-ssh"), testutil.ExitCode1(t))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")
	fs := afero.NewMemMapFs()

	// No directory or files — should not error.
	err := syncSSHExport(t.Context(), client, meshMgr, fs, "/state/sind/sind")
	require.NoError(t, err)
}

func TestSyncSSHExport_DockerError(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult("", "", fmt.Errorf("connection refused"))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")

	err := syncSSHExport(t.Context(), client, meshMgr, afero.NewMemMapFs(), "/state/sind/sind")
	assert.Error(t, err)
}

// TestSyncSSHExport_ExportError checks that a failing export of a relay
// container that exists, such as a stopped one, is an error and keeps the
// files.
func TestSyncSSHExport_ExportError(t *testing.T) {
	mock := &mock.Executor{}
	mock.AddResult("", "Error response from daemon: container sind-ssh is not running", testutil.ExitCode1(t))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")
	fs := afero.NewMemMapFs()
	dir := "/state/sind/sind"
	require.NoError(t, afero.WriteFile(fs, filepath.Join(dir, "ssh_config"), []byte("old"), 0644))

	err := syncSSHExport(t.Context(), client, meshMgr, fs, dir)
	require.ErrorContains(t, err, "is not running")
	exists, err := afero.Exists(fs, filepath.Join(dir, "ssh_config"))
	require.NoError(t, err)
	assert.True(t, exists)
}
