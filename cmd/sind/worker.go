// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
)

func newCreateWorkerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "worker [CLUSTER]",
		Short:             "Add worker nodes to a cluster",
		Long:              "Add worker nodes to a cluster.\n\n" + defaultClusterHelp("CLUSTER"),
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: withProgressOn(clusterTarget, func(cmd *cobra.Command, args []string) error {
			name, err := clusterArg(args)
			if err != nil {
				return err
			}
			return runCreateWorker(cmd, name)
		}),
	}

	cmd.Flags().Int("count", 1, "number of nodes to add")
	cmd.Flags().String("image", "", "container image (default: the newest worker's, else the controller's)")
	cmd.Flags().Int("cpus", 0, fmt.Sprintf("CPU limit per node (default: the newest worker's, else %d)", config.DefaultCPUs))
	cmd.Flags().String("memory", "", fmt.Sprintf("memory limit per node (default: the newest worker's, else %s)", config.DefaultMemory))
	cmd.Flags().String("tmp-size", "", fmt.Sprintf("/tmp tmpfs size (default: the newest worker's, else %s)", config.DefaultTmpSize))
	cmd.Flags().Bool("unmanaged", false, "don't start slurmd, don't add to slurm.conf")
	addPullFlag(cmd, "pull the --image before creating containers")
	cmd.Flags().StringSlice("cap-add", nil, "add Linux capabilities, e.g. SYS_ADMIN; --cap-add= for none (default: the newest worker's)")
	cmd.Flags().StringSlice("cap-drop", nil, "drop Linux capabilities; --cap-drop= for none (default: the newest worker's)")
	cmd.Flags().StringSlice("device", nil, "host devices to expose, e.g. /dev/fuse; --device= for none (default: the newest worker's)")
	cmd.Flags().StringSlice("security-opt", nil, "security options; --security-opt= for none (default: the newest worker's)")
	addWaitFlag(cmd)

	return cmd
}

// listFlag returns the values of a repeatable flag, nil when it is not
// given and an empty list when it is given only empty (--device=), which
// replaces the newest worker's list with none (see
// cluster.WorkerAddOptions). pflag's GetStringSlice returns an empty list,
// not nil, in both cases.
func listFlag(cmd *cobra.Command, name string) []string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	values, _ := cmd.Flags().GetStringSlice(name)
	return values
}

// checkCreateWorkerFlags rejects flag values create worker cannot act on as
// usage errors, before it takes the realm lock or calls docker.
func checkCreateWorkerFlags(opts cluster.WorkerAddOptions) error {
	if opts.Count < 1 {
		return cobratree.Usagef("--count must be at least 1, got %d", opts.Count)
	}
	return cobratree.Usage(opts.Check())
}

// createWorkerOptions returns the options of create worker's flags, with a
// nil list for each list flag that is not given (see listFlag).
func createWorkerOptions(cmd *cobra.Command, clusterName string) (cluster.WorkerAddOptions, error) {
	wait, err := waitFlag(cmd)
	if err != nil {
		return cluster.WorkerAddOptions{}, err
	}
	if err := checkNotEmpty(cmd, "image", "memory", "tmp-size"); err != nil {
		return cluster.WorkerAddOptions{}, err
	}
	count, _ := cmd.Flags().GetInt("count")
	image, _ := cmd.Flags().GetString("image")
	// Without --image there is nothing to pull, so SIND_PULL is not in
	// effect; a --pull is, and Check refuses it.
	var pull bool
	if image != "" || cmd.Flags().Changed("pull") {
		if pull, err = pullFlag(cmd); err != nil {
			return cluster.WorkerAddOptions{}, err
		}
	}
	cpus, _ := cmd.Flags().GetInt("cpus")
	memory, _ := cmd.Flags().GetString("memory")
	tmpSize, _ := cmd.Flags().GetString("tmp-size")
	unmanaged, _ := cmd.Flags().GetBool("unmanaged")
	capAdd := listFlag(cmd, "cap-add")
	capDrop := listFlag(cmd, "cap-drop")
	devices := listFlag(cmd, "device")
	securityOpt := listFlag(cmd, "security-opt")

	return cluster.WorkerAddOptions{
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
		Wait:        wait,
	}, nil
}

func runCreateWorker(cmd *cobra.Command, clusterName string) error {
	opts, err := createWorkerOptions(cmd, clusterName)
	if err != nil {
		return err
	}
	if err := checkCreateWorkerFlags(opts); err != nil {
		return err
	}

	return inLockedRealm(cmd, func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
		if _, err := cluster.WorkerAdd(ctx, client, meshMgr, opts, cluster.DefaultReadinessInterval); err != nil {
			return err
		}
		updateSSHExport(cmd, client, meshMgr)
		return nil
	})
}

func newDeleteWorkerCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "worker NODES",
		Short:             "Remove worker nodes from a cluster",
		Long:              "Remove worker nodes from a cluster.\n\n" + nodesHelp,
		Example:           nodesExample("sind delete worker", "worker-0.dev"),
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWorkerNames,
		RunE: withProgressOn(nodesTarget, func(cmd *cobra.Command, args []string) error {
			return runDeleteWorker(cmd, strings.Join(args, " "))
		}),
	}
}

func runDeleteWorker(cmd *cobra.Command, nodeSpec string) error {
	targets, err := parseNodeArgs(nodeSpec)
	if err != nil {
		return err
	}

	return inLockedRealm(cmd, func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
		// A cluster that fails does not keep the others from losing their
		// workers, as with power, and the SSH export follows the workers
		// that are gone even when some fail.
		var errs []error
		for _, g := range groupByCluster(targets) {
			errs = append(errs, cluster.WorkerRemove(ctx, client, meshMgr, g.Cluster, g.ShortNames))
		}
		updateSSHExport(cmd, client, meshMgr)
		return errors.Join(errs...)
	})
}
