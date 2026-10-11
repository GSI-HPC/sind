// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mesh manages the global infrastructure shared across all sind clusters.
package mesh

import (
	"context"
	"encoding/binary"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/retry"
	"golang.org/x/sync/errgroup"
)

// DefaultRealm is the realm name that produces the standard resource names.
const DefaultRealm = "sind"

// LabelRealm is the Docker label applied to mesh resources (network, volumes)
// so realm-scoped listings can filter by label rather than by name prefix.
// pkg/cluster defines the same constant for cluster-scoped resources; keeping
// it local here avoids a pkg/mesh → pkg/cluster import.
const LabelRealm = "sind.realm"

// LabelDNSIP is the label of a mesh network that records the address its
// DNS container is pinned to (see EnsureMeshNetwork). Meshes created by
// earlier sind versions lack it: their DNS container gets an address from
// Docker each time it starts.
const LabelDNSIP = "sind.dns.ip"

// meshSubnetAttempts is how often EnsureMeshNetwork tries to create a new
// mesh network again with its subnet pinned, when another network takes
// the subnet in between, before it keeps one without.
const meshSubnetAttempts = 5

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

// DNSImage is the container image used for the mesh DNS server, a pinned
// CoreDNS release.
const DNSImage = "coredns/coredns:1.14.7"

// corefilePath is the path to the Corefile inside the DNS container.
const corefilePath = "/Corefile"

// Manager handles global infrastructure resources shared across all clusters.
type Manager struct {
	Docker  *docker.Client
	Exec    cmdexec.Executor // executor for non-docker system commands (systemctl, resolvectl, etc.)
	Realm   string
	Pull    bool // force fresh image pull (--pull always)
	HostDNS bool // configure host DNS resolution via systemd-resolved

	// OnWarning receives warnings meant for the user, such as a mesh DNS
	// address that containers created earlier no longer use. The sind CLI
	// prints them to stderr. Without it, Warn logs them at warn level.
	OnWarning func(msg string)

	created bool // set by EnsureMeshNetwork when this call created the mesh network
}

// Warn reports a warning for the user through OnWarning, or the context's
// logger when OnWarning is nil.
func (m *Manager) Warn(ctx context.Context, msg string) {
	if m.OnWarning != nil {
		m.OnWarning(msg)
		return
	}
	sindlog.From(ctx).WarnContext(ctx, msg)
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

// EnsureMesh creates the global infrastructure resources (mesh network, DNS,
// SSH volume, SSH container) that do not exist yet, and starts the DNS and
// SSH containers when they exist but are stopped, as after a host reboot or
// a Docker daemon restart.
//
//	network ┬→ DNS ────┬→ SSH relay → host DNS
//	        └→ volume ─┘
//
// The DNS container starts before the relay, which resolves through it.
//
// EnsureMesh reports its work as the hidden progress step "mesh", with a
// target for each part: "network", "dns", "volume" and "relay", each
// skipped with the reason "exists" or "running" when it had nothing to do,
// so that a mesh in place leaves no trace on a display. The step shows in
// the row of a part the newest line its docker commands write to standard
// error, not the data they write to standard output (see docker.Client):
// the docker create of the DNS container and of the relay writes the lines
// of the pull of its image there when the daemon does not have it. A part
// not seen to because one before it failed ends canceled with the step.
func (m *Manager) EnsureMesh(ctx context.Context) (err error) {
	ctx, step := progress.Start(ctx, progress.KindStep, "mesh",
		progress.WithFlags(progress.Hidden|progress.Fold|progress.ShowLines), progress.Total(4))
	defer func() { step.End(err) }()
	log := sindlog.From(ctx)
	log.InfoContext(ctx, "ensuring mesh infrastructure", "realm", m.Realm)

	// The DNS container and the volume are seen to at once, in a group
	// that stops one when the other fails; every part is announced before
	// the first runs.
	g, gctx := errgroup.WithContext(ctx)
	network := startPart(ctx, partNetwork)
	dns := startPart(gctx, partDNS)
	volume := startPart(gctx, partVolume)
	relay := startPart(ctx, partRelay)

	var pinnedIP string
	err = network.run(skipExists, func(ctx context.Context) (bool, error) {
		ip, err := m.ensureMeshNetwork(ctx)
		pinnedIP = ip
		return m.created, err
	})
	if err != nil {
		_ = g.Wait() // releases gctx, under which nothing ran
		return err
	}

	var dnsIP string
	var volumeCreated bool
	g.Go(func() error {
		return dns.run(skipRunning, func(ctx context.Context) (bool, error) {
			ip, changed, err := m.ensureDNS(ctx, pinnedIP)
			dnsIP = ip
			return changed, err
		})
	})
	g.Go(func() error {
		return volume.run(skipExists, func(ctx context.Context) (bool, error) {
			created, err := m.ensureSSHVolume(ctx)
			volumeCreated = created
			return created, err
		})
	})
	if err := g.Wait(); err != nil {
		return err
	}
	err = relay.run(skipRunning, func(ctx context.Context) (bool, error) {
		return m.ensureSSH(ctx, dnsIP, volumeCreated)
	})
	if err != nil {
		return err
	}

	// Best-effort: configure host DNS resolution via systemd-resolved.
	if m.HostDNS {
		m.ensureHostDNS(ctx)
	}

	log.DebugContext(ctx, "mesh infrastructure ready")
	return nil
}

// ensureHostDNS configures host DNS resolution and logs the outcome.
// Failures do not fail the caller: host DNS is best-effort.
func (m *Manager) ensureHostDNS(ctx context.Context) {
	log := sindlog.From(ctx)
	if ok, err := m.configureHostDNS(ctx); err != nil {
		log.InfoContext(ctx, "host DNS configuration failed", "error", err)
	} else if ok {
		log.InfoContext(ctx, "host DNS resolution enabled")
	}
}

// StartMesh starts the realm's DNS and SSH relay containers that exist but
// are not running, as after a host reboot or a Docker daemon restart. It
// creates nothing: EnsureMesh does. The DNS container starts first, as the
// relay and the nodes resolve through it, and a restarted DNS container
// reapplies host DNS (HostDNS), which the host loses with the mesh bridge.
//
// It returns the DNS container's address on the mesh network, and whether
// the realm has a DNS container. A relay created with another DNS address is
// reported with Warn (see WarnStaleDNS).
func (m *Manager) StartMesh(ctx context.Context) (dnsIP string, found bool, err error) {
	var dns, relay *docker.ContainerInfo
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		info, err := m.inspectIfExists(gctx, m.DNSContainerName())
		if err != nil {
			return fmt.Errorf("inspecting DNS container: %w", err)
		}
		dns = info
		return nil
	})
	g.Go(func() error {
		info, err := m.inspectIfExists(gctx, m.SSHContainerName())
		if err != nil {
			return fmt.Errorf("inspecting SSH container: %w", err)
		}
		relay = info
		return nil
	})
	if err := g.Wait(); err != nil {
		return "", false, err
	}

	if dns != nil {
		stopped := dns.Status != docker.StateRunning
		if dns, err = m.startDNS(ctx, dns); err != nil {
			return "", false, err
		}
		dnsIP = dns.IPs[m.NetworkName()]
		if stopped && m.HostDNS {
			m.ensureHostDNS(ctx)
		}
	}
	if relay != nil {
		if err := m.startSSH(ctx, relay); err != nil {
			return "", false, err
		}
		m.WarnStaleDNS(ctx, dnsIP, relay)
	}
	return dnsIP, dns != nil, nil
}

// WarnStaleDNS warns about the containers in infos that were created to
// resolve through the mesh DNS at another address than dnsIP, the DNS
// container's current one. Docker fixes a container's DNS servers when it
// creates the container, so these keep asking the old address: names under
// <realm>.sind and external names no longer resolve in them. Containers
// created without --dns, and an empty dnsIP, are not checked.
func (m *Manager) WarnStaleDNS(ctx context.Context, dnsIP string, infos ...*docker.ContainerInfo) {
	if dnsIP == "" {
		return
	}
	var names, addrs []string
	for _, info := range infos {
		if len(info.DNS) == 0 || slices.Contains(info.DNS, dnsIP) {
			continue
		}
		names = append(names, string(info.Name))
		for _, a := range info.DNS {
			if !slices.Contains(addrs, a) {
				addrs = append(addrs, a)
			}
		}
	}
	if len(names) == 0 {
		return
	}
	msg := fmt.Sprintf("mesh DNS %s now has the address %s, but containers created before still use %s (%s): "+
		"names under %s.sind and external names do not resolve in them. Delete and re-create their clusters to repair this",
		m.DNSContainerName(), dnsIP, strings.Join(addrs, ", "), strings.Join(names, ", "), m.Realm)
	if slices.Contains(names, string(m.SSHContainerName())) {
		msg += "; the SSH relay is re-created once the realm's last cluster is deleted"
	}
	m.Warn(ctx, msg)
}

// inspectIfExists inspects a container, returning nil without error when
// it does not exist.
func (m *Manager) inspectIfExists(ctx context.Context, name docker.ContainerName) (*docker.ContainerInfo, error) {
	info, err := m.Docker.InspectContainer(ctx, name)
	if err != nil {
		if docker.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return info, nil
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

	// Remove containers first (auto-disconnects from networks), in
	// parallel. Include the keygen container that earlier sind versions
	// created to write the SSH keys, and could leave behind when
	// interrupted.
	g, gctx := errgroup.WithContext(ctx)
	for _, c := range []struct {
		name docker.ContainerName
		what string
	}{
		{m.SSHKeygenName(), "SSH keygen"},
		{m.SSHContainerName(), "SSH"},
		{m.DNSContainerName(), "DNS"},
	} {
		g.Go(func() error {
			if err := m.removeContainerIfExists(gctx, c.name); err != nil {
				return fmt.Errorf("removing %s container: %w", c.what, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if err := m.removeNetworkIfExists(ctx, m.NetworkName()); err != nil {
		return fmt.Errorf("removing mesh network: %w", err)
	}

	if err := m.removeVolumeIfExists(ctx, m.SSHVolumeName()); err != nil {
		return fmt.Errorf("removing SSH volume: %w", err)
	}

	return nil
}

// removeContainerIfExists removes a container, treating "not found" as
// success so the delete path survives a TOCTOU race or a user-driven manual
// removal.
func (m *Manager) removeContainerIfExists(ctx context.Context, name docker.ContainerName) error {
	if err := m.Docker.RemoveContainer(ctx, name); err != nil && !docker.IsNotFound(err) {
		return err
	}
	return nil
}

// removeNetworkIfExists removes a network, treating "not found" as success.
func (m *Manager) removeNetworkIfExists(ctx context.Context, name docker.NetworkName) error {
	if err := m.Docker.RemoveNetwork(ctx, name); err != nil && !docker.IsNotFound(err) {
		return err
	}
	return nil
}

// removeVolumeIfExists removes a volume, retrying past the dockerd
// async-cleanup race that follows `docker rm -f`, and treating "not found"
// as success.
func (m *Manager) removeVolumeIfExists(ctx context.Context, name docker.VolumeName) error {
	err := retry.Do(ctx,
		func() error { return m.Docker.RemoveVolume(ctx, name) },
		docker.IsVolumeInUse,
		6, 100*time.Millisecond)
	if err != nil && !docker.IsNotFound(err) {
		return err
	}
	return nil
}

// Created reports whether the last EnsureMesh (or EnsureMeshNetwork) call
// created the mesh network, that is, the realm had no mesh before. Callers
// use it to decide whether to remove the mesh again when a create fails. A
// Manager can serve several creates, so they must still check that no
// cluster uses the mesh before they remove it.
func (m *Manager) Created() bool {
	return m.created
}

// EnsureMeshNetwork creates the shared mesh network if it does not already
// exist, and records in Created whether it did.
//
// A new mesh network pins the address of the DNS container, which every
// node and the relay get as their --dns: Docker does not keep a stopped
// container's address, so after a host reboot a container that starts
// before the DNS container could take it. Docker accepts a fixed address
// (--ip) only on a network with a user-configured subnet. EnsureMeshNetwork
// therefore creates the network without one, so that Docker picks a subnet
// from its default address pools that avoids the other networks and the
// host's routes, removes it, and creates it again with that subnet and
// gateway and the --ip-range that pinnedLayout derives, recording the DNS
// address in the LabelDNSIP label. When another network takes the subnet in
// between, it starts over with the subnet Docker picks next, up to
// meshSubnetAttempts times. A subnet too small for the layout, or no
// attempt left, leaves the network without a pinned address, as earlier sind
// versions created it.
//
// A client of the same Docker daemon that does not hold the realm's lock on
// the daemon (state.LockRealm with LockOptions.Client), such as a library
// caller, can create the network between the check and the create.
// Docker's "already exists" error then counts as an existing network, so
// that the caller's failure handling never removes it.
func (m *Manager) EnsureMeshNetwork(ctx context.Context) error {
	_, err := m.ensureMeshNetwork(ctx)
	return err
}

// ensureMeshNetwork is EnsureMeshNetwork, and also returns the address the
// mesh pins the DNS container to, empty for a mesh without one.
func (m *Manager) ensureMeshNetwork(ctx context.Context) (string, error) {
	m.created = false
	labels, exists, err := m.Docker.NetworkLabels(ctx, m.NetworkName())
	if err != nil {
		return "", fmt.Errorf("checking mesh network: %w", err)
	}
	if exists {
		return labels[LabelDNSIP], nil
	}
	return m.createMeshNetwork(ctx)
}

// createMeshNetwork creates the mesh network as EnsureMeshNetwork describes,
// and returns its pinned DNS address, empty without one.
func (m *Manager) createMeshNetwork(ctx context.Context) (string, error) {
	log := sindlog.From(ctx)
	name := m.NetworkName()
	labels := docker.Labels{
		LabelRealm:                 m.Realm,
		docker.ComposeProjectLabel: m.ComposeProject(),
		docker.ComposeNetworkLabel: "mesh",
	}
	for attempt := 1; ; attempt++ {
		if _, err := m.Docker.CreateNetwork(ctx, name, labels); err != nil {
			if isAlreadyExists(err) {
				return "", nil
			}
			return "", fmt.Errorf("creating mesh network: %w", err)
		}
		m.created = true
		if attempt > meshSubnetAttempts {
			log.InfoContext(ctx, "mesh DNS address not pinned: other networks took the subnets meanwhile", "network", string(name))
			return "", nil
		}
		info, err := m.Docker.InspectNetwork(ctx, name)
		if err != nil {
			return "", fmt.Errorf("inspecting mesh network: %w", err)
		}
		ipam, dnsIP, ok := pinnedLayout(info.Subnet, info.Gateway)
		if !ok {
			log.InfoContext(ctx, "mesh DNS address not pinned: the subnet is too small", "network", string(name), "subnet", info.Subnet)
			return "", nil
		}

		if err := m.Docker.RemoveNetwork(ctx, name); err != nil {
			return "", fmt.Errorf("removing mesh network to pin its subnet: %w", err)
		}
		m.created = false
		pinned := maps.Clone(labels)
		pinned[LabelDNSIP] = dnsIP
		_, err = m.Docker.CreateNetworkWithSubnet(ctx, name, pinned, ipam)
		switch {
		case err == nil:
			m.created = true
			log.DebugContext(ctx, "mesh network created", "subnet", ipam.Subnet, "ip-range", ipam.IPRange, "dns", dnsIP)
			return dnsIP, nil
		case isAlreadyExists(err):
			return "", nil
		case !isSubnetTaken(err):
			return "", fmt.Errorf("creating mesh network: %w", err)
		}
		log.DebugContext(ctx, "mesh subnet taken meanwhile, trying another one", "subnet", ipam.Subnet, "error", err)
	}
}

// pinnedLayout returns the IPAM configuration of a mesh network with the
// given IPv4 subnet and gateway, and the DNS container's address: the lower
// half of the subnet is the --ip-range that Docker takes the other
// containers' addresses from, and the DNS container gets the last address
// before the broadcast address, in the upper half, where no other container
// gets one. The range has to hold an address for every container but the
// DNS that a bridge network can take (docker.MaxBridgeEndpoints), besides
// the network address and the gateway, so the subnet must be a /21 or
// larger. It returns false for a subnet that is not IPv4, or smaller, or a
// gateway outside it or at the DNS address.
func pinnedLayout(subnet, gateway string) (docker.NetworkIPAM, string, bool) {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil || !prefix.Addr().Is4() {
		return docker.NetworkIPAM{}, "", false
	}
	prefix = prefix.Masked()
	gw, err := netip.ParseAddr(gateway)
	if err != nil || !prefix.Contains(gw) {
		return docker.NetworkIPAM{}, "", false
	}
	hostBits := 32 - prefix.Bits()
	if hostBits < 2 || 1<<(hostBits-1)-2 < docker.MaxBridgeEndpoints-1 {
		return docker.NetworkIPAM{}, "", false
	}
	base := prefix.Addr().As4()
	broadcast := binary.BigEndian.Uint32(base[:]) | uint32(1<<hostBits-1)
	var dns [4]byte
	binary.BigEndian.PutUint32(dns[:], broadcast-1)
	dnsIP := netip.AddrFrom4(dns)
	if dnsIP == gw {
		return docker.NetworkIPAM{}, "", false
	}
	return docker.NetworkIPAM{
		Subnet:  prefix.String(),
		Gateway: gw.String(),
		IPRange: netip.PrefixFrom(prefix.Addr(), prefix.Bits()+1).String(),
	}, dnsIP.String(), true
}

// isAlreadyExists reports whether err is Docker's error for a network name
// that is taken ("network with name X already exists").
func isAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}

// isSubnetTaken reports whether err is Docker's error for a configured
// subnet that overlaps another network's. With an --ip-range, Docker's
// address allocator does not check the subnet for overlaps (moby#46756): it
// shares the subnet of a network that has it already, and then fails to
// allocate the gateway that network holds ("failed to allocate gateway
// (172.18.0.1): Address already in use"). With another gateway, the bridge
// driver refuses a subnet that overlaps another bridge network.
func isSubnetTaken(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Pool overlaps with other one on this address space") ||
		strings.Contains(msg, "networks have overlapping IPv4") ||
		strings.Contains(msg, "failed to allocate gateway") && strings.Contains(msg, "Address already in use")
}

// ensureDNS creates the mesh DNS container if it does not exist yet, or
// starts it when it is stopped, and returns its address on the mesh network,
// and whether it did either. The container runs CoreDNS on the mesh network,
// serving <realm>.sind records from inline hosts entries in the Corefile. A
// new container gets the address pinnedIP, unless it is empty.
func (m *Manager) ensureDNS(ctx context.Context, pinnedIP string) (string, bool, error) {
	info, err := m.inspectIfExists(ctx, m.DNSContainerName())
	if err != nil {
		return "", false, fmt.Errorf("checking DNS container: %w", err)
	}
	changed := info == nil || info.Status != docker.StateRunning
	if info == nil {
		info, err = m.createDNS(ctx, pinnedIP)
	} else {
		info, err = m.startDNS(ctx, info)
	}
	if err != nil {
		return "", false, err
	}
	return info.IPs[m.NetworkName()], changed, nil
}

// createDNS creates and starts the mesh DNS container with an empty
// Corefile, at the address pinnedIP unless it is empty, and returns its
// details once started.
func (m *Manager) createDNS(ctx context.Context, pinnedIP string) (*docker.ContainerInfo, error) {
	name := m.DNSContainerName()
	args := []string{
		"--name", string(name),
		"--network", string(m.NetworkName()),
	}
	if pinnedIP != "" {
		args = append(args, "--ip", pinnedIP)
	}
	args = append(args, composeLabelFlags(m.ComposeProject(), "dns")...)
	if m.Pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, DNSImage)
	if _, err := m.Docker.CreateContainer(ctx, args...); err != nil {
		return nil, fmt.Errorf("creating DNS container: %w", err)
	}

	err := m.Docker.CopyToContainer(ctx, name, "/", docker.FileContents{
		"Corefile": []byte(generateCorefile(m.Realm, nil)),
	})
	if err != nil {
		return nil, fmt.Errorf("writing DNS configuration: %w", err)
	}

	return m.startDNS(ctx, &docker.ContainerInfo{Name: name, Status: docker.StateCreated})
}

// startDNS starts the DNS container that info describes unless it is
// running, and then returns its details again: the started container has a
// new address on the mesh network.
func (m *Manager) startDNS(ctx context.Context, info *docker.ContainerInfo) (*docker.ContainerInfo, error) {
	if info.Status == docker.StateRunning {
		return info, nil
	}
	sindlog.From(ctx).InfoContext(ctx, "starting mesh DNS", "container", string(info.Name), "state", string(info.Status))
	if err := m.Docker.StartContainer(ctx, info.Name); err != nil {
		return nil, fmt.Errorf("starting DNS container: %w", err)
	}
	started, err := m.Docker.InspectContainer(ctx, info.Name)
	if err != nil {
		return nil, fmt.Errorf("inspecting DNS container: %w", err)
	}
	return started, nil
}

// AddDNSRecord adds an A record to the mesh DNS Corefile and reloads CoreDNS.
// The hostname should be a fully qualified sind DNS name (e.g. "controller.dev.sind.sind").
// An existing entry for the hostname is replaced, as AddDNSRecords does.
func (m *Manager) AddDNSRecord(ctx context.Context, hostname, ip string) error {
	return m.AddDNSRecords(ctx, []DNSRecord{{Hostname: hostname, IP: ip}})
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
	newEntries := make(map[string]bool, len(records))
	for _, r := range records {
		newHostnames[r.Hostname] = true
		newEntries[r.IP+" "+r.Hostname] = true
	}

	// Leave the Corefile alone when it has exactly these entries for the
	// hostnames, as when a power on finds the nodes at their old addresses.
	present, stale := 0, false
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) < 2 || !newHostnames[fields[1]] {
			continue
		}
		if newEntries[fields[0]+" "+fields[1]] {
			present++
		} else {
			stale = true
		}
	}
	if !stale && present == len(newEntries) {
		return nil
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
	return m.RemoveDNSRecords(ctx, []string{hostname})
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

// requireMeshContainer returns an error if the given mesh container does not
// exist, translating docker's raw "exit status 1" into a "no mesh found for
// realm" error. Used by getters to keep typo/empty-realm failures clean.
func (m *Manager) requireMeshContainer(ctx context.Context, name docker.ContainerName) error {
	exists, err := m.Docker.ContainerExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking %s: %w", name, err)
	}
	if !exists {
		return m.errNoMesh()
	}
	return nil
}

// errNoMesh returns the error for a realm without mesh.
func (m *Manager) errNoMesh() error {
	return fmt.Errorf("no mesh found for realm %q", m.Realm)
}

// GetInfo returns information about the mesh infrastructure for this realm:
// the DNS container's address (empty while it is stopped) and the images the
// DNS and SSH containers run (empty without an SSH container). Returns an
// error containing "no mesh found for realm" if the DNS container does not
// exist.
func (m *Manager) GetInfo(ctx context.Context) (*Info, error) {
	dnsName := m.DNSContainerName()
	netName := m.NetworkName()

	var dnsInfo, sshInfo *docker.ContainerInfo
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		info, err := m.inspectIfExists(gctx, dnsName)
		if err != nil {
			return fmt.Errorf("inspecting DNS container: %w", err)
		}
		dnsInfo = info
		return nil
	})
	g.Go(func() error {
		info, err := m.inspectIfExists(gctx, m.SSHContainerName())
		if err != nil {
			return fmt.Errorf("inspecting SSH container: %w", err)
		}
		sshInfo = info
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if dnsInfo == nil {
		return nil, m.errNoMesh()
	}

	info := &Info{
		Network:      string(netName),
		DNSContainer: string(dnsName),
		DNSIP:        dnsInfo.IPs[netName],
		DNSZone:      m.Realm + ".sind",
		DNSImage:     dnsInfo.ImageRef,
		SSHContainer: string(m.SSHContainerName()),
		SSHVolume:    string(m.SSHVolumeName()),
	}
	if sshInfo != nil {
		info.SSHImage = sshInfo.ImageRef
	}
	return info, nil
}

// DNSRecord represents a single A record in the mesh DNS.
type DNSRecord struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
}

// GetDNSRecords returns all A records currently served by the mesh DNS.
func (m *Manager) GetDNSRecords(ctx context.Context) ([]DNSRecord, error) {
	if err := m.requireMeshContainer(ctx, m.DNSContainerName()); err != nil {
		return nil, err
	}
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

// writeDNSEntries writes a Corefile with the given host entries into the
// DNS container and has CoreDNS load it. A running CoreDNS reloads it on
// SIGUSR1 without dropping queries. A stopped DNS container is started,
// with a stopped relay (see StartMesh), as every node and the relay resolve
// through it.
//
// The write and the reload ignore the cancellation of ctx, so that Ctrl+C
// cannot leave the DNS container stopped or with a Corefile it has not
// loaded.
func (m *Manager) writeDNSEntries(ctx context.Context, entries []string) error {
	ctx = context.WithoutCancel(ctx)
	name := m.DNSContainerName()
	err := m.Docker.CopyToContainer(ctx, name, "/", docker.FileContents{
		"Corefile": []byte(generateCorefile(m.Realm, entries)),
	})
	if err != nil {
		return fmt.Errorf("writing DNS Corefile: %w", err)
	}

	info, err := m.Docker.InspectContainer(ctx, name)
	if err != nil {
		return fmt.Errorf("inspecting DNS container: %w", err)
	}
	if info.Status != docker.StateRunning {
		if _, _, err := m.StartMesh(ctx); err != nil {
			return fmt.Errorf("reloading DNS: %w", err)
		}
		return nil
	}

	if err := m.Docker.SignalContainer(ctx, name, "USR1"); err != nil {
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
