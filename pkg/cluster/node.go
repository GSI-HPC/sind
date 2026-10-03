// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"encoding/csv"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// DefaultDataMountPath is the default mount path for the shared data volume.
const DefaultDataMountPath = "/data"

// RunTmpfsSize is the size of every node's /run tmpfs. /run holds systemd's
// runtime state, the daemons' sockets and pid files, and journald's
// volatile journal, which journald keeps to a tenth of the file system;
// without a size it could grow to half the host's memory, all of it
// charged to the node. systemd wants 16M free on /run to reload.
const RunTmpfsSize = "64m"

// Label keys used on sind containers.
//
// Docker merges the image's labels into a container's, so a label sind
// leaves out can come from the image. Every node container therefore gets
// each of these labels, with an empty value where there is nothing to
// record, and readers treat an empty or missing label alike, as nodes
// created by earlier sind versions lack some of them.
const (
	LabelRealm        = "sind.realm"
	LabelCluster      = "sind.cluster"
	LabelRole         = "sind.role"
	LabelManaged      = "sind.managed"
	LabelSlurmVersion = "sind.slurm.version"
	// LabelDataHostPath records the host directory the node bind-mounts as
	// its data (storage.dataStorage.hostPath), empty for the cluster's data
	// volume.
	LabelDataHostPath = "sind.data.hostpath"
	// LabelDataMountPath records the data mount point
	// (storage.dataStorage.mountPath, DefaultDataMountPath by default).
	LabelDataMountPath = "sind.data.mountpath"
	// LabelCVMFS records how the node mounts CVMFS (storage.cvmfs): "volume"
	// for the cvmfs volume plugin, "hostPath" for the Docker host's /cvmfs,
	// empty for none.
	LabelCVMFS = "sind.cvmfs"
	// LabelUsers and LabelGroups record the cluster users and groups
	// (users, groups), as space-separated entries (see LinuxUsers.Labels).
	LabelUsers  = "sind.users"
	LabelGroups = "sind.groups"
	// LabelIdentity records the identity mode (identity) of the cluster:
	// local, nssSlurm or clientIds.
	LabelIdentity = "sind.identity"
)

// IdentityFromLabels returns the identity mode a node container's
// LabelIdentity records, local without one.
func IdentityFromLabels(labels docker.Labels) config.IdentityMode {
	if mode := labels[LabelIdentity]; mode != "" {
		return config.IdentityMode(mode)
	}
	return config.IdentityLocal
}

// DataMountPath returns where a node container mounts the cluster's data:
// its LabelDataMountPath, or DefaultDataMountPath without one.
func DataMountPath(labels docker.Labels) string {
	if p := labels[LabelDataMountPath]; p != "" {
		return p
	}
	return DefaultDataMountPath
}

// ComposeProject returns the Docker Compose project name for a cluster.
func ComposeProject(realm, clusterName string) string {
	return realm + "-" + clusterName
}

// NodeLabels returns the standard labels for a node container.
// managed records whether sind manages Slurm on the node (see IsManaged).
// containerNumber is the 1-based instance number for compose compatibility.
// The slurm version label is always set, empty when sind does not know the
// version (unmanaged clusters), and so is the data host path label, empty in
// Docker volume mode: the image's labels would otherwise show through on the
// container, and the sind-node image carries a slurm version label.
func NodeLabels(realm, clusterName string, role config.Role, managed bool, slurmVersion, dataHostPath string, containerNumber int) docker.Labels {
	labels := docker.ComposeLabels(ComposeProject(realm, clusterName), string(role), containerNumber)
	labels[LabelRealm] = realm
	labels[LabelCluster] = clusterName
	labels[LabelRole] = string(role)
	labels[LabelManaged] = strconv.FormatBool(managed)
	labels[LabelSlurmVersion] = slurmVersion
	labels[LabelDataHostPath] = dataHostPath
	return labels
}

// IsManaged reports whether a node container's labels mark it as managed by
// sind: its Slurm configuration is sind's and sind runs its Slurm daemon.
// On a controller it tells whether the whole cluster is managed. Containers
// created before sind recorded LabelManaged count as managed.
func IsManaged(labels docker.Labels) bool {
	return labels[LabelManaged] != "false"
}

// RunConfig holds the parameters needed to build docker run arguments
// for creating a node container.
type RunConfig struct {
	Realm           string      // realm name (e.g. "sind")
	ClusterName     string      // cluster name
	ShortName       string      // node hostname: "controller", "worker-0"
	Role            config.Role // "controller", "db", "submitter", "worker"
	Image           string      // container image
	CPUs            int         // CPU limit
	Memory          string      // memory limit (e.g. "2g")
	TmpSize         string      // /tmp tmpfs size (e.g. "1g")
	SlurmVersion    string      // slurm version for labels (optional)
	DNSIP           string      // mesh DNS container IP (optional)
	DataHostPath    string      // host path for data volume (empty = use docker volume)
	DataMountPath   string      // mount point for data (default: /data)
	Managed         bool        // sind manages Slurm on the node: its configuration and daemon (see IsManaged)
	SharedState     bool        // mount the shared slurmctld state volume (controllers of a backup pair)
	ContainerNumber int         // 1-based compose container instance number
	Pull            bool        // force fresh image pull (--pull always)
	CapAdd          []string    // extra Linux capabilities (e.g. "SYS_ADMIN")
	CapDrop         []string    // dropped Linux capabilities
	Devices         []string    // host devices to expose (e.g. "/dev/fuse")
	SecurityOpt     []string    // extra security options

	// CVMFS is how the node mounts /cvmfs: config.StorageVolume for the
	// plugin volume, config.StorageHostPath for the host's /cvmfs, or empty
	// for none (see DetectCVMFS).
	CVMFS config.StorageType

	// Users are the cluster users and groups. The node records them in its
	// labels, and with users it mounts the home volume at HomeMountPath.
	Users LinuxUsers
	// AddUsers creates the users and groups on the node (see
	// nodeGetsUsers).
	AddUsers bool

	// Identity is the cluster's identity mode. With clientIds, munge is
	// masked and the node mounts no munge volume.
	Identity config.IdentityMode
	// NSSSlurm switches the node's passwd and group lookups to nss_slurm
	// first: managed workers with identity nssSlurm or clientIds.
	NSSSlurm bool

	// StoragePass is, on a managed db node, the StoragePass the slurmdbd
	// section sets: the password of slurmdbd's MariaDB account, which
	// authenticates with unix_socket without one (see accountingSQL).
	StoragePass string
}

// UserJobCapability is the capability workers of a cluster with users get,
// so that slurmstepd can bind the tasks of users other than root.
const UserJobCapability = "SYS_NICE"

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
	args = append(args, "-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeConfig))+":"+slurm.ConfDir+":"+configMode)
	if cfg.Identity != config.IdentityClientIDs {
		args = append(args, "-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeMunge))+":"+slurm.MungeDir+":ro")
	}

	// Data volume
	dataMountPath := cfg.DataMountPath
	if dataMountPath == "" {
		dataMountPath = DefaultDataMountPath
	}
	if cfg.DataHostPath != "" {
		args = append(args, "--mount", bindMount(cfg.DataHostPath, dataMountPath))
	} else {
		args = append(args, "-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeData))+":"+dataMountPath+":rw")
	}

	// Shared slurmctld state for a primary/backup controller pair. An empty
	// named volume is seeded from the image's directory, so it starts out
	// owned by the slurm user.
	if cfg.SharedState {
		args = append(args, "-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeState))+":"+slurm.StateSaveLocation+":rw")
	}

	// Home directories of the cluster users
	if len(cfg.Users.Users) > 0 {
		args = append(args, "-v", string(VolumeName(cfg.Realm, cfg.ClusterName, VolumeHome))+":"+HomeMountPath+":rw")
	}

	// CVMFS, read-only at /cvmfs (storage.cvmfs)
	args = append(args, cvmfsMountArgs(cfg.CVMFS)...)

	// tmpfs mounts: /tmp for user data, /run and /run/lock for systemd
	args = append(args,
		"--tmpfs", "/tmp:rw,nosuid,nodev,size="+cfg.TmpSize,
		"--tmpfs", "/run:exec,mode=755,size="+RunTmpfsSize,
		"--tmpfs", "/run/lock",
	)

	// Resource limits. The memory limit covers the node's own daemons and
	// the files in its tmpfs mounts (/tmp, /run, /dev/shm) as well as the
	// jobs. The node gets no swap, so it behaves the same on hosts with and
	// without swap, and a /dev/shm of half its memory, as a real node has,
	// rather than Docker's 64m.
	args = append(args,
		"--cpus", strconv.Itoa(cfg.CPUs),
		"--memory", cfg.Memory,
		"--memory-swap", cfg.Memory,
	)
	if memMB, err := slurm.ParseMemoryMB(cfg.Memory); err == nil {
		args = append(args, "--shm-size", strconv.Itoa(memMB/2)+"m")
	}

	// Security options for systemd containers
	args = append(args,
		"--cgroupns", "private",
		"--security-opt", "writable-cgroups=true",
		"--security-opt", "label=disable",
	)

	// Workers of a cluster with users may set the CPU affinity of other
	// users' processes: slurmstepd, as root, binds each task after the task
	// has become the job's user (task/affinity), which needs CAP_SYS_NICE
	// unless the user is root. Docker drops it by default.
	if cfg.Role == config.RoleWorker && len(cfg.Users.Users) > 0 {
		args = append(args, "--cap-add", UserJobCapability)
	}

	// Extra capabilities and devices (opt-in)
	for _, cap := range cfg.CapAdd {
		args = append(args, "--cap-add", cap)
	}
	for _, cap := range cfg.CapDrop {
		args = append(args, "--cap-drop", cap)
	}
	for _, dev := range cfg.Devices {
		args = append(args, "--device", dev)
	}
	for _, opt := range cfg.SecurityOpt {
		args = append(args, "--security-opt", opt)
	}

	// Labels, each one set even when empty (see the label keys)
	labels := NodeLabels(cfg.Realm, cfg.ClusterName, cfg.Role, cfg.Managed, cfg.SlurmVersion, cfg.DataHostPath, cfg.ContainerNumber)
	labels[LabelDataMountPath] = dataMountPath
	labels[LabelCVMFS] = string(cfg.CVMFS)
	maps.Copy(labels, cfg.Users.Labels())
	identity := cfg.Identity
	if identity == "" {
		identity = config.IdentityLocal
	}
	labels[LabelIdentity] = string(identity)
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

	// Entrypoint: delegate the cgroup controllers, then start systemd
	args = append(args, "--entrypoint", "/bin/sh")

	// Image, followed by the entrypoint's arguments
	entrypoint := NodeEntrypoint
	if cfg.Identity == config.IdentityClientIDs {
		entrypoint = MaskMunge + entrypoint
	}
	args = append(args, cfg.Image, "-c", entrypoint)

	return args
}

// bindMount returns the --mount value that bind-mounts the host directory
// source read-write at target. Unlike -v, which has Docker create a missing
// source as an empty root-owned directory, --mount fails on one. Docker
// reads the value as a CSV record, so a field with a comma or a quote is
// quoted.
func bindMount(source, target string) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	// Writing to a strings.Builder cannot fail.
	_ = w.Write([]string{"type=bind", "source=" + source, "target=" + target})
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

// MaskMunge is the line the node entrypoint starts with under identity
// clientIds: it masks munge.service before systemd starts, as auth/slurm
// replaces munge and the node has no munge key.
const MaskMunge = "ln -sf /dev/null /etc/systemd/system/munge.service\n"

// NodeEntrypoint is the shell script every node container starts with, as
// PID 1, before it execs systemd. It moves itself into init.scope and enables
// all available controllers in the root cgroup's cgroup.subtree_control.
//
// docker exec places its process in the container's root cgroup unless that
// cgroup has controllers enabled; runc then falls back to init's cgroup. A
// process in the root cgroup makes every later write to its
// cgroup.subtree_control fail with EBUSY (cgroup v2's no internal processes
// rule). Without this script, a docker exec of sind's that lands before
// systemd has enabled controllers keeps systemd from ever doing so, and
// daemons with Delegate=yes get none: slurmd then cannot use the memory or
// cpu controller, which jobacct_gather/cgroup needs. Each controller is
// enabled on its own, as one write of all of them fails as a whole if the
// kernel refuses one. Failures are ignored so the node still boots.
const NodeEntrypoint = `cg=/sys/fs/cgroup
mkdir -p $cg/init.scope && echo $$ > $cg/init.scope/cgroup.procs
for c in $(cat $cg/cgroup.controllers); do echo +$c > $cg/cgroup.subtree_control; done 2>/dev/null
exec /sbin/init`

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
// Worker nodes are indexed sequentially across all worker groups. In an
// unmanaged cluster every node is unmanaged; otherwise only workers and db
// nodes with managed: false are. cvmfs is the backend every node mounts CVMFS
// with (see DetectCVMFS), empty for none. The identity mode decides which
// nodes get the cluster users and groups (see nodeGetsUsers) and which
// resolve them with nss_slurm.
func NodeRunConfigs(cfg *config.Cluster, realm, dnsIP, slurmVersion string, cvmfs config.StorageType) []RunConfig {
	var configs []RunConfig
	users := NewLinuxUsers(cfg)
	hasSubmitter := slices.ContainsFunc(cfg.Nodes, func(n config.Node) bool { return n.Role == config.RoleSubmitter })
	workerIdx := 0
	clusterManaged := cfg.Managed()

	dataHostPath := ""
	dataMountPath := ""
	if cfg.Storage.DataStorage.UsesHostPath() {
		dataHostPath = absDataHostPath(cfg.Storage.DataStorage.HostPath)
	}
	if cfg.Storage.DataStorage.MountPath != "" {
		dataMountPath = cfg.Storage.DataStorage.MountPath
	}

	for _, n := range cfg.Nodes {
		switch n.Role {
		case config.RoleController, config.RoleDB, config.RoleSubmitter:
			nodeManaged := clusterManaged
			if n.Role == config.RoleDB && n.Managed != nil && !*n.Managed {
				nodeManaged = false
			}
			base := RunConfig{
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
				Managed:         nodeManaged,
				ContainerNumber: 1,
				Pull:            cfg.Pull,
				CapAdd:          n.CapAdd,
				CapDrop:         n.CapDrop,
				Devices:         n.Devices,
				SecurityOpt:     n.SecurityOpt,
				CVMFS:           cvmfs,
				Users:           users,
				AddUsers:        nodeGetsUsers(cfg.Identity, n.Role, nodeManaged, hasSubmitter),
				Identity:        cfg.Identity.Mode,
			}
			if n.Role == config.RoleDB && nodeManaged {
				base.StoragePass = slurmdbdStoragePass(cfg.Slurm.Slurmdbd)
			}
			if n.Role != config.RoleController || !n.BackupController {
				configs = append(configs, base)
				continue
			}
			// Primary/backup pair: identical containers sharing the
			// slurmctld state volume.
			base.SharedState = true
			backup := base
			backup.ShortName = ControllerBackupShortName
			backup.ContainerNumber = 2
			configs = append(configs, base, backup)
		case config.RoleWorker:
			count := n.Count
			if count <= 0 {
				count = 1
			}
			isManaged := clusterManaged && (n.Managed == nil || *n.Managed)
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
					CapAdd:          n.CapAdd,
					CapDrop:         n.CapDrop,
					Devices:         n.Devices,
					SecurityOpt:     n.SecurityOpt,
					CVMFS:           cvmfs,
					Users:           users,
					AddUsers:        nodeGetsUsers(cfg.Identity, config.RoleWorker, isManaged, hasSubmitter),
					Identity:        cfg.Identity.Mode,
					NSSSlurm:        isManaged && cfg.Identity.UsesNSSSlurm(),
				})
				workerIdx++
			}
		}
	}
	return configs
}

// absDataHostPath returns a data host path (storage.dataStorage.hostPath)
// made absolute against the working directory, as sind create cluster does
// before it validates the config, so that a library caller gets the same
// bind mount: Docker would read a bare relative name such as "data" as a
// named volume. The node labels then record an absolute path for sind create
// worker, which may run in another directory. If the working directory
// cannot be found, the path is left as it is, and Docker rejects it.
func absDataHostPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// DataPathWarning returns a warning when a cluster's data host path is the
// host's root directory or the user's home directory ($HOME), which every
// node then mounts read-write: typically by accident, from sind create
// cluster run there with the default --data . It returns "" for any other
// path.
func DataPathWarning(hostPath string) string {
	path := filepath.Clean(hostPath)
	home, homeErr := os.UserHomeDir()
	var what string
	switch {
	case path == "/":
		what = "the root directory /"
	case homeErr == nil && path == filepath.Clean(home):
		what = "your home directory " + path
	default:
		return ""
	}
	return "every node mounts " + what + " read-write as its data; use --data DIR, --data volume or storage.dataStorage to share less"
}

// nodeGetsUsers reports whether a node gets the cluster's Linux users and
// groups under an identity mode:
//
//   - local, and every unmanaged node (sind does not manage its Slurm):
//     yes.
//   - nssSlurm: every node but the workers, which resolve the job's user
//     with nss_slurm. The db node needs them for slurmdbd's own checks
//     (admin levels, coordinators).
//   - clientIds: the login node only, the submitter, or the controllers
//     without one, and the controllers too with controllerUsers. slurmctld
//     and slurmdbd learn the users from their tokens.
func nodeGetsUsers(identity config.Identity, role config.Role, managed, hasSubmitter bool) bool {
	if !managed {
		return true
	}
	switch identity.Mode {
	case config.IdentityNSSSlurm:
		return role != config.RoleWorker
	case config.IdentityClientIDs:
		switch role {
		case config.RoleSubmitter:
			return true
		case config.RoleController:
			return identity.ControllerUsers || !hasSubmitter
		default:
			return false
		}
	default:
		return true
	}
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
