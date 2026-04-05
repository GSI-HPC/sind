// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
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
		Use:               "cluster [NAME]",
		Short:             "Delete a cluster",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				if len(args) > 0 {
					return errors.New("--all does not accept arguments")
				}
				return runDeleteClustersAll(cmd)
			}
			name := config.DefaultClusterName
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

	return nil
}

func runDeleteClustersAll(cmd *cobra.Command) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm := realmFromFlag(cmd)
	meshMgr := meshMgrFrom(ctx, client, realm)

	names, err := cluster.DiscoverClusterNames(ctx, client, realm)
	if err != nil {
		return err
	}

	for _, name := range names {
		if err := cluster.Delete(ctx, client, meshMgr, name); err != nil {
			return err
		}
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", exportErr)
		}
	}

	return nil
}
