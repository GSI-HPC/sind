// SPDX-License-Identifier: LGPL-3.0-or-later

// Package main implements the sind CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
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
	stderrKey
)

// withStderr stores the command's error stream in the context, for the
// warnings that code without the command at hand prints, and for the stack
// of a panic that a pool of pkg/cluster turned into a node's error while
// the context carries no progress Bus (cluster.WithPanicLog).
func withStderr(ctx context.Context, w io.Writer) context.Context {
	return cluster.WithPanicLog(context.WithValue(ctx, stderrKey, w), w)
}

// stderrFrom retrieves the error stream from the context, falling back to
// os.Stderr.
func stderrFrom(ctx context.Context) io.Writer {
	if w, ok := ctx.Value(stderrKey).(io.Writer); ok {
		return w
	}
	return os.Stderr
}

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

// withMeshMgr stores a mesh.Manager in the context.
func withMeshMgr(ctx context.Context, m *mesh.Manager) context.Context {
	return context.WithValue(ctx, meshMgrKey, m)
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
	stderr := stderrFrom(ctx)
	mgr.OnWarning = func(msg string) {
		_, _ = fmt.Fprintln(stderr, "Warning:", termtext.EscapeLines(msg))
	}
	return mgr
}

// resolveRealm determines the realm with the following precedence:
//
//	--realm flag > SIND_REALM env var > config file > mesh.DefaultRealm
//
// Only `create cluster` reads a config file; every other command resolves
// --realm > SIND_REALM > mesh.DefaultRealm (realmFromFlag). Ranking
// SIND_REALM above the config keeps them in step: a realm set in the
// environment is the realm every later command looks in.
//
// The realm that wins must be a valid name (config.CheckName); a config
// file's realm has already been checked by config.Validate.
func resolveRealm(cmd *cobra.Command, configRealm string) (string, error) {
	if cmd.Root().Flags().Changed("realm") {
		r, _ := cmd.Root().Flags().GetString("realm")
		if err := config.CheckName("realm", r); err != nil {
			return "", usagef("--realm: %w", err)
		}
		return r, nil
	}
	if env := os.Getenv("SIND_REALM"); env != "" {
		if err := config.CheckName("realm", env); err != nil {
			return "", fmt.Errorf("SIND_REALM: %w", err)
		}
		return env, nil
	}
	if configRealm != "" {
		return configRealm, nil
	}
	return mesh.DefaultRealm, nil
}

// realmFromFlag resolves the realm when no config is available.
// Precedence: --realm flag > SIND_REALM env var > mesh.DefaultRealm
func realmFromFlag(cmd *cobra.Command) (string, error) {
	return resolveRealm(cmd, "")
}
