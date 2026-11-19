// SPDX-License-Identifier: LGPL-3.0-or-later

// Package ssh handles SSH key injection and host key collection for node
// containers, and the export of the SSH client configuration.
package ssh

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/hostname"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
)

// authorizedKeysPath is the path to the authorized_keys file inside node containers.
const authorizedKeysPath = "/root/.ssh/authorized_keys"

// InjectKeyScript appends $1, a public key, to root's authorized_keys,
// creating the directory if needed, and prints the ed25519 host key that
// sshd serves, as ssh-keyscan reports it: "localhost ssh-ed25519 AAAA...".
// It has one command per line, for sh -e, which stops at the first that
// fails, and takes the key as an argument of the shell, not as part of its
// script. ssh-keyscan asks sshd, which must be running, for the key it
// serves; HostKey reads it from the output.
const InjectKeyScript = `mkdir -p /root/.ssh
printf '%s\n' "$1" >> ` + authorizedKeysPath + `
ssh-keyscan -t ed25519 localhost`

// HostKeyFile is where sshd keeps a node's ed25519 host public key, which
// the node generates on its first boot, in authorized_keys format with a
// comment: "ssh-ed25519 AAAA... root@host".
const HostKeyFile = "/etc/ssh/ssh_host_ed25519_key.pub"

// HostKeyFromFile returns the key of HostKeyFile's content, in
// "ssh-ed25519 AAAA..." format, without the comment.
func HostKeyFromFile(content string) (string, error) {
	fields := strings.Fields(content)
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return "", fmt.Errorf("no ed25519 host key found")
	}
	return fields[0] + " " + fields[1], nil
}

// HostKey returns the ed25519 host public key in the output of
// InjectKeyScript, in "ssh-ed25519 AAAA..." format (without the hostname
// prefix). It skips ssh-keyscan's comment lines.
func HostKey(stdout string) (string, error) {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: "localhost ssh-ed25519 AAAA..."
		_, key, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		return key, nil
	}

	return "", fmt.Errorf("no ed25519 host key found")
}

// defaultRealm is the realm that gets short-name canonicalization in the SSH config.
const defaultRealm = "sind"

// sshCanonicalTemplate makes ssh expand the short names of the default
// realm's nodes, <node> in the default cluster and <node>.<cluster>, to
// their DNS names, as "ssh controller" does to controller.default.sind.sind,
// and then read the config again for that name. Only host names shaped like
// a node's get it: set for every host, ssh would first look up each host it
// connects to under the sind domains, and take a node of that name over the
// host the user meant. The patterns are the node names: the roles,
// controller-backup and worker-<n>.
// Placeholders: realm.
const sshCanonicalTemplate = `Host controller controller.* controller-backup controller-backup.* db db.* api api.* submitter submitter.* worker-*
    CanonicalizeHostname yes
    CanonicalDomains default.%[1]s.sind %[1]s.sind

`

// sshRealmTemplate holds the settings for every node of the realm.
// IdentitiesOnly keeps ssh from offering the keys of the user's agent
// before sind's: sshd ends the connection after MaxAuthTries, 6 by default,
// failed keys, and the nodes need not see the user's other public keys.
// Placeholders: realm, the private key's path and the known_hosts path,
// quoted with configPath.
const sshRealmTemplate = `Host *.%[1]s.sind
    IdentitiesOnly yes
    IdentityFile %[2]s
    UserKnownHostsFile %[3]s
    User root
    StrictHostKeyChecking yes
`

// sshProxyTemplate connects ssh to one node through the SSH relay
// container. The node's name is written into the command instead of ssh's
// %h, which ssh puts into the command as given: a host name with shell
// syntax in it, such as one from a git submodule URL, would run as a
// command in the user's shell and in the relay's bash (CVE-2023-51385).
// With a Host block for each node's name, the command only ever runs for
// those names, which are letters, digits, hyphens and dots.
// Placeholders: host, SSH container name.
const sshProxyTemplate = `
Host %[1]s
    ProxyCommand docker exec -i %[2]s bash -c 'exec 3<>/dev/tcp/%[1]s/22; cat <&3 & cat >&3; kill $!'
`

// GenerateSSHConfig returns the SSH config that lets the user's ssh reach
// the realm's nodes through its SSH relay container (mesh.RealmNames),
// with the private key and known_hosts in dir (KeyFile, KnownHostsFile).
// hosts are the nodes' DNS names; a name in it that is not a node's DNS
// name in the realm, <node>.<cluster>.<realm>.sind, is left out. For the default realm, it includes hostname canonicalization
// directives that enable short-name SSH access (e.g. "ssh controller").
func GenerateSSHConfig(dir, realm string, hosts []string) string {
	sshContainer := mesh.RealmNames(realm).SSHContainer
	var config strings.Builder
	if realm == defaultRealm {
		fmt.Fprintf(&config, sshCanonicalTemplate, realm)
	}
	fmt.Fprintf(&config, sshRealmTemplate, realm,
		configPath(filepath.Join(dir, KeyFile)), configPath(filepath.Join(dir, KnownHostsFile)))
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if seen[host] || !isNodeHost(host, realm) {
			continue
		}
		seen[host] = true
		fmt.Fprintf(&config, sshProxyTemplate, host, sshContainer)
	}
	return config.String()
}

// configPathEscaper escapes a path for a double-quoted ssh_config argument:
// ssh reads a backslash or a double quote after a backslash as itself, and
// expands % tokens such as %d in IdentityFile and UserKnownHostsFile, %%
// being a literal %.
var configPathEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)

// configPath returns path as an ssh_config argument: in double quotes, so
// that a space in it, as in a home directory such as /home/John Doe, does
// not split it, and escaped. An unquoted space makes ssh reject the file,
// and with it every connection of a user who includes it.
func configPath(path string) string {
	return `"` + configPathEscaper.Replace(path) + `"`
}

// isNodeHost reports whether host is a node's DNS name in realm,
// <node>.<cluster>.<realm>.sind, in which the node and the cluster are
// lowercase host name labels.
func isNodeHost(host, realm string) bool {
	name, ok := strings.CutSuffix(host, "."+realm+".sind")
	if !ok {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) != 2 {
		return false
	}
	for _, label := range labels {
		if hostname.CheckLabel(label) != nil || strings.ToLower(label) != label {
			return false
		}
	}
	return true
}

// knownHostNames returns the host names in a known_hosts file, in their
// order: the first field of each line, which lists them separated by
// commas.
func knownHostNames(knownHosts string) []string {
	var names []string
	for _, line := range strings.Split(knownHosts, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		names = append(names, strings.Split(fields[0], ",")...)
	}
	return names
}

// readExportScript prints the relay's private key and known_hosts,
// separated by a NUL byte, which neither file contains.
const readExportScript = `cat /root/.ssh/id_ed25519 && printf '\0' && cat /root/.ssh/known_hosts`

// The files of the SSH config export, in the directory that ExportConfig
// writes.
const (
	// ConfigFile is the ssh_config that a user's ssh includes.
	ConfigFile = "ssh_config"
	// KeyFile is the realm's private key, which ConfigFile names.
	KeyFile = "id_ed25519"
	// KnownHostsFile holds the nodes' host keys, which ConfigFile names.
	KnownHostsFile = "known_hosts"
)

// ExportFiles returns the files of the export, in the order ExportConfig
// replaces them: the key and the host keys before the ssh_config that
// names their hosts.
func ExportFiles() []string {
	return []string{KeyFile, KnownHostsFile, ConfigFile}
}

// ExportConfig exports the SSH configuration of realm to dir: it reads the
// private key and known_hosts from the realm's SSH relay container
// (mesh.RealmNames), in one docker exec, and writes id_ed25519,
// known_hosts and ssh_config to dir. Each file is written to a temporary
// file in dir and renamed into place, in the order of ExportFiles, so that
// a reader never sees a truncated file or Host blocks before their keys.
// When the relay container does not exist, the error satisfies
// docker.IsNotFound and dir is left as it is; RemoveExport then removes the
// export of a realm without clusters.
//
// The sind CLI runs it after every command that changes the realm's
// clusters; pkg/cluster does not, so a library caller that keeps an export
// runs it itself.
func ExportConfig(ctx context.Context, client *docker.Client, fs afero.Fs, dir, realm string) error {
	sshContainer := mesh.RealmNames(realm).SSHContainer
	out, err := client.Exec(ctx, sshContainer, "sh", "-c", readExportScript)
	if err != nil {
		return fmt.Errorf("reading private key and known_hosts: %w", err)
	}
	privKey, knownHosts, ok := strings.Cut(out, "\x00")
	if !ok {
		return fmt.Errorf("reading private key and known_hosts: %s printed no separator", sshContainer)
	}

	if err := fs.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	files := map[string]struct {
		data []byte
		perm os.FileMode
	}{
		KeyFile:        {[]byte(privKey), 0600},
		KnownHostsFile: {[]byte(knownHosts), 0644},
		ConfigFile:     {[]byte(GenerateSSHConfig(dir, realm, knownHostNames(knownHosts))), 0644},
	}
	for _, name := range ExportFiles() {
		f := files[name]
		if err := replaceFile(fs, dir, name, f.data, f.perm); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// replaceFile replaces dir/name with a file of data and perm: it writes a
// temporary file in dir and renames it into place, so that the file is
// either the old or the new one, never a part.
func replaceFile(fs afero.Fs, dir, name string, data []byte, perm os.FileMode) error {
	f, err := afero.TempFile(fs, dir, "."+name+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	err = errors.Join(err, f.Close())
	if err == nil {
		err = fs.Chmod(tmp, perm)
	}
	if err == nil {
		err = fs.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = fs.Remove(tmp)
	}
	return err
}

// RemoveExport removes the files of the export from dir, and dir itself
// if that leaves it empty. Files that do not exist are no error. It removes
// them in the reverse order of ExportFiles, ssh_config first, so that a
// reader never sees Host blocks whose key or host keys are gone.
func RemoveExport(fs afero.Fs, dir string) error {
	for _, name := range slices.Backward(ExportFiles()) {
		if err := fs.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, iofs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	// A directory that holds other files, such as the realm's lock, stays.
	_ = fs.Remove(dir)
	return nil
}
