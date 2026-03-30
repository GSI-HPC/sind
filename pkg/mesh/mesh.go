// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mesh manages the global infrastructure shared across all sind clusters.
package mesh

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// DefaultRealm is the realm name that produces the standard resource names.
const DefaultRealm = "sind"

// Default-realm resource names. Production code uses Manager methods;
// these constants are used in tests as expected values for DefaultRealm.
const (
	NetworkName      docker.NetworkName   = "sind-mesh"
	DNSContainerName docker.ContainerName = "sind-dns"
	SSHContainerName docker.ContainerName = "sind-ssh"
	SSHVolumeName    docker.VolumeName    = "sind-ssh-config"
)

// composeLabels returns compose compatibility labels for a mesh container.
func composeLabels(project, service string, containerNumber int) map[string]string {
	return map[string]string{
		"com.docker.compose.project":              project,
		"com.docker.compose.service":              service,
		"com.docker.compose.container-number":     fmt.Sprintf("%d", containerNumber),
		"com.docker.compose.oneoff":               "False",
		"com.docker.compose.config-hash":          "",
		"com.docker.compose.project.config_files": "",
	}
}

// composeLabelFlags returns --label flags for a mesh container.
func composeLabelFlags(project, service string) []string {
	labels := composeLabels(project, service, 1)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	flags := make([]string, 0, len(labels)*2)
	for _, k := range keys {
		flags = append(flags, "--label", k+"="+labels[k])
	}
	return flags
}

// DNSImage is the container image used for the mesh DNS server.
const DNSImage = "coredns/coredns:latest"

// corefilePath is the path to the Corefile inside the DNS container.
const corefilePath = "/Corefile"

// Manager handles global infrastructure resources shared across all clusters.
type Manager struct {
	Docker *docker.Client
	Realm  string
	Pull   bool // force fresh image pull (--pull always)
}

// NewManager returns a Manager that operates on global resources through the
// given docker client. The realm determines resource naming: realm "sind"
// produces names like "sind-mesh", "sind-dns", etc.
func NewManager(docker *docker.Client, realm string) *Manager {
	return &Manager{Docker: docker, Realm: realm}
}

// NetworkName returns the mesh network name for this realm.
func (m *Manager) NetworkName() docker.NetworkName {
	return docker.NetworkName(m.Realm + "-mesh")
}

// DNSContainerName returns the DNS container name for this realm.
func (m *Manager) DNSContainerName() docker.ContainerName {
	return docker.ContainerName(m.Realm + "-dns")
}

// SSHContainerName returns the SSH container name for this realm.
func (m *Manager) SSHContainerName() docker.ContainerName {
	return docker.ContainerName(m.Realm + "-ssh")
}

// SSHVolumeName returns the SSH volume name for this realm.
func (m *Manager) SSHVolumeName() docker.VolumeName {
	return docker.VolumeName(m.Realm + "-ssh-config")
}

// SSHKeygenName returns the temporary keygen container name for this realm.
func (m *Manager) SSHKeygenName() docker.ContainerName {
	return docker.ContainerName(m.Realm + "-ssh-keygen")
}

// ComposeProject returns the Docker Compose project name for this realm's mesh.
func (m *Manager) ComposeProject() string {
	return m.Realm + "-mesh"
}

// EnsureMesh creates all global infrastructure resources (mesh network, DNS,
// SSH volume, SSH container) if they do not already exist.
func (m *Manager) EnsureMesh(ctx context.Context) error {
	if err := m.EnsureMeshNetwork(ctx); err != nil {
		return err
	}
	if err := m.EnsureDNS(ctx); err != nil {
		return err
	}
	if err := m.EnsureSSHVolume(ctx); err != nil {
		return err
	}
	if err := m.EnsureSSH(ctx); err != nil {
		return err
	}
	return nil
}

// CleanupMesh removes all global infrastructure resources. This should only
// be called when the last cluster is deleted.
func (m *Manager) CleanupMesh(ctx context.Context) error {
	// Remove containers first (auto-disconnects from networks).
	if err := m.removeContainerIfExists(ctx, m.SSHContainerName()); err != nil {
		return fmt.Errorf("removing SSH container: %w", err)
	}
	if err := m.removeContainerIfExists(ctx, m.DNSContainerName()); err != nil {
		return fmt.Errorf("removing DNS container: %w", err)
	}

	if err := m.removeNetworkIfExists(ctx, m.NetworkName()); err != nil {
		return fmt.Errorf("removing mesh network: %w", err)
	}

	if err := m.removeVolumeIfExists(ctx, m.SSHVolumeName()); err != nil {
		return fmt.Errorf("removing SSH volume: %w", err)
	}

	return nil
}

// removeContainerIfExists stops and removes a container if it exists.
func (m *Manager) removeContainerIfExists(ctx context.Context, name docker.ContainerName) error {
	exists, err := m.Docker.ContainerExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_ = m.Docker.StopContainer(ctx, name) // best-effort; may already be stopped
	return m.Docker.RemoveContainer(ctx, name)
}

// removeNetworkIfExists removes a network if it exists.
func (m *Manager) removeNetworkIfExists(ctx context.Context, name docker.NetworkName) error {
	exists, err := m.Docker.NetworkExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return m.Docker.RemoveNetwork(ctx, name)
}

// removeVolumeIfExists removes a volume if it exists.
func (m *Manager) removeVolumeIfExists(ctx context.Context, name docker.VolumeName) error {
	exists, err := m.Docker.VolumeExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return m.Docker.RemoveVolume(ctx, name)
}

// EnsureMeshNetwork creates the shared mesh network if it does not already exist.
func (m *Manager) EnsureMeshNetwork(ctx context.Context) error {
	name := m.NetworkName()
	exists, err := m.Docker.NetworkExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking mesh network: %w", err)
	}
	if exists {
		return nil
	}
	networkLabels := map[string]string{
		"com.docker.compose.project": m.ComposeProject(),
		"com.docker.compose.network": "mesh",
	}
	_, err = m.Docker.CreateNetwork(ctx, name, networkLabels)
	if err != nil {
		return fmt.Errorf("creating mesh network: %w", err)
	}
	return nil
}

// EnsureDNS creates the mesh DNS container if it does not already exist.
// The container runs CoreDNS on the mesh network, serving sind.local records
// from inline hosts entries in the Corefile.
func (m *Manager) EnsureDNS(ctx context.Context) error {
	name := m.DNSContainerName()
	exists, err := m.Docker.ContainerExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking DNS container: %w", err)
	}
	if exists {
		return nil
	}

	args := []string{
		"--name", string(name),
		"--network", string(m.NetworkName()),
	}
	args = append(args, composeLabelFlags(m.ComposeProject(), "dns")...)
	if m.Pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, DNSImage)
	_, err = m.Docker.CreateContainer(ctx, args...)
	if err != nil {
		return fmt.Errorf("creating DNS container: %w", err)
	}

	err = m.Docker.CopyToContainer(ctx, name, "/", map[string][]byte{
		"Corefile": []byte(generateCorefile(nil)),
	})
	if err != nil {
		return fmt.Errorf("writing DNS configuration: %w", err)
	}

	err = m.Docker.StartContainer(ctx, name)
	if err != nil {
		return fmt.Errorf("starting DNS container: %w", err)
	}

	return nil
}

// AddDNSRecord adds an A record to the mesh DNS Corefile and reloads CoreDNS.
// The hostname should be a fully qualified sind DNS name (e.g. "controller.dev.sind.local").
func (m *Manager) AddDNSRecord(ctx context.Context, hostname, ip string) error {
	entries, err := m.readDNSEntries(ctx)
	if err != nil {
		return err
	}

	entries = append(entries, ip+" "+hostname)

	return m.writeDNSEntries(ctx, entries)
}

// RemoveDNSRecord removes all A records for the given hostname from the mesh DNS
// Corefile and reloads CoreDNS.
func (m *Manager) RemoveDNSRecord(ctx context.Context, hostname string) error {
	entries, err := m.readDNSEntries(ctx)
	if err != nil {
		return err
	}

	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) >= 2 && fields[1] == hostname {
			continue
		}
		kept = append(kept, entry)
	}

	return m.writeDNSEntries(ctx, kept)
}

// DNSRecord represents a single A record in the mesh DNS.
type DNSRecord struct {
	Hostname string
	IP       string
}

// GetDNSRecords returns all A records currently served by the mesh DNS.
func (m *Manager) GetDNSRecords(ctx context.Context) ([]DNSRecord, error) {
	entries, err := m.readDNSEntries(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]DNSRecord, 0, len(entries))
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) >= 2 {
			records = append(records, DNSRecord{IP: fields[0], Hostname: fields[1]})
		}
	}
	return records, nil
}

// readDNSEntries reads the current Corefile and extracts the host entries.
func (m *Manager) readDNSEntries(ctx context.Context) ([]string, error) {
	data, err := m.Docker.CopyFromContainer(ctx, m.DNSContainerName(), corefilePath)
	if err != nil {
		return nil, fmt.Errorf("reading DNS Corefile: %w", err)
	}
	return parseEntries(string(data)), nil
}

// writeDNSEntries generates a new Corefile, writes it to the container, and
// sends SIGHUP to reload CoreDNS.
func (m *Manager) writeDNSEntries(ctx context.Context, entries []string) error {
	name := m.DNSContainerName()
	err := m.Docker.CopyToContainer(ctx, name, "/", map[string][]byte{
		"Corefile": []byte(generateCorefile(entries)),
	})
	if err != nil {
		return fmt.Errorf("writing DNS Corefile: %w", err)
	}

	if err := m.Docker.KillContainer(ctx, name); err != nil {
		return fmt.Errorf("reloading DNS: %w", err)
	}
	if err := m.Docker.StartContainer(ctx, name); err != nil {
		return fmt.Errorf("reloading DNS: %w", err)
	}
	return nil
}

// generateCorefile builds a complete CoreDNS Corefile with the given host entries
// inlined in the hosts block. Each entry is an "IP hostname" string.
func generateCorefile(entries []string) string {
	var b strings.Builder
	b.WriteString("sind.local:53 {\n    hosts {\n")
	for _, entry := range entries {
		b.WriteString("        " + entry + "\n")
	}
	b.WriteString("        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n")
	b.WriteString(".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n")
	return b.String()
}

// parseEntries extracts host entries from a Corefile's hosts block.
// Each returned string is an "IP hostname" line.
func parseEntries(corefile string) []string {
	var entries []string
	inHosts := false
	for _, line := range strings.Split(corefile, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "hosts {" {
			inHosts = true
			continue
		}
		if inHosts && (trimmed == "fallthrough" || trimmed == "}") {
			inHosts = false
			continue
		}
		if inHosts && trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}
