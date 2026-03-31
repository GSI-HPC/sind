// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

// DNSSuffix is the base domain for all sind DNS names.
const DNSSuffix = "sind"

// DNSName returns the fully qualified DNS name for a node.
// shortName is the node's hostname, e.g. "controller", "worker-0".
func DNSName(shortName, cluster, realm string) string {
	return shortName + "." + cluster + "." + realm + "." + DNSSuffix
}

// DNSSearchDomain returns the DNS search domain for a cluster.
func DNSSearchDomain(cluster, realm string) string {
	return cluster + "." + realm + "." + DNSSuffix
}
