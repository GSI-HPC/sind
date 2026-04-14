// SPDX-License-Identifier: LGPL-3.0-or-later

// Package main implements the sind CLI.
package main

import (
	"context"
	"os"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

type contextKey int

const (
	clientKey contextKey = iota
	meshMgrKey
	fsKey
)

// withFs stores an afero.Fs in the context.
func withFs(ctx context.Context, fs afero.Fs) context.Context {
	return context.WithValue(ctx, fsKey, fs)
}

// fsFrom retrieves the afero.Fs from the context, falling back to the OS fs.
func fsFrom(ctx context.Context) afero.Fs {
	if fs, ok := ctx.Value(fsKey).(afero.Fs); ok {
		return fs
	}
	return afero.NewOsFs()
}

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
	return docker.NewClient(&cmdexec.OSExecutor{})
}

// meshMgrFrom retrieves the mesh.Manager from the context,
// falling back to creating one from the given client and realm.
func meshMgrFrom(ctx context.Context, client *docker.Client, realm string) *mesh.Manager {
	if m, ok := ctx.Value(meshMgrKey).(*mesh.Manager); ok {
		return m
	}
	mgr := mesh.NewManager(client, realm)
	mgr.Exec = &cmdexec.LoggingExecutor{
		Inner: mgr.Exec,
		Log: func(ctx context.Context, cmd string) {
			sindlog.From(ctx).Log(ctx, sindlog.LevelTrace, "exec", "cmd", cmd)
		},
	}
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
