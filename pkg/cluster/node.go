// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "strconv"

// RunConfig holds the parameters needed to build docker run arguments
// for creating a node container.
type RunConfig struct {
	ClusterName   string // cluster name
	ShortName     string // node hostname: "controller", "compute-0"
	Role          string // "controller", "submitter", "compute"
	Image         string // container image
	CPUs          int    // CPU limit
	Memory        string // memory limit (e.g. "2g")
	TmpSize       string // /tmp tmpfs size (e.g. "1g")
	SlurmVersion  string // slurm version for labels (optional)
	DNSIP         string // mesh DNS container IP (optional)
	DataHostPath  string // host path for data volume (empty = use docker volume)
	DataMountPath string // mount point for data (default: /data)
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

	// tmpfs
	args = append(args, "--tmpfs", "/tmp:rw,nosuid,nodev,size="+cfg.TmpSize)

	// Resource limits
	args = append(args,
		"--cpus", strconv.Itoa(cfg.CPUs),
		"--memory", cfg.Memory,
	)

	// Labels
	args = append(args,
		"--label", "sind.cluster="+cfg.ClusterName,
		"--label", "sind.role="+cfg.Role,
	)
	if cfg.SlurmVersion != "" {
		args = append(args, "--label", "sind.slurm.version="+cfg.SlurmVersion)
	}

	// Image (must be last for docker create/run)
	args = append(args, cfg.Image)

	return args
}
