// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mesh manages the global infrastructure shared across all sind clusters.
package mesh

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
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

// composeLabelFlags returns sorted --label flags for a mesh container.
func composeLabelFlags(project, service string) []string {
	return docker.SortedLabelFlags(docker.ComposeLabels(project, service, 1))
}

// DNSImage is the container image used for the mesh DNS server.
const DNSImage = "coredns/coredns:latest"

// corefilePath is the path to the Corefile inside the DNS container.
const corefilePath = "/Corefile"

// Manager handles global infrastructure resources shared across all clusters.
type Manager struct {
	Docker  *docker.Client
	Exec    cmdexec.Executor // executor for non-docker system commands (systemctl, resolvectl, etc.)
	Realm   string
	Pull    bool // force fresh image pull (--pull always)
	HostDNS bool // configure host DNS resolution via systemd-resolved
	created bool // set by EnsureMesh when mesh infrastructure is freshly created
}

// NewManager returns a Manager that operates on global resources through the
// given docker client. The realm determines resource naming: realm "sind"
// produces names like "sind-mesh", "sind-dns", etc.
func NewManager(docker *docker.Client, realm string) *Manager {
	return &Manager{Docker: docker, Exec: &cmdexec.OSExecutor{}, Realm: realm}
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
	log := sindlog.From(ctx)
	log.InfoContext(ctx, "ensuring mesh infrastructure", "realm", m.Realm)
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

	// Best-effort: configure host DNS resolution via systemd-resolved.
	if m.HostDNS {
		if ok, err := m.configureHostDNS(ctx); err != nil {
			log.InfoContext(ctx, "host DNS configuration failed", "error", err)
		} else if ok {
			log.InfoContext(ctx, "host DNS resolution enabled")
		}
	}

	log.DebugContext(ctx, "mesh infrastructure ready")
	return nil
}

// CleanupMesh removes all global infrastructure resources. This should only
// be called when the last cluster is deleted.
func (m *Manager) CleanupMesh(ctx context.Context) error {
	log := sindlog.From(ctx)
	log.InfoContext(ctx, "cleaning up mesh infrastructure", "realm", m.Realm)

	// Revert host DNS before removing the bridge.
	if m.HostDNS {
		m.revertHostDNS(ctx)
	}

	// Remove containers first (auto-disconnects from networks).
	// Include the keygen container which may be orphaned if a previous
	// EnsureSSHVolume was interrupted.
	if err := m.removeContainerIfExists(ctx, m.SSHKeygenName()); err != nil {
		return fmt.Errorf("removing SSH keygen container: %w", err)
	}
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

// Created reports whether EnsureMesh created new mesh infrastructure in this
// invocation (i.e. the mesh did not already exist). This is used to decide
// whether cleanup should also tear down the mesh after a failed cluster create.
func (m *Manager) Created() bool {
	return m.created
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
	m.created = true
	networkLabels := docker.Labels{
		docker.ComposeProjectLabel: m.ComposeProject(),
		docker.ComposeNetworkLabel: "mesh",
	}
	_, err = m.Docker.CreateNetwork(ctx, name, networkLabels)
	if err != nil {
		return fmt.Errorf("creating mesh network: %w", err)
	}
	return nil
}

// EnsureDNS creates the mesh DNS container if it does not already exist.
// The container runs CoreDNS on the mesh network, serving <realm>.sind records
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

	err = m.Docker.CopyToContainer(ctx, name, "/", docker.FileContents{
		"Corefile": []byte(generateCorefile(m.Realm, nil)),
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
// The hostname should be a fully qualified sind DNS name (e.g. "controller.dev.sind.sind").
func (m *Manager) AddDNSRecord(ctx context.Context, hostname, ip string) error {
	entries, err := m.readDNSEntries(ctx)
	if err != nil {
		return err
	}

	entries = append(entries, ip+" "+hostname)

	return m.writeDNSEntries(ctx, entries)
}

// AddDNSRecords adds multiple A records to the mesh DNS Corefile and reloads
// CoreDNS once. Existing entries for the same hostnames are replaced,
// making the operation idempotent on retry.
func (m *Manager) AddDNSRecords(ctx context.Context, records []DNSRecord) error {
	if len(records) == 0 {
		return nil
	}
	entries, err := m.readDNSEntries(ctx)
	if err != nil {
		return err
	}

	// Build set of hostnames being added for dedup.
	newHostnames := make(map[string]bool, len(records))
	for _, r := range records {
		newHostnames[r.Hostname] = true
	}

	// Keep existing entries that don't conflict with new ones.
	kept := make([]string, 0, len(entries)+len(records))
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) >= 2 && newHostnames[fields[1]] {
			continue
		}
		kept = append(kept, entry)
	}
	for _, r := range records {
		kept = append(kept, r.IP+" "+r.Hostname)
	}

	return m.writeDNSEntries(ctx, kept)
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

// RemoveDNSRecords removes all A records for the given hostnames from the
// mesh DNS Corefile and reloads CoreDNS once.
func (m *Manager) RemoveDNSRecords(ctx context.Context, hostnames []string) error {
	if len(hostnames) == 0 {
		return nil
	}
	entries, err := m.readDNSEntries(ctx)
	if err != nil {
		return err
	}

	remove := make(map[string]bool, len(hostnames))
	for _, h := range hostnames {
		remove[h] = true
	}

	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) >= 2 && remove[fields[1]] {
			continue
		}
		kept = append(kept, entry)
	}

	return m.writeDNSEntries(ctx, kept)
}

// Info holds information about the mesh infrastructure for a realm.
type Info struct {
	Network      string `json:"network"`
	DNSContainer string `json:"dns_container"`
	DNSIP        string `json:"dns_ip"`
	DNSZone      string `json:"dns_zone"`
	DNSImage     string `json:"dns_image"`
	SSHContainer string `json:"ssh_container"`
	SSHVolume    string `json:"ssh_volume"`
	SSHImage     string `json:"ssh_image"`
}

// GetInfo returns information about the mesh infrastructure for this realm.
// The mesh must exist (DNS container must be running to resolve the DNS IP).
// Returns an error containing "no mesh found for realm" if the DNS container
// does not exist yet.
func (m *Manager) GetInfo(ctx context.Context) (*Info, error) {
	dnsName := m.DNSContainerName()
	netName := m.NetworkName()

	exists, err := m.Docker.ContainerExists(ctx, dnsName)
	if err != nil {
		return nil, fmt.Errorf("checking DNS container: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("no mesh found for realm %q", m.Realm)
	}

	dnsInfo, err := m.Docker.InspectContainer(ctx, dnsName)
	if err != nil {
		return nil, fmt.Errorf("inspecting DNS container: %w", err)
	}

	return &Info{
		Network:      string(netName),
		DNSContainer: string(dnsName),
		DNSIP:        dnsInfo.IPs[netName],
		DNSZone:      m.Realm + ".sind",
		DNSImage:     DNSImage,
		SSHContainer: string(m.SSHContainerName()),
		SSHVolume:    string(m.SSHVolumeName()),
		SSHImage:     SSHImage,
	}, nil
}

// DNSRecord represents a single A record in the mesh DNS.
type DNSRecord struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
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
	err := m.Docker.CopyToContainer(ctx, name, "/", docker.FileContents{
		"Corefile": []byte(generateCorefile(m.Realm, entries)),
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
// The zone is derived from the realm: "<realm>.sind".
func generateCorefile(realm string, entries []string) string {
	var b strings.Builder
	b.WriteString(realm + ".sind:53 {\n    hosts {\n")
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
