// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mesh manages the global infrastructure shared across all sind clusters.
package mesh

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// DNSImage is the container image used for the mesh DNS server.
const DNSImage = "coredns/coredns:latest"

// corefile is the CoreDNS configuration for the mesh DNS server.
// The hosts plugin serves sind.local records from /hosts and auto-reloads
// the file on changes. All other queries are forwarded to the system resolver.
const corefile = `sind.local:53 {
    hosts /hosts {
        fallthrough
    }
    log
    errors
}

.:53 {
    forward . /etc/resolv.conf
    log
    errors
}
`

// Manager handles global infrastructure resources shared across all clusters.
type Manager struct {
	Docker *docker.Client
}

// NewManager returns a Manager that operates on global resources through the given docker client.
func NewManager(docker *docker.Client) *Manager {
	return &Manager{Docker: docker}
}

// EnsureMeshNetwork creates the shared mesh network if it does not already exist.
func (m *Manager) EnsureMeshNetwork(ctx context.Context) error {
	exists, err := m.Docker.NetworkExists(ctx, cluster.MeshNetworkName)
	if err != nil {
		return fmt.Errorf("checking mesh network: %w", err)
	}
	if exists {
		return nil
	}
	_, err = m.Docker.CreateNetwork(ctx, cluster.MeshNetworkName)
	if err != nil {
		return fmt.Errorf("creating mesh network: %w", err)
	}
	return nil
}

// EnsureDNS creates the mesh DNS container if it does not already exist.
// The container runs CoreDNS on the mesh network, serving sind.local records
// from a hosts file that is updated as nodes are added and removed.
func (m *Manager) EnsureDNS(ctx context.Context) error {
	exists, err := m.Docker.ContainerExists(ctx, cluster.DNSContainerName)
	if err != nil {
		return fmt.Errorf("checking DNS container: %w", err)
	}
	if exists {
		return nil
	}

	_, err = m.Docker.CreateContainer(ctx,
		"--name", string(cluster.DNSContainerName),
		"--network", string(cluster.MeshNetworkName),
		DNSImage,
	)
	if err != nil {
		return fmt.Errorf("creating DNS container: %w", err)
	}

	err = m.Docker.CopyToContainer(ctx, cluster.DNSContainerName, "/", map[string][]byte{
		"Corefile": []byte(corefile),
		"hosts":    {},
	})
	if err != nil {
		return fmt.Errorf("writing DNS configuration: %w", err)
	}

	err = m.Docker.StartContainer(ctx, cluster.DNSContainerName)
	if err != nil {
		return fmt.Errorf("starting DNS container: %w", err)
	}

	return nil
}
