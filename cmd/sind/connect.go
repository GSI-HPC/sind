// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
)

func newConnectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect a resource to the realm",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "container CONTAINER...",
		Short: "Attach containers outside sind to the realm's networks",
		Long: "Attach containers outside sind, such as kind nodes, to the realm's mesh network and every cluster network, " +
			"so that they resolve the realm's names and reach its nodes. Clusters created later are connected to them too, " +
			"and deleting a cluster or the realm detaches them.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExternal(cmd, args, cluster.Connect)
		},
	})
	return cmd
}

func newDisconnectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disconnect",
		Short: "Disconnect a resource from the realm",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "container CONTAINER...",
		Short: "Detach containers outside sind from the realm's networks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExternal(cmd, args, cluster.Disconnect)
		},
	})
	return cmd
}

// runExternal runs cluster.Connect or cluster.Disconnect on the named
// containers under the realm lock, which keeps a cluster create from adding
// a network meanwhile.
func runExternal(cmd *cobra.Command, args []string,
	op func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, names ...docker.ContainerName) error,
) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	unlock, err := acquireRealmLock(ctx, realm, "")
	if err != nil {
		return err
	}
	defer unlock()

	names := make([]docker.ContainerName, len(args))
	for i, a := range args {
		names[i] = docker.ContainerName(a)
	}
	return op(ctx, client, meshMgrFrom(ctx, client, realm), names...)
}
