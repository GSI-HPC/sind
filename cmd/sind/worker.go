// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

func newCreateWorkerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker [CLUSTER]",
		Short: "Add worker nodes to a cluster",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
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

	return cmd
}

func runCreateWorker(cmd *cobra.Command, clusterName string) error {
	count, _ := cmd.Flags().GetInt("count")
	image, _ := cmd.Flags().GetString("image")
	cpus, _ := cmd.Flags().GetInt("cpus")
	memory, _ := cmd.Flags().GetString("memory")
	tmpSize, _ := cmd.Flags().GetString("tmp-size")
	unmanaged, _ := cmd.Flags().GetBool("unmanaged")

	opts := cluster.WorkerAddOptions{
		ClusterName: clusterName,
		Count:       count,
		Image:       image,
		CPUs:        cpus,
		Memory:      memory,
		TmpSize:     tmpSize,
		Unmanaged:   unmanaged,
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	meshMgr := meshMgrFrom(ctx, client, realmFromFlag(cmd))

	nodes, err := cluster.WorkerAdd(ctx, client, meshMgr, opts, defaultReadinessInterval)
	if err != nil {
		return err
	}

	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	cmd.Printf("Added %d worker(s): %s\n", len(nodes), strings.Join(names, ", "))
	return nil
}

func newDeleteWorkerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "worker NODES",
		Short: "Remove worker nodes from a cluster",
		Args:  cobra.MinimumNArgs(1),
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
	meshMgr := meshMgrFrom(ctx, client, realmFromFlag(cmd))

	for clusterName, shortNames := range groupByCluster(targets) {
		if err := cluster.WorkerRemove(ctx, client, meshMgr, clusterName, shortNames); err != nil {
			return err
		}
		cmd.Printf("Removed %d worker(s) from cluster %q\n", len(shortNames), clusterName)
	}
	return nil
}
