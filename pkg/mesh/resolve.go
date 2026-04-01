// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// sysClassNet is the base path for network interface lookups.
// Overridden in tests to point at a temporary directory.
var sysClassNet = "/sys/class/net"

// resolvedActive checks if systemd-resolved is running.
func (m *Manager) resolvedActive(ctx context.Context) bool {
	_, _, err := m.Exec.Run(ctx, "systemctl", "is-active", "--quiet", "systemd-resolved")
	return err == nil
}

// polkitAuthorized checks if the current process can perform the given polkit
// action without interactive authentication.
func (m *Manager) polkitAuthorized(ctx context.Context, action string) bool {
	_, _, err := m.Exec.Run(ctx, "pkcheck",
		"--action-id", action,
		"--process", strconv.Itoa(os.Getpid()),
	)
	return err == nil
}

// dnsPolkitAuthorized checks if the current process can configure per-link DNS
// without interactive authentication.
func (m *Manager) dnsPolkitAuthorized(ctx context.Context) bool {
	for _, action := range []string{
		"org.freedesktop.resolve1.set-dns-servers",
		"org.freedesktop.resolve1.set-domains",
		"org.freedesktop.resolve1.revert",
	} {
		if !m.polkitAuthorized(ctx, action) {
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
	if _, err := os.Stat(filepath.Join(sysClassNet, name)); err != nil {
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
func (m *Manager) configureDNS(ctx context.Context, iface, dnsIP string) error {
	if _, _, err := m.Exec.Run(ctx, "resolvectl", "dns", iface, dnsIP); err != nil {
		return fmt.Errorf("setting DNS server on %s: %w", iface, err)
	}

	// Routing domain only (~ prefix). This routes *.realm.sind queries to the
	// mesh DNS without making this link a default route for all DNS queries.
	// Short-name SSH access uses OpenSSH's CanonicalizeHostname instead.
	domains := []string{"~" + m.Realm + ".sind"}

	args := append([]string{"domain", iface}, domains...)
	if _, _, err := m.Exec.Run(ctx, "resolvectl", args...); err != nil {
		return fmt.Errorf("setting DNS domains on %s: %w", iface, err)
	}
	return nil
}

// revertDNS removes any DNS configuration set on the given interface.
func (m *Manager) revertDNS(ctx context.Context, iface string) error {
	if _, _, err := m.Exec.Run(ctx, "resolvectl", "revert", iface); err != nil {
		return fmt.Errorf("reverting DNS on %s: %w", iface, err)
	}
	return nil
}

// configureHostDNS looks up the mesh network and DNS container, then configures
// systemd-resolved for host-side DNS resolution. Returns true if configured.
// Silently skipped when prerequisites are not met (no systemd-resolved, no
// polkit authorization, no bridge interface).
func (m *Manager) configureHostDNS(ctx context.Context) (bool, error) {
	if !m.resolvedActive(ctx) || !m.dnsPolkitAuthorized(ctx) {
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

	if err := m.configureDNS(ctx, iface, dnsIP); err != nil {
		return false, err
	}
	return true, nil
}

// revertHostDNS reverts host DNS configuration for the mesh bridge. Best-effort.
func (m *Manager) revertHostDNS(ctx context.Context) {
	if !m.resolvedActive(ctx) {
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

	if err := m.revertDNS(ctx, iface); err != nil {
		sindlog.From(ctx).InfoContext(ctx, "failed to revert host DNS", "error", err)
	}
}
