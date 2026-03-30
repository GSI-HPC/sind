// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/afero"
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
	realm := realmFromFlag(cmd)
	meshMgr := meshMgrFrom(ctx, client, realm)

	if err := cluster.Delete(ctx, client, meshMgr, name); err != nil {
		return err
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", exportErr)
		}
	}

	cmd.Printf("Cluster %q deleted\n", name)
	return nil
}

func runDeleteClustersAll(cmd *cobra.Command) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm := realmFromFlag(cmd)
	meshMgr := meshMgrFrom(ctx, client, realm)

	clusters, err := cluster.GetClusters(ctx, client, realm)
	if err != nil {
		return err
	}

	for _, c := range clusters {
		if err := cluster.Delete(ctx, client, meshMgr, c.Name); err != nil {
			return err
		}
		cmd.Printf("Cluster %q deleted\n", c.Name)
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", exportErr)
		}
	}

	return nil
}
