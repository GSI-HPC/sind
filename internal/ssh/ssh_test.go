// SPDX-License-Identifier: LGPL-3.0-or-later

package ssh

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- InjectKeyScript, HostKey ---

// Integration coverage is provided by TestClusterCreateDeleteLifecycle (full
// cluster with sshd), whose nodes run InjectKeyScript in their setup.

func TestInjectKeyScript(t *testing.T) {
	// One command per line, so that sh -e stops at the first that fails;
	// the key is the shell's argument $1.
	assert.Equal(t, []string{
		"mkdir -p /root/.ssh",
		`printf '%s\n' "$1" >> /root/.ssh/authorized_keys`,
		"ssh-keyscan -t ed25519 localhost",
	}, strings.Split(InjectKeyScript, "\n"))
}

func TestHostKey(t *testing.T) {
	// ssh-keyscan output includes comments and the key line
	key, err := HostKey("# localhost:22 SSH-2.0-OpenSSH_9.6\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)
}

func TestHostKey_NoKey(t *testing.T) {
	for name, stdout := range map[string]string{
		// ssh-keyscan returns only comments (e.g. sshd not serving ed25519)
		"only comments": "# localhost:22 SSH-2.0-OpenSSH_9.6\n",
		// Non-comment line with no space (malformed, skipped)
		"malformed line": "malformed\n",
		"empty output":   "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := HostKey(stdout)
			assert.EqualError(t, err, "no ed25519 host key found")
		})
	}
}

func TestHostKey_MalformedThenValid(t *testing.T) {
	// Malformed line skipped, valid key returned from next line
	key, err := HostKey("malformed\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)
}

func TestHostKeyFromFile(t *testing.T) {
	key, err := HostKeyFromFile("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest root@worker-0\n")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)

	for name, content := range map[string]string{
		"empty":     "",
		"no key":    "ssh-ed25519\n",
		"other key": "ssh-rsa AAAAB3NzaC1yc2E root@worker-0\n",
	} {
		_, err := HostKeyFromFile(content)
		assert.EqualError(t, err, "no ed25519 host key found", name)
	}
}

// --- GenerateSSHConfig ---

func TestGenerateSSHConfig_DefaultRealm(t *testing.T) {
	config := GenerateSSHConfig("/home/user/.sind", "sind",
		[]string{"controller.default.sind.sind", "worker-0.dev.sind.sind"})

	assert.Equal(t, `Host controller controller.* controller-backup controller-backup.* db db.* api api.* submitter submitter.* worker-*
    CanonicalizeHostname yes
    CanonicalDomains default.sind.sind sind.sind

Host *.sind.sind
    IdentitiesOnly yes
    IdentityFile "/home/user/.sind/id_ed25519"
    UserKnownHostsFile "/home/user/.sind/known_hosts"
    User root
    StrictHostKeyChecking yes

Host controller.default.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/controller.default.sind.sind/22; cat <&3 & cat >&3; kill $!'

Host worker-0.dev.sind.sind
    ProxyCommand docker exec -i sind-ssh bash -c 'exec 3<>/dev/tcp/worker-0.dev.sind.sind/22; cat <&3 & cat >&3; kill $!'
`, config)
}

func TestGenerateSSHConfig_NamedRealm(t *testing.T) {
	config := GenerateSSHConfig("/home/user/.sind", "ci42",
		[]string{"controller.dev.ci42.sind"})

	assert.Equal(t, `Host *.ci42.sind
    IdentitiesOnly yes
    IdentityFile "/home/user/.sind/id_ed25519"
    UserKnownHostsFile "/home/user/.sind/known_hosts"
    User root
    StrictHostKeyChecking yes

Host controller.dev.ci42.sind
    ProxyCommand docker exec -i ci42-ssh bash -c 'exec 3<>/dev/tcp/controller.dev.ci42.sind/22; cat <&3 & cat >&3; kill $!'
`, config)
}

func TestGenerateSSHConfig_NoNodes(t *testing.T) {
	config := GenerateSSHConfig("/home/user/.sind", "ci42", nil)

	assert.Contains(t, config, "Host *.ci42.sind\n")
	assert.NotContains(t, config, "ProxyCommand")
}

// TestGenerateSSHConfig_QuotedPaths checks that a state directory with a
// space, a % token, a double quote or a backslash in it stays one argument
// of each option, with ssh's token expansion and quoting escaped.
func TestGenerateSSHConfig_QuotedPaths(t *testing.T) {
	config := GenerateSSHConfig(`/home/John Doe/50%d/a"b\c`, "ci42", nil)

	assert.Contains(t, config, `
    IdentityFile "/home/John Doe/50%%d/a\"b\\c/id_ed25519"
    UserKnownHostsFile "/home/John Doe/50%%d/a\"b\\c/known_hosts"
`)
}

// TestGenerateSSHConfig_OnlyNodeNames checks that only node DNS names of
// the realm get a ProxyCommand, once each, and that ssh's %h appears in
// none: ssh puts it into the command unquoted, so a host name such as one
// from a git submodule URL ran as a command in the user's shell and in the
// relay's bash (CVE-2023-51385).
func TestGenerateSSHConfig_OnlyNodeNames(t *testing.T) {
	config := GenerateSSHConfig("/home/user/.sind", "sind", []string{
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

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind")
	require.NoError(t, err)

	// Verify ssh_config was written with correct paths, and with a
	// ProxyCommand for the node in known_hosts.
	sshConfig, err := afero.ReadFile(fs, testExportDir+"/ssh_config")
	require.NoError(t, err)
	assert.Contains(t, string(sshConfig), `IdentityFile "`+testExportDir+`/id_ed25519"`)
	assert.Contains(t, string(sshConfig), `UserKnownHostsFile "`+testExportDir+`/known_hosts"`)
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

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind")
	require.NoError(t, err)

	knownHosts, err := afero.ReadFile(fs, testExportDir+"/known_hosts")
	require.NoError(t, err)
	assert.Empty(t, knownHosts)
}

func TestExportConfig_FilePermissions(t *testing.T) {
	_, c := exportDockerMock()
	fs := afero.NewMemMapFs()

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind")
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

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind")
	assert.ErrorContains(t, err, "reading private key and known_hosts")
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

	err := ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, "sind")
	require.Error(t, err)
	assert.True(t, docker.IsNotFound(err))
}

func TestExportConfig_NoSeparator(t *testing.T) {
	var m mock.Executor
	m.AddResult("PRIVATE-KEY-DATA", "", nil)
	c := docker.NewClient(&m)

	err := ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, "sind")
	assert.EqualError(t, err, "reading private key and known_hosts: sind-ssh printed no separator")
}

func TestExportConfig_MkdirError(t *testing.T) {
	_, c := exportDockerMock()
	fs := afero.NewReadOnlyFs(afero.NewMemMapFs())

	err := ExportConfig(t.Context(), c, fs, testExportDir, "sind")
	assert.ErrorContains(t, err, "creating directory")
}

// errFs wraps an afero.Fs and fails one operation, op, on the files whose
// path contains errOn, and records the targets of its renames.
type errFs struct {
	afero.Fs
	op, errOn string
	renamed   []string
}

var errInjected = errors.New("permission denied")

func (f *errFs) fails(op, name string) bool {
	return f.op == op && strings.Contains(name, f.errOn)
}

func (f *errFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if f.fails("open", name) {
		return nil, errInjected
	}
	file, err := f.Fs.OpenFile(name, flag, perm)
	if err == nil && f.fails("write", name) {
		return failingFile{file}, nil
	}
	return file, err
}

func (f *errFs) Chmod(name string, mode os.FileMode) error {
	if f.fails("chmod", name) {
		return errInjected
	}
	return f.Fs.Chmod(name, mode)
}

func (f *errFs) Rename(oldname, newname string) error {
	if f.fails("rename", newname) {
		return errInjected
	}
	f.renamed = append(f.renamed, filepath.Base(newname))
	return f.Fs.Rename(oldname, newname)
}

func (f *errFs) Remove(name string) error {
	if f.fails("remove", name) {
		return errInjected
	}
	return f.Fs.Remove(name)
}

// failingFile is a file whose writes fail.
type failingFile struct{ afero.File }

func (failingFile) Write([]byte) (int, error) { return 0, errInjected }

// TestExportConfig_WriteErrors checks that a file that cannot be written
// fails the export, keeps the file it would replace, and leaves no
// temporary file behind.
func TestExportConfig_WriteErrors(t *testing.T) {
	for _, name := range ExportFiles() {
		for _, op := range []string{"open", "write", "chmod", "rename"} {
			t.Run(name+"/"+op, func(t *testing.T) {
				_, c := exportDockerMock()
				base := afero.NewMemMapFs()
				require.NoError(t, afero.WriteFile(base, testExportDir+"/"+name, []byte("old"), 0644))
				fs := &errFs{Fs: base, op: op, errOn: name}

				err := ExportConfig(t.Context(), c, fs, testExportDir, "sind")
				require.ErrorIs(t, err, errInjected)
				assert.ErrorContains(t, err, "writing "+name)

				data, err := afero.ReadFile(base, testExportDir+"/"+name)
				require.NoError(t, err)
				assert.Equal(t, "old", string(data), "the old file stays whole")
				entries, err := afero.ReadDir(base, testExportDir)
				require.NoError(t, err)
				for _, e := range entries {
					assert.False(t, strings.HasPrefix(e.Name(), "."), "temporary file %s left behind", e.Name())
				}
			})
		}
	}
}

// TestExportConfig_ReplacesInOrder checks that the export renames each file
// into place, the key and known_hosts before the ssh_config that names
// their hosts, and replaces files that exist.
func TestExportConfig_ReplacesInOrder(t *testing.T) {
	_, c := exportDockerMock()
	base := afero.NewMemMapFs()
	for _, name := range ExportFiles() {
		require.NoError(t, afero.WriteFile(base, testExportDir+"/"+name, []byte("old"), 0600))
	}
	fs := &errFs{Fs: base}

	require.NoError(t, ExportConfig(t.Context(), c, fs, testExportDir, "sind"))
	assert.Equal(t, []string{"id_ed25519", "known_hosts", "ssh_config"}, fs.renamed)
	data, err := afero.ReadFile(base, testExportDir+"/known_hosts")
	require.NoError(t, err)
	assert.Equal(t, "known-hosts-data\n", string(data))
	info, err := base.Stat(testExportDir + "/ssh_config")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm(), "the mode of the new file")
}

// TestExportConfig_RealmRelay checks that the export reads from the relay
// container of its realm.
func TestExportConfig_RealmRelay(t *testing.T) {
	m, c := exportDockerMock()
	require.NoError(t, ExportConfig(t.Context(), c, afero.NewMemMapFs(), testExportDir, "ci"))
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "ci-ssh"}, m.Calls[0].Args[:2])
}

// --- RemoveExport ---

func TestRemoveExport(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, name := range ExportFiles() {
		require.NoError(t, afero.WriteFile(fs, testExportDir+"/"+name, []byte("x"), 0600))
	}
	require.NoError(t, RemoveExport(fs, testExportDir))
	exists, err := afero.DirExists(fs, testExportDir)
	require.NoError(t, err)
	assert.False(t, exists, "the empty directory is removed")

	// Missing files are no error, and a directory with other files stays.
	require.NoError(t, afero.WriteFile(fs, testExportDir+"/lock", nil, 0600))
	require.NoError(t, RemoveExport(fs, testExportDir))
	exists, err = afero.Exists(fs, testExportDir+"/lock")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestRemoveExport_Error(t *testing.T) {
	base := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(base, testExportDir+"/known_hosts", nil, 0600))
	fs := &errFs{Fs: base, op: "remove", errOn: "known_hosts"}
	err := RemoveExport(fs, testExportDir)
	require.ErrorIs(t, err, errInjected)
	assert.ErrorContains(t, err, "removing known_hosts")
}

// TestRemoveExport_ConfigFirst checks that RemoveExport removes ssh_config
// before the key and the host keys it names.
func TestRemoveExport_ConfigFirst(t *testing.T) {
	base := afero.NewMemMapFs()
	for _, name := range ExportFiles() {
		require.NoError(t, afero.WriteFile(base, testExportDir+"/"+name, []byte("x"), 0600))
	}
	fs := &errFs{Fs: base, op: "remove", errOn: KnownHostsFile}
	require.ErrorIs(t, RemoveExport(fs, testExportDir), errInjected)
	config, err := afero.Exists(base, testExportDir+"/"+ConfigFile)
	require.NoError(t, err)
	assert.False(t, config, "ssh_config goes first")
	key, err := afero.Exists(base, testExportDir+"/"+KeyFile)
	require.NoError(t, err)
	assert.True(t, key, "the key goes last")
}
