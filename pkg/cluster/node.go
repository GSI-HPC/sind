// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// DefaultDataMountPath is the default mount path for the shared data volume.
const DefaultDataMountPath = "/data"

// Label keys used on sind containers.
const (
	LabelRealm        = "sind.realm"
	LabelCluster      = "sind.cluster"
	LabelRole         = "sind.role"
	LabelSlurmVersion = "sind.slurm.version"
	LabelDataHostPath = "sind.data.hostpath"
)

// ComposeProject returns the Docker Compose project name for a cluster.
func ComposeProject(realm, clusterName string) string {
	return realm + "-" + clusterName
}

// NodeLabels returns the standard labels for a node container.
// containerNumber is the 1-based instance number for compose compatibility.
// The slurm version label is omitted when slurmVersion is empty.
// The data host path label is omitted when dataHostPath is empty (Docker volume mode).
func NodeLabels(realm, clusterName string, role config.Role, slurmVersion, dataHostPath string, containerNumber int) map[string]string {
	labels := docker.ComposeLabels(ComposeProject(realm, clusterName), string(role), containerNumber)
	labels[LabelRealm] = realm
	labels[LabelCluster] = clusterName
	labels[LabelRole] = string(role)
	if slurmVersion != "" {
		labels[LabelSlurmVersion] = slurmVersion
	}
	if dataHostPath != "" {
		labels[LabelDataHostPath] = dataHostPath
	}
	return labels
}

// RunConfig holds the parameters needed to build docker run arguments
// for creating a node container.
type RunConfig struct {
	Realm           string      // realm name (e.g. "sind")
	ClusterName     string      // cluster name
	ShortName       string      // node hostname: "controller", "worker-0"
	Role            config.Role // "controller", "submitter", "worker"
	Image           string      // container image
	CPUs            int         // CPU limit
	Memory          string      // memory limit (e.g. "2g")
	TmpSize         string      // /tmp tmpfs size (e.g. "1g")
	SlurmVersion    string      // slurm version for labels (optional)
	DNSIP           string      // mesh DNS container IP (optional)
	DataHostPath    string      // host path for data volume (empty = use docker volume)
	DataMountPath   string      // mount point for data (default: /data)
	Managed         bool        // start slurmd and add to slurm.conf (worker only)
	ContainerNumber int         // 1-based compose container instance number
	Pull            bool        // force fresh image pull (--pull always)
}

// BuildRunArgs returns the docker arguments for creating a node container.
// The returned slice does not include "create" or "run -d" — the caller
// passes these args to Client.CreateContainer or Client.RunContainer.
func BuildRunArgs(cfg RunConfig) []string {
	var args []string

	// Identity
	args = append(args,
		"--name", string(ContainerName(cfg.Realm, cfg.ClusterName, cfg.ShortName)),
		"--hostname", cfg.ShortName,
	)

	// Network
	args = append(args, "--network", string(NetworkName(cfg.Realm, cfg.ClusterName)))
	if cfg.DNSIP != "" {
		args = append(args, "--dns", cfg.DNSIP)
	}
	args = append(args, "--dns-search", DNSSearchDomain(cfg.ClusterName, cfg.Realm))

	// Volume mounts
	configMode := "ro"
	if cfg.Role == config.RoleController {
		configMode = "rw"
	}
	args = append(args,
		"-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeConfig))+":"+slurm.ConfDir+":"+configMode,
		"-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeMunge))+":"+slurm.MungeDir+":ro",
	)

	// Data volume
	dataMountPath := cfg.DataMountPath
	if dataMountPath == "" {
		dataMountPath = DefaultDataMountPath
	}
	if cfg.DataHostPath != "" {
		args = append(args, "-v", cfg.DataHostPath+":"+dataMountPath+":rw")
	} else {
		args = append(args, "-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeData))+":"+dataMountPath+":rw")
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
	labels := NodeLabels(cfg.Realm, cfg.ClusterName, cfg.Role, cfg.SlurmVersion, cfg.DataHostPath, cfg.ContainerNumber)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}

	// Pull policy
	if cfg.Pull {
		args = append(args, "--pull", "always")
	}

	// Image (must be last for docker create/run)
	args = append(args, cfg.Image)

	return args
}

// CreateNode creates a node container, connects it to the mesh network,
// and starts it. Returns the container ID.
func CreateNode(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg RunConfig) (docker.ContainerID, error) {
	args := BuildRunArgs(cfg)

	id, err := client.CreateContainer(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("creating container %s: %w", cfg.ShortName, err)
	}

	containerName := ContainerName(cfg.Realm, cfg.ClusterName, cfg.ShortName)
	if err := client.ConnectNetwork(ctx, meshMgr.NetworkName(), containerName); err != nil {
		return "", fmt.Errorf("connecting %s to mesh: %w", cfg.ShortName, err)
	}

	if err := client.StartContainer(ctx, containerName); err != nil {
		return "", fmt.Errorf("starting container %s: %w", cfg.ShortName, err)
	}

	return id, nil
}

// NodeRunConfigs builds RunConfig entries for all nodes in the cluster config.
// Worker nodes are indexed sequentially across all worker groups.
func NodeRunConfigs(cfg *config.Cluster, realm, dnsIP, slurmVersion string) []RunConfig {
	var configs []RunConfig
	workerIdx := 0

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
		case config.RoleController, config.RoleSubmitter:
			configs = append(configs, RunConfig{
				Realm:           realm,
				ClusterName:     cfg.Name,
				ShortName:       string(n.Role),
				Role:            n.Role,
				Image:           n.Image,
				CPUs:            n.CPUs,
				Memory:          n.Memory,
				TmpSize:         n.TmpSize,
				SlurmVersion:    slurmVersion,
				DNSIP:           dnsIP,
				DataHostPath:    dataHostPath,
				DataMountPath:   dataMountPath,
				ContainerNumber: 1,
				Pull:            cfg.Pull,
			})
		case config.RoleWorker:
			count := n.Count
			if count <= 0 {
				count = 1
			}
			isManaged := n.Managed == nil || *n.Managed
			for i := 0; i < count; i++ {
				configs = append(configs, RunConfig{
					Realm:           realm,
					ClusterName:     cfg.Name,
					ShortName:       fmt.Sprintf("worker-%d", workerIdx),
					Role:            config.RoleWorker,
					Image:           n.Image,
					CPUs:            n.CPUs,
					Memory:          n.Memory,
					TmpSize:         n.TmpSize,
					SlurmVersion:    slurmVersion,
					DNSIP:           dnsIP,
					DataHostPath:    dataHostPath,
					DataMountPath:   dataMountPath,
					Managed:         isManaged,
					ContainerNumber: workerIdx + 1,
					Pull:            cfg.Pull,
				})
				workerIdx++
			}
		}
	}
	return configs
}

// CreateClusterNodes creates all node containers for the cluster.
// Each node is created, connected to the mesh network, and started.
func CreateClusterNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, configs []RunConfig) error {
	for _, cfg := range configs {
		_, err := CreateNode(ctx, client, meshMgr, cfg)
		if err != nil {
			return fmt.Errorf("node %s: %w", cfg.ShortName, err)
		}
	}
	return nil
}

// EnableSlurmServices enables the role-appropriate Slurm daemon on each node.
// Controller nodes get slurmctld; managed worker nodes get slurmd.
// Submitter and unmanaged worker nodes are skipped.
func EnableSlurmServices(ctx context.Context, client *docker.Client, configs []RunConfig) error {
	for _, cfg := range configs {
		var service string
		switch cfg.Role {
		case config.RoleController:
			service = "slurmctld"
		case config.RoleWorker:
			if !cfg.Managed {
				continue
			}
			service = "slurmd"
		default:
			continue
		}

		containerName := ContainerName(cfg.Realm, cfg.ClusterName, cfg.ShortName)
		_, err := client.Exec(ctx, containerName, "systemctl", "enable", "--now", service)
		if err != nil {
			return fmt.Errorf("enabling %s on %s: %w", service, cfg.ShortName, err)
		}
	}
	return nil
}
