// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// resolvedActive checks if systemd-resolved is running.
func resolvedActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run() == nil
}

// polkitAuthorized checks if the current process can perform the given polkit
// action without interactive authentication.
func polkitAuthorized(action string) bool {
	return exec.Command("pkcheck",
		"--action-id", action,
		"--process", strconv.Itoa(os.Getpid()),
	).Run() == nil
}

// dnsPolkitAuthorized checks if the current process can configure per-link DNS
// without interactive authentication.
func dnsPolkitAuthorized() bool {
	for _, action := range []string{
		"org.freedesktop.resolve1.set-dns-servers",
		"org.freedesktop.resolve1.set-domains",
		"org.freedesktop.resolve1.revert",
	} {
		if !polkitAuthorized(action) {
			return false
		}
	}
	return true
}

// findBridgeInterface returns the Linux bridge interface name for a Docker
// network ID. Docker names bridges "br-" + first 12 chars of the network ID.
func findBridgeInterface(networkID string) (string, error) {
	if len(networkID) < 12 {
		return "", fmt.Errorf("network ID too short: %q", networkID)
	}
	name := "br-" + networkID[:12]
	if _, err := os.Stat(filepath.Join("/sys/class/net", name)); err != nil {
		return "", fmt.Errorf("bridge interface %s not found: %w", name, err)
	}
	return name, nil
}

// configureDNS sets up systemd-resolved to route queries for the realm's DNS
// zone to the mesh DNS container via the mesh bridge interface.
//
// For the default realm, search domains are added for short-name resolution:
//   - <realm>.sind (enables "controller.default" → FQDN)
//   - default.<realm>.sind (enables bare "controller" → FQDN for default cluster)
func configureDNS(iface, dnsIP, realm string) error {
	if err := exec.Command("resolvectl", "dns", iface, dnsIP).Run(); err != nil {
		return fmt.Errorf("setting DNS server on %s: %w", iface, err)
	}

	// Routing domain only (~ prefix). This routes *.realm.sind queries to the
	// mesh DNS without making this link a default route for all DNS queries.
	// Short-name SSH access uses OpenSSH's CanonicalizeHostname instead.
	domains := []string{"~" + realm + ".sind"}

	args := append([]string{"domain", iface}, domains...)
	if err := exec.Command("resolvectl", args...).Run(); err != nil {
		return fmt.Errorf("setting DNS domains on %s: %w", iface, err)
	}
	return nil
}

// revertDNS removes any DNS configuration set on the given interface.
func revertDNS(iface string) error {
	if err := exec.Command("resolvectl", "revert", iface).Run(); err != nil {
		return fmt.Errorf("reverting DNS on %s: %w", iface, err)
	}
	return nil
}

// configureHostDNS looks up the mesh network and DNS container, then configures
// systemd-resolved for host-side DNS resolution. Returns true if configured.
// Silently skipped when prerequisites are not met (no systemd-resolved, no
// polkit authorization, no bridge interface).
func (m *Manager) configureHostDNS(ctx context.Context) (bool, error) {
	if !resolvedActive() || !dnsPolkitAuthorized() {
		return false, nil
	}

	netInfo, err := m.Docker.InspectNetwork(ctx, m.NetworkName())
	if err != nil {
		return false, nil
	}

	iface, err := findBridgeInterface(netInfo.ID)
	if err != nil {
		return false, nil
	}

	dnsInfo, err := m.Docker.InspectContainer(ctx, m.DNSContainerName())
	if err != nil {
		return false, nil
	}
	dnsIP := dnsInfo.IPs[m.NetworkName()]

	if err := configureDNS(iface, dnsIP, m.Realm); err != nil {
		return false, err
	}
	return true, nil
}

// revertHostDNS reverts host DNS configuration for the mesh bridge. Best-effort.
func (m *Manager) revertHostDNS(ctx context.Context) {
	if !resolvedActive() {
		return
	}

	netInfo, err := m.Docker.InspectNetwork(ctx, m.NetworkName())
	if err != nil {
		return
	}

	iface, err := findBridgeInterface(netInfo.ID)
	if err != nil {
		return
	}

	if err := revertDNS(iface); err != nil {
		sindlog.From(ctx).InfoContext(ctx, "failed to revert host DNS", "error", err)
	}
}
