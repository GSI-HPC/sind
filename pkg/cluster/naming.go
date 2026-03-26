// SPDX-License-Identifier: LGPL-3.0-or-later

// Package cluster provides types and operations for sind cluster management.
package cluster

import "github.com/GSI-HPC/sind/pkg/docker"

// NetworkName returns the Docker network name for a cluster.
func NetworkName(realm, cluster string) docker.NetworkName {
	return docker.NetworkName(realm + "-" + cluster + "-net")
}

// ContainerName returns the Docker container name for a node.
// shortName is the node's hostname, e.g. "controller", "submitter", "worker-0".
func ContainerName(realm, cluster, shortName string) docker.ContainerName {
	return docker.ContainerName(realm + "-" + cluster + "-" + shortName)
}

// VolumeName returns the Docker volume name for a cluster resource.
// volumeType is one of: "config", "munge", "data".
func VolumeName(realm, cluster, volumeType string) docker.VolumeName {
	return docker.VolumeName(realm + "-" + cluster + "-" + volumeType)
}

// ContainerPrefix returns the container name prefix for a cluster,
// used to extract short names from full container names.
func ContainerPrefix(realm, cluster string) string {
	return realm + "-" + cluster + "-"
}
