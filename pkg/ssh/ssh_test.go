// SPDX-License-Identifier: LGPL-3.0-or-later

package ssh

import (
	"fmt"
	"os"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- InjectAndCollect Lifecycle ---

// TestInjectAndCollectLifecycle exercises InjectPublicKey and CollectHostKey
// in sequence on a container. Integration coverage for these functions is
// provided by TestClusterCreateDeleteLifecycle (full cluster with sshd).
func TestInjectAndCollectLifecycle(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                             // mkdir .ssh
	m.AddResult("", "", nil)                                             // append authorized_keys
	m.AddResult("# comment\nlocalhost ssh-ed25519 AAAA-test-hostkey\n", "", nil) // ssh-keyscan
	c := docker.NewClient(&m)
	name := docker.ContainerName("sind-dev-controller")

	// Inject public key.
	err := InjectPublicKey(t.Context(), c, name, "ssh-ed25519 AAAA-test-pubkey")
	require.NoError(t, err)

	// Collect host key.
	hostKey, err := CollectHostKey(t.Context(), c, name)
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAA-test-hostkey", hostKey)
}

// --- InjectPublicKey ---

func TestInjectPublicKey(t *testing.T) {
	var m docker.MockExecutor
	// Exec mkdir → success
	m.AddResult("", "", nil)
	// AppendFile → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := InjectPublicKey(t.Context(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...\n")
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"mkdir", "-p", "/root/.ssh",
	}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"exec", "-i", "sind-dev-controller",
		"sh", "-c", "cat >> " + authorizedKeysPath,
	}, m.Calls[1].Args)
	assert.Equal(t, "ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestInjectPublicKey_AddsNewline(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := InjectPublicKey(t.Context(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...")
	require.NoError(t, err)

	// Should append newline if missing.
	assert.Equal(t, "ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestInjectPublicKey_MkdirError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := InjectPublicKey(t.Context(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...\n")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating .ssh directory")
}

func TestInjectPublicKey_WriteError(t *testing.T) {
	var m docker.MockExecutor
	// Exec mkdir → success
	m.AddResult("", "", nil)
	// AppendFile → error
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := InjectPublicKey(t.Context(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...\n")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing authorized_keys")
}

// --- CollectHostKey ---

func TestCollectHostKey(t *testing.T) {
	var m docker.MockExecutor
	// ssh-keyscan output includes comments and the key line
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.6\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n", "", nil)
	c := docker.NewClient(&m)

	key, err := CollectHostKey(t.Context(), c, "sind-dev-controller")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"ssh-keyscan", "-t", "ed25519", "localhost",
	}, m.Calls[0].Args)
}

func TestCollectHostKey_NoKey(t *testing.T) {
	var m docker.MockExecutor
	// ssh-keyscan returns only comments (e.g. sshd not serving ed25519)
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.6\n", "", nil)
	c := docker.NewClient(&m)

	_, err := CollectHostKey(t.Context(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ed25519 host key found")
}

func TestCollectHostKey_MalformedLine(t *testing.T) {
	var m docker.MockExecutor
	// Non-comment line with no space (malformed, skipped)
	m.AddResult("malformed\n", "", nil)
	c := docker.NewClient(&m)

	_, err := CollectHostKey(t.Context(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ed25519 host key found")
}

func TestCollectHostKey_MalformedThenValid(t *testing.T) {
	var m docker.MockExecutor
	// Malformed line skipped, valid key returned from next line
	m.AddResult("malformed\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n", "", nil)
	c := docker.NewClient(&m)

	key, err := CollectHostKey(t.Context(), c, "sind-dev-controller")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)
}

func TestCollectHostKey_EmptyOutput(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, err := CollectHostKey(t.Context(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ed25519 host key found")
}

func TestCollectHostKey_ExecError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	_, err := CollectHostKey(t.Context(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning host key")
}

// --- GenerateSSHConfig ---

func TestGenerateSSHConfig(t *testing.T) {
	config := GenerateSSHConfig(docker.ContainerName("sind-ssh"), "/home/user/.sind")

	assert.Contains(t, config, "Host *.sind.local")
	assert.Contains(t, config, "ProxyCommand docker exec -i sind-ssh nc %h 22")
	assert.Contains(t, config, "IdentityFile /home/user/.sind/id_ed25519")
	assert.Contains(t, config, "UserKnownHostsFile /home/user/.sind/known_hosts")
	assert.Contains(t, config, "User root")
	assert.Contains(t, config, "StrictHostKeyChecking yes")
}

// --- ExportConfig ---

const testExportDir = "/home/user/.sind"

func exportDockerMock() (*docker.MockExecutor, *docker.Client) {
	var m docker.MockExecutor
	m.AddResult("PRIVATE-KEY-DATA", "", nil)
	m.AddResult("known-hosts-data\n", "", nil)
	return &m, docker.NewClient(&m)
}

func TestExportConfig(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("PRIVATE-KEY-DATA", "", nil)
	m.AddResult("host1 ssh-ed25519 AAAA...\n", "", nil)
	c := docker.NewClient(&m)

	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, docker.ContainerName("sind-ssh"))
	require.NoError(t, err)

	// Verify ssh_config was written with correct paths.
	sshConfig, err := afero.ReadFile(fs, testExportDir+"/ssh_config")
	require.NoError(t, err)
	assert.Contains(t, string(sshConfig), "IdentityFile "+testExportDir+"/id_ed25519")
	assert.Contains(t, string(sshConfig), "UserKnownHostsFile "+testExportDir+"/known_hosts")

	// Verify private key was written.
	privKey, err := afero.ReadFile(fs, testExportDir+"/id_ed25519")
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE-KEY-DATA", string(privKey))

	// Verify known_hosts was written.
	knownHosts, err := afero.ReadFile(fs, testExportDir+"/known_hosts")
	require.NoError(t, err)
	assert.Equal(t, "host1 ssh-ed25519 AAAA...\n", string(knownHosts))

	// Verify docker calls read from SSH container.
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/id_ed25519"}, m.Calls[0].Args)
	assert.Equal(t, []string{"exec", "sind-ssh", "cat", "/root/.ssh/known_hosts"}, m.Calls[1].Args)
}

func TestExportConfig_FilePermissions(t *testing.T) {
	_, c := exportDockerMock()
	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, docker.ContainerName("sind-ssh"))
	require.NoError(t, err)

	// Private key must be 0600 (owner read/write only).
	info, err := fs.Stat(testExportDir + "/id_ed25519")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// ssh_config and known_hosts are 0644.
	info, err = fs.Stat(testExportDir + "/ssh_config")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())

	info, err = fs.Stat(testExportDir + "/known_hosts")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestExportConfig_ReadPrivKeyError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading private key")
}

func TestExportConfig_ReadKnownHostsError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("PRIVATE-KEY-DATA", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading known_hosts")
}

func TestExportConfig_MkdirError(t *testing.T) {
	_, c := exportDockerMock()
	fs := afero.NewReadOnlyFs(afero.NewMemMapFs())

	err := ExportConfig(t.Context(), c, fs, testExportDir, docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating directory")
}

// errFs wraps an afero.Fs and fails OpenFile on a specific path.
type errFs struct {
	afero.Fs
	errOn string
}

func (f *errFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if name == f.errOn {
		return nil, fmt.Errorf("permission denied")
	}
	return f.Fs.OpenFile(name, flag, perm)
}

func TestExportConfig_WriteSSHConfigError(t *testing.T) {
	_, c := exportDockerMock()
	fs := &errFs{Fs: afero.NewMemMapFs(), errOn: testExportDir + "/ssh_config"}

	err := ExportConfig(t.Context(), c, fs, testExportDir, docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing ssh_config")
}

func TestExportConfig_WritePrivKeyError(t *testing.T) {
	_, c := exportDockerMock()
	fs := &errFs{Fs: afero.NewMemMapFs(), errOn: testExportDir + "/id_ed25519"}

	err := ExportConfig(t.Context(), c, fs, testExportDir, docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing id_ed25519")
}

func TestExportConfig_WriteKnownHostsError(t *testing.T) {
	_, c := exportDockerMock()
	fs := &errFs{Fs: afero.NewMemMapFs(), errOn: testExportDir + "/known_hosts"}

	err := ExportConfig(t.Context(), c, fs, testExportDir, docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing known_hosts")
}
