// SPDX-License-Identifier: LGPL-3.0-or-later

// Package main implements the sind CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
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
	stderrKey
)

// withStderr stores the command's error stream in the context, for the
// warnings that code without the command at hand prints, and for the stack
// of a panic that a pool of pkg/cluster turned into a node's error while
// the context carries no progress Bus (cluster.WithPanicLog).
//
// The warnings pkg/ reports for the user (sindlog.Warn), the realm lock's
// and the mesh manager's among them, go there as "Warning:" lines, escaped
// as they can quote what docker or a container wrote.
func withStderr(ctx context.Context, w io.Writer) context.Context {
	ctx = sindlog.WithWarn(ctx, func(msg string) {
		_, _ = fmt.Fprintln(w, "Warning:", termtext.EscapeLines(msg))
	})
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
	return mesh.NewHostManager(client, realm)
}

// resolveRealm determines the realm (cluster.ResolveRealm): --realm >
// SIND_REALM > configRealm > mesh.DefaultRealm. Only `create cluster` has a
// config file; every other command resolves with realmFromFlag. An invalid
// --realm is a usage error.
func resolveRealm(cmd *cobra.Command, configRealm string) (string, error) {
	var flag *string
	if cmd.Root().Flags().Changed("realm") {
		r, _ := cmd.Root().Flags().GetString("realm")
		flag = &r
	}
	realm, err := cluster.ResolveRealm(flag, configRealm)
	var flagErr *cluster.RealmFlagError
	if errors.As(err, &flagErr) {
		return "", cobratree.Usage(err)
	}
	return realm, err
}

// realmFromFlag resolves the realm when no config is available.
// Precedence: --realm flag > SIND_REALM env var > mesh.DefaultRealm
func realmFromFlag(cmd *cobra.Command) (string, error) {
	return resolveRealm(cmd, "")
}
