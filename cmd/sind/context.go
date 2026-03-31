// SPDX-License-Identifier: LGPL-3.0-or-later

// Package main implements the sind CLI.
package main

import (
	"context"
	"os"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
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

// clientFrom retrieves the docker.Client from the context,
// falling back to a real OSExecutor-based client.
func clientFrom(ctx context.Context) *docker.Client {
	if c, ok := ctx.Value(clientKey).(*docker.Client); ok {
		return c
	}
	return docker.NewClient(&docker.OSExecutor{})
}

// meshMgrFrom retrieves the mesh.Manager from the context,
// falling back to creating one from the given client and realm.
func meshMgrFrom(ctx context.Context, client *docker.Client, realm string) *mesh.Manager {
	if m, ok := ctx.Value(meshMgrKey).(*mesh.Manager); ok {
		return m
	}
	mgr := mesh.NewManager(client, realm)
	mgr.HostDNS = true
	return mgr
}

// resolveRealm determines the realm with the following precedence:
//
//	--realm flag > config file > SIND_REALM env var > mesh.DefaultRealm
func resolveRealm(cmd *cobra.Command, configRealm string) string {
	if cmd.Root().Flags().Changed("realm") {
		r, _ := cmd.Root().Flags().GetString("realm")
		return r
	}
	if configRealm != "" {
		return configRealm
	}
	if env := os.Getenv("SIND_REALM"); env != "" {
		return env
	}
	return mesh.DefaultRealm
}

// realmFromFlag resolves the realm when no config is available.
// Precedence: --realm flag > SIND_REALM env var > mesh.DefaultRealm
func realmFromFlag(cmd *cobra.Command) string {
	return resolveRealm(cmd, "")
}
