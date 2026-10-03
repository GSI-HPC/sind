// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/GSI-HPC/sind/internal/termtext"
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
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				if len(args) > 0 {
					return usagef("--all does not accept arguments")
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
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}

	unlock, err := acquireRealmLock(ctx, realm, "")
	if err != nil {
		return err
	}
	defer unlock()

	meshMgr := meshMgrFrom(ctx, client, realm)

	if err := cluster.Delete(ctx, client, meshMgr, name); err != nil {
		return err
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", termtext.EscapeText(exportErr.Error()))
		}
	}

	return nil
}

func runDeleteClustersAll(cmd *cobra.Command) error {
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

	meshMgr := meshMgrFrom(ctx, client, realm)

	// The SSH export follows the mesh even when some clusters fail to
	// delete.
	err = cluster.DeleteAll(ctx, client, meshMgr)

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", termtext.EscapeText(exportErr.Error()))
		}
	}

	return err
}
