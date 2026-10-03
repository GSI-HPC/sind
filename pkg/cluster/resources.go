// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// CreateClusterNetwork creates the cluster-specific Docker bridge network.
func CreateClusterNetwork(ctx context.Context, client *docker.Client, realm, clusterName string) error {
	labels := docker.Labels{
		LabelRealm:                 realm,
		LabelCluster:               clusterName,
		docker.ComposeProjectLabel: ComposeProject(realm, clusterName),
		docker.ComposeNetworkLabel: "net",
	}
	_, err := client.CreateNetwork(ctx, NetworkName(realm, clusterName), labels)
	if err != nil {
		return fmt.Errorf("creating cluster network: %w", err)
	}
	return nil
}

// CreateClusterVolume creates a single cluster volume.
func CreateClusterVolume(ctx context.Context, client *docker.Client, realm, clusterName string, vtype VolumeType) error {
	labels := docker.Labels{
		LabelRealm:                 realm,
		LabelCluster:               clusterName,
		docker.ComposeProjectLabel: ComposeProject(realm, clusterName),
		docker.ComposeVolumeLabel:  string(vtype),
	}
	if err := client.CreateVolume(ctx, VolumeName(realm, clusterName, vtype), labels); err != nil {
		return fmt.Errorf("creating %s volume: %w", vtype, err)
	}
	return nil
}

// WriteClusterConfig generates and writes slurm.conf, sind-nodes.conf, and
// cgroup.conf to the config volume. Uses a temporary container to access the
// volume.
//
// With a managed db node it also writes slurmdbd.conf and turns on accounting
// in slurm.conf; an unmanaged db node gets neither. With identity clientIds
// it writes a new slurm.key, the key of auth/slurm, which every node reads
// from the config volume. slurmdbd refuses a slurmdbd.conf, and auth/slurm
// a slurm.key, that others may read; docker cp writes files as root, so the
// helper then runs (like the munge helper) to give both to SlurmUser with
// mode 0600 with docker exec.
func WriteClusterConfig(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster, image string, pull bool) error {
	helperName := ContainerName(realm, cfg.Name, "config-helper")
	volName := VolumeName(realm, cfg.Name, VolumeConfig)
	hasDB := cfg.HasManagedDB()
	clientIDs := cfg.Identity.Mode == config.IdentityClientIDs
	var secrets []string // paths owned by SlurmUser, mode 0600
	if hasDB {
		secrets = append(secrets, slurm.SlurmdbdConfPath)
	}
	if clientIDs {
		secrets = append(secrets, slurm.SlurmKeyPath)
	}

	args := []string{
		"--name", string(helperName),
		"--label", LabelRealm + "=" + realm,
		"--label", LabelCluster + "=" + cfg.Name,
		"-v", string(volName) + ":" + slurm.ConfDir,
	}
	if pull {
		args = append(args, "--pull", "always")
	}
	if len(secrets) > 0 {
		args = append(args, image, "sleep", "30")
		if _, err := client.RunContainer(ctx, args...); err != nil {
			return fmt.Errorf("creating config helper container: %w", err)
		}
		defer func() {
			_ = client.KillContainer(ctx, helperName)
			_ = client.RemoveContainer(ctx, helperName)
		}()
	} else {
		args = append(args, image)
		if _, err := client.CreateContainer(ctx, args...); err != nil {
			return fmt.Errorf("creating config helper container: %w", err)
		}
		defer client.RemoveContainer(ctx, helperName) //nolint:errcheck
	}

	confOpts := slurm.ConfOptions{
		BackupController: cfg.HasBackupController(),
		Accounting:       hasDB,
		Identity:         cfg.Identity.Mode,
	}
	files := docker.FileContents{
		"slurm.conf":        []byte(slurm.GenerateSlurmConf(cfg.Name, cfg.Slurm.Main, confOpts)),
		slurm.NodesConfFile: []byte(slurm.GenerateNodesConf(cfg.Nodes)),
		"cgroup.conf":       []byte(slurm.GenerateCgroupConf(cfg.Slurm.Cgroup)),
		"plugstack.conf":    []byte(slurm.GeneratePlugstackConf(cfg.Slurm.Plugstack)),
	}
	addSectionFragments(files, "slurm", cfg.Slurm.Main)
	addSectionFragments(files, "cgroup", cfg.Slurm.Cgroup)
	addSectionFragments(files, "plugstack", cfg.Slurm.Plugstack)

	// Standalone sections: only created when configured
	for _, s := range []struct {
		name    string
		section config.Section
	}{
		{"gres", cfg.Slurm.Gres},
		{"topology", cfg.Slurm.Topology},
	} {
		if !s.section.IsEmpty() {
			files[s.name+".conf"] = []byte(slurm.GenerateSectionConf(s.name, s.section))
			addSectionFragments(files, s.name, s.section)
		}
	}

	// Always create plugstack.conf.d/ directory (even if empty)
	if !cfg.Slurm.Plugstack.IsMap() {
		files["plugstack.conf.d/.keep"] = nil
	}

	if hasDB {
		files[slurm.SlurmdbdConfFile] = []byte(slurm.GenerateSlurmdbdConf(cfg.Slurm.Slurmdbd, cfg.Identity.Mode))
		addSectionFragments(files, "slurmdbd", cfg.Slurm.Slurmdbd)
	}
	if clientIDs {
		files[slurm.SlurmKeyFile] = slurm.GenerateSlurmKey()
	}

	if err := client.CopyToContainer(ctx, helperName, slurm.ConfDir, files); err != nil {
		return fmt.Errorf("writing slurm config: %w", err)
	}

	for _, path := range secrets {
		file := filepath.Base(path)
		if _, err := client.Exec(ctx, helperName, "chown", "slurm:slurm", path); err != nil {
			return fmt.Errorf("fixing %s ownership: %w", file, err)
		}
		if _, err := client.Exec(ctx, helperName, "chmod", "0600", path); err != nil {
			return fmt.Errorf("fixing %s permissions: %w", file, err)
		}
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

	err = client.CopyToContainer(ctx, helperName, slurm.MungeDir, docker.FileContents{
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

// addSectionFragments adds fragment files from a map-form section to the
// FileContents map. Each fragment becomes <name>.conf.d/<key>.conf.
func addSectionFragments(files docker.FileContents, name string, s config.Section) {
	for _, key := range s.FragmentNames() {
		files[name+".conf.d/"+key+".conf"] = []byte(s.Fragments[key])
	}
}
