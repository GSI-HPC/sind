// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

func newDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a resource",
	}

	cmd.AddCommand(newDeleteClusterCommand())
	cmd.AddCommand(newDeleteWorkerCommand())

	return cmd
}

func newDeleteClusterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster [NAME]",
		Short: "Delete a cluster",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				if len(args) > 0 {
					return fmt.Errorf("--all does not accept arguments")
				}
				return runDeleteClustersAll(cmd)
			}
			name := "default"
			if len(args) > 0 {
				name = args[0]
			}
			return runDeleteCluster(cmd, name)
		},
	}

	cmd.Flags().Bool("all", false, "delete all clusters")

	return cmd
}

func runDeleteCluster(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	meshMgr := meshMgrFrom(ctx, client)

	if err := cluster.Delete(ctx, client, meshMgr, name); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Cluster %q deleted\n", name)
	return nil
}

func runDeleteClustersAll(cmd *cobra.Command) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	meshMgr := meshMgrFrom(ctx, client)

	clusters, err := cluster.GetClusters(ctx, client)
	if err != nil {
		return err
	}

	for _, c := range clusters {
		if err := cluster.Delete(ctx, client, meshMgr, c.Name); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Cluster %q deleted\n", c.Name)
	}

	return nil
}
