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

// CreateClusterNetwork creates the cluster-specific Docker bridge network.
func CreateClusterNetwork(ctx context.Context, client *docker.Client, clusterName string) error {
	_, err := client.CreateNetwork(ctx, NetworkName(clusterName))
	if err != nil {
		return fmt.Errorf("creating cluster network: %w", err)
	}
	return nil
}

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

// NodeRunConfigs builds RunConfig entries for all nodes in the cluster config.
// Compute nodes are indexed sequentially across all compute groups.
func NodeRunConfigs(cfg *config.Cluster, dnsIP, slurmVersion string) []RunConfig {
	var configs []RunConfig
	computeIdx := 0

	dataHostPath := ""
	dataMountPath := ""
	if cfg.Storage.DataStorage.Type == "hostPath" {
		dataHostPath = cfg.Storage.DataStorage.HostPath
	}
	if cfg.Storage.DataStorage.MountPath != "" {
		dataMountPath = cfg.Storage.DataStorage.MountPath
	}

	for _, n := range cfg.Nodes {
		switch n.Role {
		case "controller", "submitter":
			configs = append(configs, RunConfig{
				ClusterName:   cfg.Name,
				ShortName:     n.Role,
				Role:          n.Role,
				Image:         n.Image,
				CPUs:          n.CPUs,
				Memory:        n.Memory,
				TmpSize:       n.TmpSize,
				SlurmVersion:  slurmVersion,
				DNSIP:         dnsIP,
				DataHostPath:  dataHostPath,
				DataMountPath: dataMountPath,
			})
		case "compute":
			count := n.Count
			if count <= 0 {
				count = 1
			}
			isManaged := n.Managed == nil || *n.Managed
			for i := 0; i < count; i++ {
				configs = append(configs, RunConfig{
					ClusterName:   cfg.Name,
					ShortName:     fmt.Sprintf("compute-%d", computeIdx),
					Role:          "compute",
					Image:         n.Image,
					CPUs:          n.CPUs,
					Memory:        n.Memory,
					TmpSize:       n.TmpSize,
					SlurmVersion:  slurmVersion,
					DNSIP:         dnsIP,
					DataHostPath:  dataHostPath,
					DataMountPath: dataMountPath,
					Managed:       isManaged,
				})
				computeIdx++
			}
		}
	}
	return configs
}

// CreateClusterNodes creates all node containers for the cluster.
// Each node is created, connected to the mesh network, and started.
func CreateClusterNodes(ctx context.Context, client *docker.Client, configs []RunConfig) error {
	for _, cfg := range configs {
		_, err := CreateNode(ctx, client, cfg)
		if err != nil {
			return fmt.Errorf("node %s: %w", cfg.ShortName, err)
		}
	}
	return nil
}

// EnableSlurmServices enables the role-appropriate Slurm daemon on each node.
// Controller nodes get slurmctld; managed compute nodes get slurmd.
// Submitter and unmanaged compute nodes are skipped.
func EnableSlurmServices(ctx context.Context, client *docker.Client, configs []RunConfig) error {
	for _, cfg := range configs {
		var service string
		switch cfg.Role {
		case "controller":
			service = "slurmctld"
		case "compute":
			if !cfg.Managed {
				continue
			}
			service = "slurmd"
		default:
			continue
		}

		containerName := ContainerName(cfg.ClusterName, cfg.ShortName)
		_, err := client.Exec(ctx, containerName, "systemctl", "enable", "--now", service)
		if err != nil {
			return fmt.Errorf("enabling %s on %s: %w", service, cfg.ShortName, err)
		}
	}
	return nil
}
