// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

type contextKey int

const (
	clientKey contextKey = iota
	meshMgrKey
)

// withClient stores a docker.Client in the context.
func withClient(ctx context.Context, c *docker.Client) context.Context {
	return context.WithValue(ctx, clientKey, c)
}

// withMeshManager stores a mesh.Manager in the context.
func withMeshManager(ctx context.Context, m *mesh.Manager) context.Context {
	return context.WithValue(ctx, meshMgrKey, m)
}

// clientFrom retrieves the docker.Client from the context,
// falling back to a real OSExecutor-based client.
func clientFrom(ctx context.Context) *docker.Client {
	if c, ok := ctx.Value(clientKey).(*docker.Client); ok {
		return c
	}
	return docker.NewClient(&docker.OSExecutor{})
}

// meshMgrFrom retrieves the mesh.Manager from the context,
// falling back to creating one from the given client.
func meshMgrFrom(ctx context.Context, client *docker.Client) *mesh.Manager {
	if m, ok := ctx.Value(meshMgrKey).(*mesh.Manager); ok {
		return m
	}
	return mesh.NewManager(client)
}
