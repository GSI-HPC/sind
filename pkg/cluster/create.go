// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// helperImage is the container image used for temporary helper containers
// that write files into volumes.
const helperImage = "busybox:latest"

// CreateClusterVolumes creates the config, munge, and data volumes for a cluster.
func CreateClusterVolumes(ctx context.Context, client *docker.Client, clusterName string) error {
	for _, vtype := range []string{"config", "munge", "data"} {
		if err := client.CreateVolume(ctx, VolumeName(clusterName, vtype)); err != nil {
			return fmt.Errorf("creating %s volume: %w", vtype, err)
		}
	}
	return nil
}

// WriteClusterConfig generates and writes slurm.conf, sind-nodes.conf, and
// cgroup.conf to the config volume. Uses a temporary container to access the
// volume.
func WriteClusterConfig(ctx context.Context, client *docker.Client, cfg *config.Cluster) error {
	helperName := ContainerName(cfg.Name, "config-helper")
	volName := VolumeName(cfg.Name, "config")

	_, err := client.CreateContainer(ctx,
		"--name", string(helperName),
		"-v", string(volName)+":/etc/slurm",
		helperImage,
	)
	if err != nil {
		return fmt.Errorf("creating config helper container: %w", err)
	}
	defer client.RemoveContainer(ctx, helperName) //nolint:errcheck

	files := map[string][]byte{
		"slurm.conf":      []byte(slurm.GenerateSlurmConf(cfg.Name)),
		"sind-nodes.conf": []byte(slurm.GenerateNodesConf(cfg.Nodes)),
		"cgroup.conf":     []byte(slurm.GenerateCgroupConf()),
	}

	err = client.CopyToContainer(ctx, helperName, "/etc/slurm", files)
	if err != nil {
		return fmt.Errorf("writing slurm config: %w", err)
	}

	return nil
}

// WriteMungeKey writes the given munge key to the munge volume.
// Uses a temporary container to access the volume.
func WriteMungeKey(ctx context.Context, client *docker.Client, clusterName string, key []byte) error {
	helperName := ContainerName(clusterName, "munge-helper")
	volName := VolumeName(clusterName, "munge")

	_, err := client.CreateContainer(ctx,
		"--name", string(helperName),
		"-v", string(volName)+":/etc/munge",
		helperImage,
	)
	if err != nil {
		return fmt.Errorf("creating munge helper container: %w", err)
	}
	defer client.RemoveContainer(ctx, helperName) //nolint:errcheck

	err = client.CopyToContainer(ctx, helperName, "/etc/munge", map[string][]byte{
		"munge.key": key,
	})
	if err != nil {
		return fmt.Errorf("writing munge key: %w", err)
	}

	return nil
}
