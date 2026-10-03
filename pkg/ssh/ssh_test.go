// SPDX-License-Identifier: LGPL-3.0-or-later

package ssh

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- InjectKeyAndCollectHostKey ---

// Integration coverage is provided by TestClusterCreateDeleteLifecycle (full
// cluster with sshd).

func TestInjectKeyAndCollectHostKey(t *testing.T) {
	var m mock.Executor
	// ssh-keyscan output includes comments and the key line
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.6\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n", "", nil)
	c := docker.NewClient(&m)

	key, err := InjectKeyAndCollectHostKey(t.Context(), c, "sind-dev-controller", "ssh-ed25519 AAAA-test-pubkey\n")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)

	// One docker exec, with the key as an argument of the shell.
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"sh", "-c", `mkdir -p /root/.ssh && printf '%s\n' "$1" >> /root/.ssh/authorized_keys && ssh-keyscan -t ed25519 localhost`,
		"sh", "ssh-ed25519 AAAA-test-pubkey",
	}, m.Calls[0].Args)
}

func TestInjectKeyAndCollectHostKey_KeyWithoutNewline(t *testing.T) {
	var m mock.Executor
	m.AddResult("localhost ssh-ed25519 AAAA-hostkey\n", "", nil)
	c := docker.NewClient(&m)

	_, err := InjectKeyAndCollectHostKey(t.Context(), c, "sind-dev-controller", "ssh-ed25519 AAAA...")
	require.NoError(t, err)

	// The script adds the newline.
	assert.Equal(t, "ssh-ed25519 AAAA...", m.Calls[0].Args[len(m.Calls[0].Args)-1])
}

func TestInjectKeyAndCollectHostKey_ExecError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	_, err := InjectKeyAndCollectHostKey(t.Context(), c, "sind-dev-controller", "ssh-ed25519 AAAA...\n")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "injecting SSH key and scanning host key")
}

func TestInjectKeyAndCollectHostKey_NoKey(t *testing.T) {
	for name, stdout := range map[string]string{
		// ssh-keyscan returns only comments (e.g. sshd not serving ed25519)
		"only comments": "# localhost:22 SSH-2.0-OpenSSH_9.6\n",
		// Non-comment line with no space (malformed, skipped)
		"malformed line": "malformed\n",
		"empty output":   "",
	} {
		t.Run(name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(stdout, "", nil)
			c := docker.NewClient(&m)

			_, err := InjectKeyAndCollectHostKey(t.Context(), c, "sind-dev-controller", "ssh-ed25519 AAAA...\n")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "no ed25519 host key found")
		})
	}
}

func TestInjectKeyAndCollectHostKey_MalformedThenValid(t *testing.T) {
	var m mock.Executor
	// Malformed line skipped, valid key returned from next line
	m.AddResult("malformed\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n", "", nil)
	c := docker.NewClient(&m)

	key, err := InjectKeyAndCollectHostKey(t.Context(), c, "sind-dev-controller", "ssh-ed25519 AAAA...\n")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)
}

// --- GenerateSSHConfig ---

func TestGenerateSSHConfig_DefaultRealm(t *testing.T) {
	config := GenerateSSHConfig(docker.ContainerName("sind-ssh"), "/home/user/.sind", "sind",
		[]string{"controller.default.sind.sind", "worker-0.dev.sind.sind"})

	assert.Equal(t, `Host controller controller.* controller-backup controller-backup.* db db.* submitter submitter.* worker-*
    CanonicalizeHostname yes
    CanonicalDomains default.sind.sind sind.sind

Host *.sind.sind
    IdentityFile /home/user/.sind/id_ed25519
    UserKnownHostsFile /home/user/.sind/known_hosts
    User root
    StrictHostKeyChecking yes

Host controller.default.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/controller.default.sind.sind/22; cat <&3 & cat >&3; kill $!'

Host worker-0.dev.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/worker-0.dev.sind.sind/22; cat <&3 & cat >&3; kill $!'
`, config)
}

func TestGenerateSSHConfig_NamedRealm(t *testing.T) {
	config := GenerateSSHConfig(docker.ContainerName("ci42-ssh"), "/home/user/.sind", "ci42",
		[]string{"controller.dev.ci42.sind"})

	assert.Equal(t, `Host *.ci42.sind
    IdentityFile /home/user/.sind/id_ed25519
    UserKnownHostsFile /home/user/.sind/known_hosts
    User root
    StrictHostKeyChecking yes

Host controller.dev.ci42.sind
    ProxyCommand docker exec -i ci42-ssh bash -c 'exec 3<>/dev/tcp/controller.dev.ci42.sind/22; cat <&3 & cat >&3; kill $!'
`, config)
}

func TestGenerateSSHConfig_NoNodes(t *testing.T) {
	config := GenerateSSHConfig(docker.ContainerName("ci42-ssh"), "/home/user/.sind", "ci42", nil)

	assert.Contains(t, config, "Host *.ci42.sind\n")
	assert.NotContains(t, config, "ProxyCommand")
}

// TestGenerateSSHConfig_OnlyNodeNames checks that only node DNS names of
// the realm get a ProxyCommand, once each, and that ssh's %h appears in
// none: ssh puts it into the command unquoted, so a host name such as one
// from a git submodule URL ran as a command in the user's shell and in the
// relay's bash (CVE-2023-51385).
func TestGenerateSSHConfig_OnlyNodeNames(t *testing.T) {
	config := GenerateSSHConfig(docker.ContainerName("sind-ssh"), "/home/user/.sind", "sind", []string{
		"worker-0.dev.sind.sind",
		"worker-0.dev.sind.sind",
		"x'$(touch p)'.sind.sind",
		"x$(touch\tp).dev.sind.sind",
		"a.b.dev.sind.sind",
		"dev.sind.sind",
		"controller.dev.ci.sind",
		"Controller.dev.sind.sind",
		"-o.dev.sind.sind",
		"controller..sind.sind",
		"[controller.dev.sind.sind]:22",
		"|1|aGFzaA==|aGFzaA==",
		"",
	})

	assert.Equal(t, 1, strings.Count(config, "ProxyCommand"), config)
	assert.Contains(t, config, "\nHost worker-0.dev.sind.sind\n")
	assert.NotContains(t, config, "%h")
	assert.NotContains(t, config, "touch")
}

func TestKnownHostNames(t *testing.T) {
	names := knownHostNames("# comment\n\ncontroller.dev.sind.sind ssh-ed25519 AAAA\n" +
		"worker-0.dev.sind.sind,10.0.0.3 ssh-ed25519 BBBB\n  \n")

	assert.Equal(t, []string{"#", "controller.dev.sind.sind", "worker-0.dev.sind.sind", "10.0.0.3"}, names)
}

// --- ExportConfig ---

const testExportDir = "/home/user/.sind"

func exportDockerMock() (*mock.Executor, *docker.Client) {
	var m mock.Executor
	m.AddResult("PRIVATE-KEY-DATA\x00known-hosts-data\n", "", nil)
	return &m, docker.NewClient(&m)
}

func TestExportConfig(t *testing.T) {
	var m mock.Executor
	m.AddResult("PRIVATE-KEY-DATA\x00controller.dev.sind.sind ssh-ed25519 AAAA...\n", "", nil)
	c := docker.NewClient(&m)

	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
	require.NoError(t, err)

	// Verify ssh_config was written with correct paths, and with a
	// ProxyCommand for the node in known_hosts.
	sshConfig, err := afero.ReadFile(fs, testExportDir+"/ssh_config")
	require.NoError(t, err)
	assert.Contains(t, string(sshConfig), "IdentityFile "+testExportDir+"/id_ed25519")
	assert.Contains(t, string(sshConfig), "UserKnownHostsFile "+testExportDir+"/known_hosts")
	assert.Contains(t, string(sshConfig), "\nHost controller.dev.sind.sind\n    ProxyCommand docker exec -i sind-ssh ")

	// Verify private key was written.
	privKey, err := afero.ReadFile(fs, testExportDir+"/id_ed25519")
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE-KEY-DATA", string(privKey))

	// Verify known_hosts was written.
	knownHosts, err := afero.ReadFile(fs, testExportDir+"/known_hosts")
	require.NoError(t, err)
	assert.Equal(t, "controller.dev.sind.sind ssh-ed25519 AAAA...\n", string(knownHosts))

	// Verify one docker exec read both files from the SSH container.
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"exec", "sind-ssh",
		"sh", "-c", `cat /root/.ssh/id_ed25519 && printf '\0' && cat /root/.ssh/known_hosts`,
	}, m.Calls[0].Args)
}

func TestExportConfig_EmptyKnownHosts(t *testing.T) {
	var m mock.Executor
	m.AddResult("PRIVATE-KEY-DATA\x00", "", nil)
	c := docker.NewClient(&m)
	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
	require.NoError(t, err)

	knownHosts, err := afero.ReadFile(fs, testExportDir+"/known_hosts")
	require.NoError(t, err)
	assert.Empty(t, knownHosts)
}

func TestExportConfig_FilePermissions(t *testing.T) {
	_, c := exportDockerMock()
	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
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

func TestExportConfig_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading private key and known_hosts")
	assert.False(t, docker.IsNotFound(err))

	// Nothing is written.
	exists, err := afero.DirExists(fs, testExportDir)
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestExportConfig_NoRelay checks that the error for a relay container that
// does not exist is one docker.IsNotFound recognises, which the caller
// takes for a realm without clusters.
func TestExportConfig_NoRelay(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-ssh"), testutil.ExitCode1(t))
	c := docker.NewClient(&m)

	err := ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, "sind", docker.ContainerName("sind-ssh"))
	require.Error(t, err)
	assert.True(t, docker.IsNotFound(err))
}

func TestExportConfig_NoSeparator(t *testing.T) {
	var m mock.Executor
	m.AddResult("PRIVATE-KEY-DATA", "", nil)
	c := docker.NewClient(&m)

	err := ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, "sind", docker.ContainerName("sind-ssh"))
	assert.EqualError(t, err, "reading private key and known_hosts: sind-ssh printed no separator")
}

func TestExportConfig_MkdirError(t *testing.T) {
	_, c := exportDockerMock()
	fs := afero.NewReadOnlyFs(afero.NewMemMapFs())

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
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

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing ssh_config")
}

func TestExportConfig_WritePrivKeyError(t *testing.T) {
	_, c := exportDockerMock()
	fs := &errFs{Fs: afero.NewMemMapFs(), errOn: testExportDir + "/id_ed25519"}

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing id_ed25519")
}

func TestExportConfig_WriteKnownHostsError(t *testing.T) {
	_, c := exportDockerMock()
	fs := &errFs{Fs: afero.NewMemMapFs(), errOn: testExportDir + "/known_hosts"}

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind", docker.ContainerName("sind-ssh"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing known_hosts")
}
