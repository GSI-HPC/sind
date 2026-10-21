// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
)

func newDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a resource",
	}

	cmd.AddCommand(newDeleteClusterCommand())
	cmd.AddCommand(newDeleteWorkerCommand())
	cmd.AddCommand(newDeleteRealmCommand())

	return cmd
}

func newDeleteClusterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster [NAME]",
		Short: "Delete a cluster",
		// delete cluster --all and delete realm run the same code, so their
		// help says the same.
		Long: "Delete a cluster.\n\n" + defaultClusterHelp("NAME") + `

--all deletes every cluster of the realm, its mesh, its monitoring stack
and its exported SSH configuration, as delete realm does.`,
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: withProgress(func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				if len(args) > 0 {
					return argsErrorf("--all does not accept arguments")
				}
				return runDeleteClustersAll(cmd)
			}
			return runDeleteCluster(cmd, clusterArg(args))
		}),
	}

	cmd.Flags().Bool("all", false, "delete every cluster of the realm, its mesh, its monitoring stack and its exported SSH configuration, as delete realm does")

	return cmd
}

func newDeleteRealmCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "realm",
		Short: "Delete every cluster of the realm and its mesh",
		Long: `Delete every cluster of the realm, its mesh, its monitoring stack and its
exported SSH configuration, as delete cluster --all does. The realm is the
one --realm, SIND_REALM or the default names. Containers outside sind
attached to the realm's networks are detached, not removed.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := noArgs(cmd, args); err != nil {
				return fmt.Errorf("%w; name the realm with --realm, as in sind --realm %s delete realm", err, args[0])
			}
			return nil
		},
		RunE: withProgress(func(cmd *cobra.Command, _ []string) error {
			return runDeleteClustersAll(cmd)
		}),
	}
}

func runDeleteCluster(cmd *cobra.Command, name string) error {
	return inLockedRealm(cmd, func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
		if err := cluster.Delete(ctx, client, meshMgr, name); err != nil {
			return err
		}
		updateSSHExport(cmd, client, meshMgr)
		return nil
	})
}

func runDeleteClustersAll(cmd *cobra.Command) error {
	return inLockedRealm(cmd, func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
		// The SSH export follows the mesh even when some clusters fail to
		// delete.
		err := cluster.DeleteAll(ctx, client, meshMgr)
		updateSSHExport(cmd, client, meshMgr)
		return err
	})
}
