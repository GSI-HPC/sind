// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mesh manages the global infrastructure shared across all sind clusters.
package mesh

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

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
