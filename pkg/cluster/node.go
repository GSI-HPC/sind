// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// Label keys used on sind containers.
const (
	LabelCluster      = "sind.cluster"
	LabelRole         = "sind.role"
	LabelSlurmVersion = "sind.slurm.version"
)

// NodeLabels returns the standard labels for a node container.
// The slurm version label is omitted when slurmVersion is empty.
func NodeLabels(clusterName, role, slurmVersion string) map[string]string {
	labels := map[string]string{
		LabelCluster: clusterName,
		LabelRole:    role,
	}
	if slurmVersion != "" {
		labels[LabelSlurmVersion] = slurmVersion
	}
	return labels
}

// RunConfig holds the parameters needed to build docker run arguments
// for creating a node container.
type RunConfig struct {
	ClusterName   string // cluster name
	ShortName     string // node hostname: "controller", "worker-0"
	Role          string // "controller", "submitter", "worker"
	Image         string // container image
	CPUs          int    // CPU limit
	Memory        string // memory limit (e.g. "2g")
	TmpSize       string // /tmp tmpfs size (e.g. "1g")
	SlurmVersion  string // slurm version for labels (optional)
	DNSIP         string // mesh DNS container IP (optional)
	DataHostPath  string // host path for data volume (empty = use docker volume)
	DataMountPath string // mount point for data (default: /data)
	Managed       bool   // start slurmd and add to slurm.conf (worker only)
}

// BuildRunArgs returns the docker arguments for creating a node container.
// The returned slice does not include "create" or "run -d" — the caller
// passes these args to Client.CreateContainer or Client.RunContainer.
func BuildRunArgs(cfg RunConfig) []string {
	var args []string

	// Identity
	args = append(args,
		"--name", string(ContainerName(cfg.ClusterName, cfg.ShortName)),
		"--hostname", cfg.ShortName,
	)

	// Network
	args = append(args, "--network", string(NetworkName(cfg.ClusterName)))
	if cfg.DNSIP != "" {
		args = append(args, "--dns", cfg.DNSIP)
	}
	args = append(args, "--dns-search", DNSSearchDomain(cfg.ClusterName))

	// Volume mounts
	configMode := "ro"
	if cfg.Role == "controller" {
		configMode = "rw"
	}
	args = append(args,
		"-v", string(VolumeName(cfg.ClusterName, "config"))+":/etc/slurm:"+configMode+",z",
		"-v", string(VolumeName(cfg.ClusterName, "munge"))+":/etc/munge:ro,z",
	)

	// Data volume
	dataMountPath := cfg.DataMountPath
	if dataMountPath == "" {
		dataMountPath = "/data"
	}
	if cfg.DataHostPath != "" {
		args = append(args, "-v", cfg.DataHostPath+":"+dataMountPath+":rw,z")
	} else {
		args = append(args, "-v", string(VolumeName(cfg.ClusterName, "data"))+":"+dataMountPath+":rw,z")
	}

	// tmpfs mounts: /tmp for user data, /run and /run/lock for systemd
	args = append(args,
		"--tmpfs", "/tmp:rw,nosuid,nodev,size="+cfg.TmpSize,
		"--tmpfs", "/run:exec,mode=755",
		"--tmpfs", "/run/lock",
	)

	// Resource limits
	args = append(args,
		"--cpus", strconv.Itoa(cfg.CPUs),
		"--memory", cfg.Memory,
	)

	// Security options for systemd containers
	args = append(args,
		"--cgroupns", "private",
		"--security-opt", "writable-cgroups=true",
		"--security-opt", "label=disable",
	)

	// Labels
	labels := NodeLabels(cfg.ClusterName, cfg.Role, cfg.SlurmVersion)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}

	// Image (must be last for docker create/run)
	args = append(args, cfg.Image)

	return args
}

// CreateNode creates a node container, connects it to the mesh network,
// and starts it. Returns the container ID.
func CreateNode(ctx context.Context, client *docker.Client, cfg RunConfig) (docker.ContainerID, error) {
	args := BuildRunArgs(cfg)

	id, err := client.CreateContainer(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("creating container %s: %w", cfg.ShortName, err)
	}

	containerName := ContainerName(cfg.ClusterName, cfg.ShortName)
	if err := client.ConnectNetwork(ctx, mesh.NetworkName, containerName); err != nil {
		return "", fmt.Errorf("connecting %s to mesh: %w", cfg.ShortName, err)
	}

	if err := client.StartContainer(ctx, containerName); err != nil {
		return "", fmt.Errorf("starting container %s: %w", cfg.ShortName, err)
	}

	return id, nil
}
