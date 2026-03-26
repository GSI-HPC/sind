// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

// DNSSuffix is the base domain for all sind DNS names.
const DNSSuffix = "sind.local"

// DNSName returns the fully qualified DNS name for a node.
// shortName is the node's hostname, e.g. "controller", "worker-0".
func DNSName(shortName, cluster string) string {
	return shortName + "." + cluster + "." + DNSSuffix
}

// DNSSearchDomain returns the DNS search domain for a cluster.
func DNSSearchDomain(cluster string) string {
	return cluster + "." + DNSSuffix
}
