// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// CreateClusterNetwork creates the cluster-specific Docker bridge network.
func CreateClusterNetwork(ctx context.Context, client *docker.Client, realm, clusterName string) error {
	labels := map[string]string{
		docker.ComposeProjectLabel: ComposeProject(realm, clusterName),
		docker.ComposeNetworkLabel: "net",
	}
	_, err := client.CreateNetwork(ctx, NetworkName(realm, clusterName), labels)
	if err != nil {
		return fmt.Errorf("creating cluster network: %w", err)
	}
	return nil
}

// CreateClusterVolumes creates the config and munge volumes for a cluster.
// When useDataVolume is true, a data volume is also created; otherwise the
// caller is expected to use a host-path bind mount for /data.
func CreateClusterVolumes(ctx context.Context, client *docker.Client, realm, clusterName string, useDataVolume bool) error {
	types := []VolumeType{VolumeConfig, VolumeMunge}
	if useDataVolume {
		types = append(types, VolumeData)
	}
	for _, vtype := range types {
		labels := map[string]string{
			docker.ComposeProjectLabel: ComposeProject(realm, clusterName),
			docker.ComposeVolumeLabel:  string(vtype),
		}
		if err := client.CreateVolume(ctx, VolumeName(realm, clusterName, vtype), labels); err != nil {
			return fmt.Errorf("creating %s volume: %w", vtype, err)
		}
	}
	return nil
}

// WriteClusterConfig generates and writes slurm.conf, sind-nodes.conf, and
// cgroup.conf to the config volume. Uses a temporary container to access the
// volume.
func WriteClusterConfig(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster, image string, pull bool) error {
	helperName := ContainerName(realm, cfg.Name, "config-helper")
	volName := VolumeName(realm, cfg.Name, VolumeConfig)

	args := []string{
		"--name", string(helperName),
		"--label", LabelRealm + "=" + realm,
		"--label", LabelCluster + "=" + cfg.Name,
		"-v", string(volName) + ":" + slurm.ConfDir,
	}
	if pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, image)
	_, err := client.CreateContainer(ctx, args...)
	if err != nil {
		return fmt.Errorf("creating config helper container: %w", err)
	}
	defer client.RemoveContainer(ctx, helperName) //nolint:errcheck

	extraNames := cfg.Slurm.ExtraNames()

	files := map[string][]byte{
		"slurm.conf":        []byte(slurm.GenerateSlurmConf(cfg.Name, extraNames)),
		slurm.NodesConfFile: []byte(slurm.GenerateNodesConf(cfg.Nodes)),
		"cgroup.conf":       []byte(slurm.GenerateCgroupConf()),
	}
	for _, name := range extraNames {
		files[name+".conf"] = []byte(cfg.Slurm.Extra[name])
	}

	err = client.CopyToContainer(ctx, helperName, slurm.ConfDir, files)
	if err != nil {
		return fmt.Errorf("writing slurm config: %w", err)
	}

	return nil
}

// WriteMungeKey writes the given munge key to the munge volume.
// Uses a temporary container to access the volume.
func WriteMungeKey(ctx context.Context, client *docker.Client, realm, clusterName string, key []byte, image string, pull bool) error {
	helperName := ContainerName(realm, clusterName, "munge-helper")
	volName := VolumeName(realm, clusterName, VolumeMunge)

	args := []string{
		"--name", string(helperName),
		"--label", LabelRealm + "=" + realm,
		"--label", LabelCluster + "=" + clusterName,
		"-v", string(volName) + ":" + slurm.MungeDir,
	}
	if pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, image, "sleep", "30")
	_, err := client.RunContainer(ctx, args...)
	if err != nil {
		return fmt.Errorf("creating munge helper container: %w", err)
	}
	defer func() {
		_ = client.KillContainer(ctx, helperName)
		_ = client.RemoveContainer(ctx, helperName)
	}()

	err = client.CopyToContainer(ctx, helperName, slurm.MungeDir, map[string][]byte{
		slurm.MungeKeyFile: key,
	})
	if err != nil {
		return fmt.Errorf("writing munge key: %w", err)
	}

	// docker cp creates files as root; munge requires ownership by the munge user.
	_, err = client.Exec(ctx, helperName, "chown", "munge:munge", slurm.MungeKeyPath)
	if err != nil {
		return fmt.Errorf("fixing munge key ownership: %w", err)
	}
	_, err = client.Exec(ctx, helperName, "chmod", "0400", slurm.MungeKeyPath)
	if err != nil {
		return fmt.Errorf("fixing munge key permissions: %w", err)
	}

	return nil
}
