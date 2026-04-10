// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

func newCreateWorkerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "worker [CLUSTER]",
		Short:             "Add worker nodes to a cluster",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := config.DefaultClusterName
			if len(args) > 0 {
				name = args[0]
			}
			return runCreateWorker(cmd, name)
		},
	}

	cmd.Flags().Int("count", 1, "number of nodes to add")
	cmd.Flags().String("image", "", "container image")
	cmd.Flags().Int("cpus", 0, "CPU limit per node")
	cmd.Flags().String("memory", "", "memory limit")
	cmd.Flags().String("tmp-size", "", "/tmp tmpfs size")
	cmd.Flags().Bool("unmanaged", false, "don't start slurmd, don't add to slurm.conf")
	cmd.Flags().Bool("pull", false, "pull images before creating containers")
	cmd.Flags().StringSlice("cap-add", nil, "add Linux capabilities (e.g. SYS_ADMIN)")
	cmd.Flags().StringSlice("cap-drop", nil, "drop Linux capabilities")
	cmd.Flags().StringSlice("device", nil, "host devices to expose (e.g. /dev/fuse)")
	cmd.Flags().StringSlice("security-opt", nil, "security options")

	return cmd
}

func runCreateWorker(cmd *cobra.Command, clusterName string) error {
	count, _ := cmd.Flags().GetInt("count")
	image, _ := cmd.Flags().GetString("image")
	cpus, _ := cmd.Flags().GetInt("cpus")
	memory, _ := cmd.Flags().GetString("memory")
	tmpSize, _ := cmd.Flags().GetString("tmp-size")
	unmanaged, _ := cmd.Flags().GetBool("unmanaged")
	pull, _ := cmd.Flags().GetBool("pull")
	capAdd, _ := cmd.Flags().GetStringSlice("cap-add")
	capDrop, _ := cmd.Flags().GetStringSlice("cap-drop")
	devices, _ := cmd.Flags().GetStringSlice("device")
	securityOpt, _ := cmd.Flags().GetStringSlice("security-opt")

	opts := cluster.WorkerAddOptions{
		ClusterName: clusterName,
		Count:       count,
		Image:       image,
		CPUs:        cpus,
		Memory:      memory,
		TmpSize:     tmpSize,
		Unmanaged:   unmanaged,
		Pull:        pull,
		CapAdd:      capAdd,
		CapDrop:     capDrop,
		Devices:     devices,
		SecurityOpt: securityOpt,
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm := realmFromFlag(cmd)

	unlock, err := acquireRealmLock(ctx, realm, "")
	if err != nil {
		return err
	}
	defer unlock()

	meshMgr := meshMgrFrom(ctx, client, realm)

	_, err = cluster.WorkerAdd(ctx, client, meshMgr, opts, defaultReadinessInterval)
	if err != nil {
		return err
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", exportErr)
		}
	}

	return nil
}

func newDeleteWorkerCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "worker NODES",
		Short:             "Remove worker nodes from a cluster",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeNodeNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeleteWorker(cmd, strings.Join(args, ","))
		},
	}
}

func runDeleteWorker(cmd *cobra.Command, nodeSpec string) error {
	targets, err := parseNodeArgs(nodeSpec)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm := realmFromFlag(cmd)

	unlock, lockErr := acquireRealmLock(ctx, realm, "")
	if lockErr != nil {
		return lockErr
	}
	defer unlock()

	meshMgr := meshMgrFrom(ctx, client, realm)

	for clusterName, shortNames := range groupByCluster(targets) {
		if err := cluster.WorkerRemove(ctx, client, meshMgr, clusterName, shortNames); err != nil {
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
