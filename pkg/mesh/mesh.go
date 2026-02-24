// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mesh manages the global infrastructure shared across all sind clusters.
package mesh

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// Global resource names shared across all clusters.
const (
	NetworkName  docker.NetworkName   = "sind-mesh"
	DNSContainerName docker.ContainerName = "sind-dns"
	SSHContainerName docker.ContainerName = "sind-ssh"
	SSHVolumeName    docker.VolumeName    = "sind-ssh-config"
)

// DNSImage is the container image used for the mesh DNS server.
const DNSImage = "coredns/coredns:latest"

// corefilePath is the path to the Corefile inside the DNS container.
const corefilePath = "/Corefile"

// Manager handles global infrastructure resources shared across all clusters.
type Manager struct {
	Docker *docker.Client
}

// NewManager returns a Manager that operates on global resources through the given docker client.
func NewManager(docker *docker.Client) *Manager {
	return &Manager{Docker: docker}
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
	if err := m.removeContainerIfExists(ctx, SSHContainerName); err != nil {
		return fmt.Errorf("removing SSH container: %w", err)
	}
	if err := m.removeContainerIfExists(ctx, DNSContainerName); err != nil {
		return fmt.Errorf("removing DNS container: %w", err)
	}

	if err := m.removeNetworkIfExists(ctx, NetworkName); err != nil {
		return fmt.Errorf("removing mesh network: %w", err)
	}

	if err := m.removeVolumeIfExists(ctx, SSHVolumeName); err != nil {
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
	exists, err := m.Docker.NetworkExists(ctx, NetworkName)
	if err != nil {
		return fmt.Errorf("checking mesh network: %w", err)
	}
	if exists {
		return nil
	}
	_, err = m.Docker.CreateNetwork(ctx, NetworkName)
	if err != nil {
		return fmt.Errorf("creating mesh network: %w", err)
	}
	return nil
}

// EnsureDNS creates the mesh DNS container if it does not already exist.
// The container runs CoreDNS on the mesh network, serving sind.local records
// from inline hosts entries in the Corefile.
func (m *Manager) EnsureDNS(ctx context.Context) error {
	exists, err := m.Docker.ContainerExists(ctx, DNSContainerName)
	if err != nil {
		return fmt.Errorf("checking DNS container: %w", err)
	}
	if exists {
		return nil
	}

	_, err = m.Docker.CreateContainer(ctx,
		"--name", string(DNSContainerName),
		"--network", string(NetworkName),
		DNSImage,
	)
	if err != nil {
		return fmt.Errorf("creating DNS container: %w", err)
	}

	err = m.Docker.CopyToContainer(ctx, DNSContainerName, "/", map[string][]byte{
		"Corefile": []byte(generateCorefile(nil)),
	})
	if err != nil {
		return fmt.Errorf("writing DNS configuration: %w", err)
	}

	err = m.Docker.StartContainer(ctx, DNSContainerName)
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

	var kept []string
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) >= 2 && fields[1] == hostname {
			continue
		}
		kept = append(kept, entry)
	}

	return m.writeDNSEntries(ctx, kept)
}

// readDNSEntries reads the current Corefile and extracts the host entries.
func (m *Manager) readDNSEntries(ctx context.Context) ([]string, error) {
	data, err := m.Docker.CopyFromContainer(ctx, DNSContainerName, corefilePath)
	if err != nil {
		return nil, fmt.Errorf("reading DNS Corefile: %w", err)
	}
	return parseEntries(string(data)), nil
}

// writeDNSEntries generates a new Corefile, writes it to the container, and
// sends SIGHUP to reload CoreDNS.
func (m *Manager) writeDNSEntries(ctx context.Context, entries []string) error {
	err := m.Docker.CopyToContainer(ctx, DNSContainerName, "/", map[string][]byte{
		"Corefile": []byte(generateCorefile(entries)),
	})
	if err != nil {
		return fmt.Errorf("writing DNS Corefile: %w", err)
	}

	err = m.Docker.SignalContainer(ctx, DNSContainerName, "HUP")
	if err != nil {
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
	b.WriteString("        fallthrough\n    }\n    log\n    errors\n}\n\n")
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
