// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"fmt"
	"net/netip"
)

// PinnedDNSIP returns the address the mesh network pins its DNS container
// to (LabelDNSIP), empty for a mesh created by an earlier sind version
// without one, or for a realm without a mesh network.
func (m *Manager) PinnedDNSIP(ctx context.Context) (string, error) {
	labels, _, err := m.Docker.NetworkLabels(ctx, m.NetworkName())
	if err != nil {
		return "", fmt.Errorf("checking mesh network: %w", err)
	}
	return labels[LabelDNSIP], nil
}

// ServiceIP returns the address n below a pinned DNS address, for the
// n-th mesh service with a pinned address of its own (n from 1): like the
// DNS address, it lies in the upper half of the mesh subnet, outside the
// --ip-range Docker gives other containers their addresses from (see
// pinnedLayout), so no container takes it while the service is stopped.
// It returns "" for an empty or unreadable dnsIP.
func ServiceIP(dnsIP string, n int) string {
	addr, err := netip.ParseAddr(dnsIP)
	if err != nil || !addr.Is4() {
		return ""
	}
	for range n {
		addr = addr.Prev()
	}
	return addr.String()
}
