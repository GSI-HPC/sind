// SPDX-License-Identifier: LGPL-3.0-or-later

// Package ssh handles SSH key injection and host key collection for node
// containers, and the export of the SSH client configuration.
package ssh

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/GSI-HPC/sind/internal/hostname"
	"github.com/GSI-HPC/sind/pkg/docker"
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
// Placeholders: realm, dir.
const sshRealmTemplate = `Host *.%[1]s.sind
    IdentityFile %[2]s/id_ed25519
    UserKnownHostsFile %[2]s/known_hosts
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
// the realm's nodes through its SSH relay container, with the private key
// and known_hosts in dir. hosts are the nodes' DNS names; a name in it that
// is not a node's DNS name in the realm, <node>.<cluster>.<realm>.sind, is
// left out. For the default realm, it includes hostname canonicalization
// directives that enable short-name SSH access (e.g. "ssh controller").
func GenerateSSHConfig(sshContainer docker.ContainerName, dir, realm string, hosts []string) string {
	var config strings.Builder
	if realm == defaultRealm {
		fmt.Fprintf(&config, sshCanonicalTemplate, realm)
	}
	fmt.Fprintf(&config, sshRealmTemplate, realm, dir)
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

// ExportConfig exports SSH configuration to the given directory by reading
// the private key and known_hosts from the SSH relay container, in one
// docker exec, and writing ssh_config, id_ed25519, and known_hosts to dir.
// When the relay container does not exist, the error satisfies
// docker.IsNotFound and dir is left as it is.
func ExportConfig(ctx context.Context, client *docker.Client, fs afero.Fs, dir, realm string, sshContainer docker.ContainerName) error {
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

	if err := afero.WriteFile(fs, filepath.Join(dir, "ssh_config"), []byte(GenerateSSHConfig(sshContainer, dir, realm, knownHostNames(knownHosts))), 0644); err != nil {
		return fmt.Errorf("writing ssh_config: %w", err)
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "id_ed25519"), []byte(privKey), 0600); err != nil {
		return fmt.Errorf("writing id_ed25519: %w", err)
	}

	if err := afero.WriteFile(fs, filepath.Join(dir, "known_hosts"), []byte(knownHosts), 0644); err != nil {
		return fmt.Errorf("writing known_hosts: %w", err)
	}

	return nil
}
