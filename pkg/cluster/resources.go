// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"path"
	"strings"

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
// With a managed db node it also writes slurmdbd.conf, and the
// slurmdbd.conf.d fragments of a map-form slurmdbd section, and turns on
// accounting in slurm.conf; an unmanaged db node gets neither. With identity
// clientIds it writes a new slurm.key, the key of auth/slurm, which every
// node reads from the config volume. These files can hold secrets, such as
// a StoragePass, and every node mounts the config volume: they are written
// with mode 0600, so that others can never read them, and slurmdbd refuses
// a slurmdbd.conf, and auth/slurm a slurm.key, that others may read. docker
// cp writes files as root, so the helper then runs (like the munge helper)
// to give them to SlurmUser with docker exec, and the fragments' directory
// too, with mode 0700.
func WriteClusterConfig(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster, image string, pull bool) error {
	workers, err := slurm.ManagedWorkers(cfg.Nodes)
	if err != nil {
		return fmt.Errorf("generating %s: %w", slurm.NodesConfFile, err)
	}

	helperName := ContainerName(realm, cfg.Name, "config-helper")
	volName := VolumeName(realm, cfg.Name, VolumeConfig)
	hasDB := cfg.HasManagedDB()
	clientIDs := cfg.Identity.Mode == config.IdentityClientIDs
	var secrets []string    // files owned by SlurmUser, mode 0600, relative to slurm.ConfDir
	var secretDirs []string // their directories owned by SlurmUser, mode 0700
	if hasDB {
		secrets = append(secrets, slurm.SlurmdbdConfFile)
		for _, key := range cfg.Slurm.Slurmdbd.FragmentNames() {
			secrets = append(secrets, sectionFragment("slurmdbd", key))
		}
		if cfg.Slurm.Slurmdbd.IsMap() {
			secretDirs = append(secretDirs, sectionFragmentDir("slurmdbd"))
		}
	}
	if clientIDs {
		secrets = append(secrets, slurm.SlurmKeyFile)
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
		slurm.NodesConfFile: []byte(slurm.GenerateNodesConf(workers)),
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

	withModes := make(map[string]docker.File, len(files))
	for name, content := range files {
		withModes[name] = docker.File{Content: content, Mode: 0o644}
	}
	for _, name := range secrets {
		withModes[name] = docker.File{Content: files[name], Mode: 0o600}
	}
	if err := client.CopyFilesToContainer(ctx, helperName, slurm.ConfDir, withModes); err != nil {
		return fmt.Errorf("writing slurm config: %w", err)
	}

	if len(secrets) == 0 {
		return nil
	}
	chown := append([]string{"chown", "slurm:slurm"}, confPaths(secretDirs)...)
	if _, err := client.Exec(ctx, helperName, append(chown, confPaths(secrets)...)...); err != nil {
		return fmt.Errorf("fixing ownership of %s: %w", strings.Join(secrets, ", "), err)
	}
	if len(secretDirs) > 0 {
		chmod := append([]string{"chmod", "0700"}, confPaths(secretDirs)...)
		if _, err := client.Exec(ctx, helperName, chmod...); err != nil {
			return fmt.Errorf("fixing permissions of %s: %w", strings.Join(secretDirs, ", "), err)
		}
	}

	return nil
}

// confPaths returns the absolute paths of files or directories given
// relative to slurm.ConfDir.
func confPaths(names []string) []string {
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = path.Join(slurm.ConfDir, name)
	}
	return paths
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

	// Mode 0400 from the start, so that the key is never readable by others.
	err = client.CopyFilesToContainer(ctx, helperName, slurm.MungeDir, map[string]docker.File{
		slurm.MungeKeyFile: {Content: key, Mode: 0o400},
	})
	if err != nil {
		return fmt.Errorf("writing munge key: %w", err)
	}

	// docker cp creates files as root; munge requires ownership by the munge user.
	_, err = client.Exec(ctx, helperName, "chown", "munge:munge", slurm.MungeKeyPath)
	if err != nil {
		return fmt.Errorf("fixing munge key ownership: %w", err)
	}

	return nil
}

// addSectionFragments adds fragment files from a map-form section to the
// FileContents map. Each fragment becomes <name>.conf.d/<key>.conf.
func addSectionFragments(files docker.FileContents, name string, s config.Section) {
	for _, key := range s.FragmentNames() {
		files[sectionFragment(name, key)] = []byte(s.Fragments[key])
	}
}

// sectionFragmentDir returns the directory, relative to slurm.ConfDir, that
// holds the fragments of a map-form section: <name>.conf.d.
func sectionFragmentDir(name string) string {
	return name + ".conf.d"
}

// sectionFragment returns the file, relative to slurm.ConfDir, of a
// map-form section's fragment: <name>.conf.d/<key>.conf.
func sectionFragment(name, key string) string {
	return sectionFragmentDir(name) + "/" + key + ".conf"
}
