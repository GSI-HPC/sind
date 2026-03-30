// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitCode1 returns a *exec.ExitError with exit code 1 for mocking
// ContainerExists returning false.
func exitCode1(t *testing.T) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

// --- sindStateDir ---

func TestSindStateDir_XDGSet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	dir, err := sindStateDir("sind")
	require.NoError(t, err)
	assert.Equal(t, "/custom/state/sind/sind", dir)
}

func TestSindStateDir_XDGFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/user")
	dir, err := sindStateDir("sind")
	require.NoError(t, err)
	assert.Equal(t, "/home/user/.local/state/sind/sind", dir)
}

func TestSindStateDir_CustomRealm(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	dir, err := sindStateDir("ci-42")
	require.NoError(t, err)
	assert.Equal(t, "/custom/state/sind/ci-42", dir)
}

// --- syncSSHExport ---

func TestSyncSSHExport_ExportsWhenContainerExists(t *testing.T) {
	mock := &docker.MockExecutor{}
	// ContainerExists: docker container inspect sind-ssh → success
	mock.AddResult("[{}]", "", nil)
	// ExportConfig: ReadFile private key
	mock.AddResult("PRIVATE-KEY", "", nil)
	// ExportConfig: ReadFile known_hosts
	mock.AddResult("host1 ssh-ed25519 AAAA\n", "", nil)

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
	assert.Equal(t, "host1 ssh-ed25519 AAAA\n", string(data))

	data, err = afero.ReadFile(fs, filepath.Join(dir, "ssh_config"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Host *.sind.local")
	assert.Contains(t, string(data), "ProxyCommand docker exec -i sind-ssh")
}

func TestSyncSSHExport_CleansFilesWhenContainerGone(t *testing.T) {
	mock := &docker.MockExecutor{}
	// ContainerExists: not found
	mock.AddResult("", "Error: No such container", exitCode1(t))

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
	mock := &docker.MockExecutor{}
	mock.AddResult("", "Error: No such container", exitCode1(t))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")
	fs := afero.NewMemMapFs()

	// No directory or files — should not error.
	err := syncSSHExport(t.Context(), client, meshMgr, fs, "/state/sind/sind")
	require.NoError(t, err)
}

func TestSyncSSHExport_ContainerCheckError(t *testing.T) {
	mock := &docker.MockExecutor{}
	mock.AddResult("", "", fmt.Errorf("connection refused"))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")

	err := syncSSHExport(t.Context(), client, meshMgr, afero.NewMemMapFs(), "/state/sind/sind")
	assert.Error(t, err)
}

func TestSyncSSHExport_ExportError(t *testing.T) {
	mock := &docker.MockExecutor{}
	mock.AddResult("[{}]", "", nil)
	mock.AddResult("", "Error", fmt.Errorf("exec failed"))

	client := docker.NewClient(mock)
	meshMgr := mesh.NewManager(client, "sind")

	err := syncSSHExport(t.Context(), client, meshMgr, afero.NewMemMapFs(), "/state/sind/sind")
	assert.Error(t, err)
}
