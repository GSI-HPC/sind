// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/internal/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

func newCreateWorkerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "worker [CLUSTER]",
		Short:             "Add worker nodes to a cluster",
		Args:              optionalCluster,
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
	cmd.Flags().String("image", "", "container image (default: the newest worker's, else the controller's)")
	cmd.Flags().Int("cpus", 0, fmt.Sprintf("CPU limit per node (default: the newest worker's, else %d)", config.DefaultCPUs))
	cmd.Flags().String("memory", "", fmt.Sprintf("memory limit per node (default: the newest worker's, else %s)", config.DefaultMemory))
	cmd.Flags().String("tmp-size", "", fmt.Sprintf("/tmp tmpfs size (default: the newest worker's, else %s)", config.DefaultTmpSize))
	cmd.Flags().Bool("unmanaged", false, "don't start slurmd, don't add to slurm.conf")
	cmd.Flags().Bool("pull", false, "pull the --image before creating containers")
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
		return usagef("--count must be at least 1, got %d", opts.Count)
	}
	return usage(opts.Check())
}

// createWorkerOptions returns the options of create worker's flags, with a
// nil list for each list flag that is not given (see listFlag).
func createWorkerOptions(cmd *cobra.Command, clusterName string) (cluster.WorkerAddOptions, error) {
	wait, err := waitFlag(cmd)
	if err != nil {
		return cluster.WorkerAddOptions{}, err
	}
	count, _ := cmd.Flags().GetInt("count")
	image, _ := cmd.Flags().GetString("image")
	cpus, _ := cmd.Flags().GetInt("cpus")
	memory, _ := cmd.Flags().GetString("memory")
	tmpSize, _ := cmd.Flags().GetString("tmp-size")
	unmanaged, _ := cmd.Flags().GetBool("unmanaged")
	pull, _ := cmd.Flags().GetBool("pull")
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

	_, err = cluster.WorkerAdd(ctx, client, meshMgr, opts, cluster.DefaultReadinessInterval)
	if err != nil {
		return err
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", termtext.EscapeText(exportErr.Error()))
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
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}

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
			cmd.PrintErrln("Warning: could not update SSH config:", termtext.EscapeText(exportErr.Error()))
		}
	}

	return nil
}
