// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cluster

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterCreateDeleteLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-cluster")
	clusterName := "it-cluster"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	err := meshMgr.EnsureMesh(ctx)
	require.NoError(t, err)

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	// Create.
	result, err := Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)
	assert.Equal(t, clusterName, result.Name)
	assert.Equal(t, StateRunning, result.State)
	require.Len(t, result.Nodes, 2)

	// GetClusters.
	clusters, err := GetClusters(ctx, c, realm)
	require.NoError(t, err)
	var found bool
	for _, cl := range clusters {
		if cl.Name == clusterName {
			found = true
			assert.Equal(t, 2, cl.NodeCount)
		}
	}
	assert.True(t, found, "cluster should appear in GetClusters")

	// GetNodes.
	nodes, err := GetNodes(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Len(t, nodes, 2)

	// GetStatus and ListClusterResources find the network and the volumes
	// with one listing each.
	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.True(t, status.Network.Cluster)
	require.Len(t, status.Mounts, 3)
	for _, m := range status.Mounts {
		assert.True(t, m.OK, "mount %s present", m.Path)
	}
	res, err := ListClusterResources(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.True(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{
		VolumeName(realm, clusterName, VolumeConfig),
		VolumeName(realm, clusterName, VolumeMunge),
		VolumeName(realm, clusterName, VolumeData),
	}, res.Volumes)

	// The default 512m node has a 256m /dev/shm, a 64m /run and no swap.
	worker := ContainerName(realm, clusterName, "worker-0")
	out, err := c.Exec(ctx, worker, "df", "--output=size", "-BM", "/dev/shm", "/run")
	require.NoError(t, err)
	assert.Equal(t, []string{"1M-blocks", "256M", "64M"}, strings.Fields(out))
	out, err = c.Exec(ctx, worker, "cat", "/sys/fs/cgroup/memory.swap.max")
	if err == nil { // only where the kernel accounts swap
		assert.Equal(t, "0", strings.TrimSpace(out))
	}

	// A managed worker stays while the controller is frozen: sind could
	// not take it out of sind-nodes.conf.
	require.NoError(t, PowerFreeze(ctx, c, realm, clusterName, []string{"controller"}))
	err = WorkerRemove(ctx, c, meshMgr, clusterName, []string{"worker-0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not running (paused)")
	exists, err := c.ContainerExists(ctx, ContainerName(realm, clusterName, "worker-0"))
	require.NoError(t, err)
	assert.True(t, exists)
	require.NoError(t, PowerUnfreeze(ctx, c, realm, clusterName, []string{"controller"}))

	// Delete.
	err = Delete(ctx, c, meshMgr, clusterName)
	require.NoError(t, err)

	// The network and the volumes are gone too.
	res, err = ListClusterResources(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Empty(t, res.Containers)
	assert.False(t, res.NetworkExists)
	assert.Empty(t, res.Volumes)

	// Verify gone.
	clusters, err = GetClusters(ctx, c, realm)
	require.NoError(t, err)
	for _, cl := range clusters {
		assert.NotEqual(t, clusterName, cl.Name)
	}

	t.Logf("docker I/O:\n%s", rec.Dump())
}

const probeInterval = 500 * time.Millisecond

func TestSlurmSectionApplied(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-slurm-sect")
	clusterName := "it-slurm-sect"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	err := meshMgr.EnsureMesh(ctx)
	require.NoError(t, err)

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
slurm:
  main: |
    MaxNodeCount=10
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
    PartitionName=DEFAULT DefaultTime=00:30:00
    PartitionName=debug Nodes=worker-0 Default=YES MaxTime=01:00:00
  cgroup: |
    ConstrainCores=yes
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	result, err := Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, result.State)

	// Verify settings via scontrol show config on the controller.
	controller := ContainerName(realm, clusterName, "controller")
	out, err := c.Exec(ctx, controller, "scontrol", "show", "config")
	require.NoError(t, err)

	assert.Contains(t, out, "MaxNodeCount            = 10")
	assert.Contains(t, out, "SelectType              = select/cons_tres")
	assert.Contains(t, out, "SelectTypeParameters    = CR_CORE_MEMORY")
	assert.Contains(t, out, "ConstrainCores          = yes")
	// A job without --mem gets 512 MB per CPU, not the whole node.
	assert.Contains(t, out, fmt.Sprintf("%-23s = %s", "DefMemPerCPU", "512"))

	// sind-nodes.conf comes after slurm.main: its PartitionName=DEFAULT
	// line applies to partition all, its partition may name sind's nodes,
	// and its default partition stays the default.
	out, err = c.Exec(ctx, controller, "scontrol", "show", "partition", "all")
	require.NoError(t, err)
	assert.Contains(t, out, "DefaultTime=00:30:00")
	assert.Contains(t, out, "Default=NO")
	out, err = c.Exec(ctx, controller, "scontrol", "show", "partition", "debug")
	require.NoError(t, err)
	assert.Contains(t, out, "Default=YES")
	assert.Contains(t, out, "Nodes=worker-0")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestSlurmSectionMapFormApplied(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-slurm-map")
	clusterName := "it-slurm-map"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	err := meshMgr.EnsureMesh(ctx)
	require.NoError(t, err)

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
users: [alice]
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
      SchedulerParameters=bf_continue
    resources: |
      SelectType=select/cons_tres
    tasks: |
      TaskPlugin=task/cgroup,task/affinity
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	result, err := Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, result.State)

	// Verify settings via scontrol show config on the controller.
	controller := ContainerName(realm, clusterName, "controller")
	out, err := c.Exec(ctx, controller, "scontrol", "show", "config")
	require.NoError(t, err)

	assert.Contains(t, out, "SchedulerType           = sched/backfill")
	assert.Contains(t, out, "SchedulerParameters     = bf_continue")
	assert.Contains(t, out, "SelectType              = select/cons_tres")
	assert.Contains(t, out, "TaskPlugin              = task/cgroup,task/affinity")

	// The fragment's TaskPlugin replaces sind's instead of following it.
	conf, err := c.ReadFile(ctx, controller, slurm.SlurmConfPath)
	require.NoError(t, err)
	assert.NotContains(t, conf, "TaskPlugin=")

	// task/affinity binds the tasks of users other than root, which needs
	// CAP_SYS_NICE on the worker: a job of alice runs.
	worker := ContainerName(realm, clusterName, "worker-0")
	assert.True(t, hasSysNice(t, c, worker))
	assert.False(t, hasSysNice(t, c, controller))
	waitNodeIdle(t, c, controller, "worker-0")
	out, err = c.Exec(ctx, controller, "runuser", "-u", "alice", "--", "srun", "-N1", "id", "-un")
	require.NoError(t, err)
	assert.Equal(t, "alice", strings.TrimSpace(out))

	// A worker added later gets it too: sind reads the TaskPlugin from the
	// fragment slurm.conf includes.
	added, err := WorkerAdd(ctx, c, meshMgr, WorkerAddOptions{ClusterName: clusterName, Count: 1}, probeInterval)
	require.NoError(t, err)
	require.Len(t, added, 1)
	assert.True(t, hasSysNice(t, c, ContainerName(realm, clusterName, added[0].Name)))

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// hasSysNice reports whether a node container has CAP_SYS_NICE (bit 23)
// in its bounding set.
func hasSysNice(t *testing.T, c *docker.Client, container docker.ContainerName) bool {
	t.Helper()
	out, err := c.Exec(t.Context(), container, "grep", "CapBnd", "/proc/1/status")
	require.NoError(t, err)
	fields := strings.Fields(out)
	require.Len(t, fields, 2, out)
	caps, err := strconv.ParseUint(fields[1], 16, 64)
	require.NoError(t, err)
	return caps&(1<<23) != 0
}

// TestControllerPairFailover exercises a primary/backup controller pair:
// graceful takeover and hand-back, then an outage of the primary with a
// worker added through the backup, then recovery of the primary.
func TestControllerPairFailover(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-ha")
	clusterName := "it-ha"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	// A short SlurmctldTimeout keeps the outage takeover quick.
	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
nodes:
  - role: controller
    backupController: true
  - worker
slurm:
  main: |
    SlurmctldTimeout=10
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	result, err := Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)
	require.Len(t, result.Nodes, 3)

	primary := ContainerName(realm, clusterName, "controller")
	backup := ContainerName(realm, clusterName, ControllerBackupShortName)
	worker := ContainerName(realm, clusterName, "worker-0")

	out, err := c.Exec(ctx, primary, "scontrol", "show", "config")
	require.NoError(t, err)
	assert.Contains(t, out, "SlurmctldHost[1]        = controller-backup")
	assert.Contains(t, out, "SlurmctldTimeout        = 10 sec")

	waitInControl(t, c, realm, clusterName, "controller")
	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	var stateMount bool
	for _, m := range status.Mounts {
		stateMount = stateMount || (m.Source == string(VolumeName(realm, clusterName, VolumeState)) && m.OK)
	}
	assert.True(t, stateMount, "state volume listed and present")

	// A job submitted before the takeover survives it.
	out, err = c.Exec(ctx, worker, "sbatch", "--parsable", "--wrap", "sleep 600")
	require.NoError(t, err)
	jobID := strings.TrimSpace(out)

	// Graceful: the backup takes over and the primary's slurmctld exits.
	_, err = c.Exec(ctx, worker, "scontrol", "takeover")
	require.NoError(t, err)
	waitInControl(t, c, realm, clusterName, ControllerBackupShortName)
	target, _, err := EnterTarget(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Equal(t, ControllerBackupShortName, target)
	out, err = c.Exec(ctx, worker, "squeue", "-h", "-j", jobID, "-o", "%i")
	require.NoError(t, err)
	assert.Equal(t, jobID, strings.TrimSpace(out))

	// Hand-back: starting the primary's slurmctld reclaims control.
	_, err = c.Exec(ctx, primary, "systemctl", "start", "slurmctld")
	require.NoError(t, err)
	waitInControl(t, c, realm, clusterName, "controller")

	// Outage: the backup takes over after SlurmctldTimeout.
	require.NoError(t, PowerCut(ctx, c, realm, clusterName, []string{"controller"}))
	waitInControl(t, c, realm, clusterName, ControllerBackupShortName)
	out, err = c.Exec(ctx, worker, "squeue", "-h", "-j", jobID, "-o", "%i")
	require.NoError(t, err)
	assert.Equal(t, jobID, strings.TrimSpace(out))

	// Managed worker add goes through the backup while the primary is down.
	added, err := WorkerAdd(ctx, c, meshMgr, WorkerAddOptions{ClusterName: clusterName, Count: 1}, probeInterval)
	require.NoError(t, err)
	require.Len(t, added, 1)
	out, err = c.Exec(ctx, backup, "sinfo", "-h", "-N", "-o", "%N")
	require.NoError(t, err)
	assert.Contains(t, out, added[0].Name)

	// Recovery: the restarted primary takes control back.
	require.NoError(t, PowerOn(ctx, c, realm, clusterName, []string{"controller"}))
	waitInControl(t, c, realm, clusterName, "controller")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// TestClusterNamesAroundMesh creates two clusters in one realm whose
// networks sort on both sides of the mesh network (<realm>-alpha-net <
// <realm>-mesh < <realm>-test-net). Every node's hostname is also a DNS name
// on the mesh, so without the cluster network's gateway priority, test's
// nodes would resolve controller through the mesh, to both controllers.
func TestClusterNamesAroundMesh(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-names")
	meshMgr := mesh.NewManager(c, realm)
	clusters := []string{"alpha", "test"}

	t.Cleanup(func() {
		bg := context.Background()
		for _, name := range clusters {
			_ = Delete(bg, c, meshMgr, name)
		}
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))
	require.Less(t, string(NetworkName(realm, "alpha")), string(meshMgr.NetworkName()))
	require.Greater(t, string(NetworkName(realm, "test")), string(meshMgr.NetworkName()))

	// One create at a time: both rewrite the realm's Corefile.
	for _, name := range clusters {
		cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
`, name, img)))
		require.NoError(t, err)
		cfg.ApplyDefaults()
		require.NoError(t, cfg.Validate())
		_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
		require.NoError(t, err, "creating %s", name)
	}

	for _, name := range clusters {
		controller, err := c.InspectContainer(ctx, ContainerName(realm, name, "controller"))
		require.NoError(t, err)
		want := controller.IPs[NetworkName(realm, name)]
		require.NotEmpty(t, want)

		out, err := c.Exec(ctx, ContainerName(realm, name, "worker-0"), "getent", "hosts", "controller")
		require.NoError(t, err, "resolving controller in %s", name)
		var got []string
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if fields := strings.Fields(line); len(fields) > 0 {
				got = append(got, fields[0])
			}
		}
		assert.Equal(t, []string{want}, got, "controller in cluster %s", name)
	}

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// TestUnmanagedCluster creates an unmanaged cluster with a controller pair,
// a submitter and a worker, checks that sind leaves Slurm alone, then
// provisions Slurm by hand the way a user's Chef or Ansible run would.
func TestUnmanagedCluster(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-unmanaged")
	clusterName := "it-unmanaged"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
nodes:
  - role: controller
    managed: false
    backupController: true
  - db
  - submitter
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	result, err := Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)
	assert.Empty(t, result.SlurmVersion)
	require.Len(t, result.Nodes, 5)

	primary := ContainerName(realm, clusterName, "controller")
	backup := ContainerName(realm, clusterName, ControllerBackupShortName)
	submitter := ContainerName(realm, clusterName, "submitter")
	worker := ContainerName(realm, clusterName, "worker-0")

	// sind wrote no Slurm configuration and started no Slurm daemon.
	out, err := c.Exec(ctx, primary, "ls", "-A", slurm.ConfDir)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(out), "config volume stays empty")
	for _, n := range []docker.ContainerName{primary, backup} {
		out, err = c.ExecAllowNonZero(ctx, n, "systemctl", "is-active", "slurmctld")
		require.NoError(t, err)
		assert.NotEqual(t, "active", strings.TrimSpace(out), "slurmctld on %s", n)
	}
	out, err = c.ExecAllowNonZero(ctx, worker, "systemctl", "is-active", "slurmd")
	require.NoError(t, err)
	assert.NotEqual(t, "active", strings.TrimSpace(out), "slurmd on %s", worker)
	db := ContainerName(realm, clusterName, "db")
	for _, unit := range []string{"mariadb", "slurmdbd"} {
		out, err = c.ExecAllowNonZero(ctx, db, "systemctl", "is-active", unit)
		require.NoError(t, err)
		assert.NotEqual(t, "active", strings.TrimSpace(out), "%s on the bare db node", unit)
	}

	// The config volume is shared and writable on the controllers only.
	_, err = c.Exec(ctx, primary, "touch", slurm.ConfDir+"/from-controller")
	require.NoError(t, err)
	_, err = c.Exec(ctx, backup, "rm", slurm.ConfDir+"/from-controller")
	require.NoError(t, err, "the backup sees and edits the primary's file")
	for _, n := range []docker.ContainerName{submitter, worker} {
		_, err = c.Exec(ctx, n, "touch", slurm.ConfDir+"/from-elsewhere")
		assert.Error(t, err, "%s mounts the config volume read-only", n)
	}

	// Both controllers share the slurmctld state volume.
	_, err = c.Exec(ctx, primary, "touch", slurm.StateSaveLocation+"/from-primary")
	require.NoError(t, err)
	_, err = c.Exec(ctx, backup, "rm", slurm.StateSaveLocation+"/from-primary")
	require.NoError(t, err)

	// munge stays sind's: a credential from the worker decodes on the controller.
	cred, err := c.Exec(ctx, worker, "munge", "-n")
	require.NoError(t, err)
	_, err = c.Exec(ctx, primary, "sh", "-c", "echo '"+strings.TrimSpace(cred)+"' | unmunge")
	require.NoError(t, err)

	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Empty(t, status.SlurmVersion)
	require.Len(t, status.Nodes, 5)
	for _, n := range status.Nodes {
		assert.False(t, n.Managed, n.Name)
		assert.Nil(t, n.Health.HA, n.Name)
		assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true}, n.Health.Services, n.Name)
	}

	// Workers added later are unmanaged too.
	added, err := WorkerAdd(ctx, c, meshMgr, WorkerAddOptions{ClusterName: clusterName, Count: 1}, probeInterval)
	require.NoError(t, err)
	require.Len(t, added, 1)
	info, err := c.InspectContainer(ctx, ContainerName(realm, clusterName, added[0].Name))
	require.NoError(t, err)
	assert.False(t, IsManaged(info.Labels))

	// The blank slate is usable: configure and start Slurm by hand.
	require.NoError(t, c.WriteFile(ctx, primary, slurm.ConfDir+"/slurm.conf", "ClusterName="+clusterName+`
SlurmctldHost=controller
SlurmUser=slurm
StateSaveLocation=`+slurm.StateSaveLocation+`
SlurmdSpoolDir=/var/spool/slurmd
ProctrackType=proctrack/cgroup
TaskPlugin=task/cgroup,task/affinity
ReturnToService=2
NodeName=worker-0 CPUs=1 State=UNKNOWN
PartitionName=all Nodes=worker-0 Default=YES MaxTime=INFINITE State=UP
`))
	require.NoError(t, c.WriteFile(ctx, primary, slurm.ConfDir+"/cgroup.conf", "CgroupPlugin=autodetect\n"))
	_, err = c.Exec(ctx, primary, "systemctl", "start", "slurmctld")
	require.NoError(t, err)
	_, err = c.Exec(ctx, worker, "systemctl", "start", "slurmd")
	require.NoError(t, err)
	waitNodeIdle(t, c, submitter, "worker-0")
	out, err = c.Exec(ctx, submitter, "srun", "-N1", "hostname")
	require.NoError(t, err)
	assert.Equal(t, "worker-0", strings.TrimSpace(out))

	// Removing a worker leaves the user's configuration alone.
	conf, err := c.ReadFile(ctx, primary, slurm.ConfDir+"/slurm.conf")
	require.NoError(t, err)
	require.NoError(t, WorkerRemove(ctx, c, meshMgr, clusterName, []string{added[0].Name}))
	after, err := c.ReadFile(ctx, primary, slurm.ConfDir+"/slurm.conf")
	require.NoError(t, err)
	assert.Equal(t, conf, after)
	out, err = c.Exec(ctx, primary, "ls", "-A", slurm.ConfDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"cgroup.conf", "slurm.conf"}, strings.Fields(out))

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestDBNodeAccounting(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	// Bound the test so a db service stuck in startup fails here instead of
	// running into the package-wide go test timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-db")
	clusterName := "it-db"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
slurm:
  slurmdbd:
    debug: |
      DebugLevel=info
nodes:
  - controller
  - db
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	result, err := Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, result.State)
	require.Len(t, result.Nodes, 3)

	// slurmdbd.conf includes the slurmdbd section's fragments. It has the
	// ownership and mode slurmdbd insists on, and the fragments, which may
	// hold secrets such as a StoragePass, get the same protection.
	db := ContainerName(realm, clusterName, "db")
	out, err := c.Exec(ctx, db, "stat", "-c", "%U:%G %a %n", slurm.SlurmdbdConfPath,
		slurm.ConfDir+"/slurmdbd.conf.d", slurm.ConfDir+"/slurmdbd.conf.d/debug.conf")
	require.NoError(t, err)
	assert.Equal(t, "slurm:slurm 600 /etc/slurm/slurmdbd.conf\n"+
		"slurm:slurm 700 /etc/slurm/slurmdbd.conf.d\n"+
		"slurm:slurm 600 /etc/slurm/slurmdbd.conf.d/debug.conf", strings.TrimSpace(out))
	out, err = c.Exec(ctx, db, "cat", slurm.SlurmdbdConfPath)
	require.NoError(t, err)
	assert.Contains(t, out, "include /etc/slurm/slurmdbd.conf.d/debug.conf")

	// MariaDB's slurm account authenticates with unix_socket: the OS user
	// slurm, which slurmdbd runs as, logs in as it; no other user does,
	// not even root.
	_, err = c.Exec(ctx, db, "runuser", "-u", "slurm", "--", "mysql", "-u", "slurm", "slurm_acct_db", "-e", "SELECT 1")
	require.NoError(t, err)
	_, err = c.Exec(ctx, db, "mysql", "-u", "slurm", "slurm_acct_db", "-e", "SELECT 1")
	assert.Error(t, err, "root logged in as slurm")

	// The controller sends accounting data to slurmdbd on the db node.
	controller := ContainerName(realm, clusterName, "controller")
	out, err = c.Exec(ctx, controller, "scontrol", "show", "config")
	require.NoError(t, err)
	assert.Contains(t, out, "AccountingStorageType   = accounting_storage/slurmdbd")
	assert.Contains(t, out, "AccountingStorageHost   = db")
	assert.Contains(t, out, "JobAcctGatherType       = jobacct_gather/cgroup")

	// slurmctld registers the cluster with slurmdbd on startup; the
	// registration can land after slurmctld answers pings.
	assert.Eventually(t, func() bool {
		out, err := c.Exec(ctx, controller, "sacctmgr", "-n", "-P", "show", "cluster", "format=cluster")
		return err == nil && strings.Contains(out, clusterName)
	}, time.Minute, time.Second, "cluster %q not registered with slurmdbd", clusterName)

	// A finished job is recorded in the accounting database.
	waitNodeIdle(t, c, controller, "worker-0")
	out, err = c.Exec(ctx, controller, "srun", "-N1", "hostname")
	require.NoError(t, err)
	assert.Equal(t, "worker-0", strings.TrimSpace(out))
	assert.Eventually(t, func() bool {
		out, err := c.Exec(ctx, controller, "sacct", "-a", "-n", "-X", "-P", "-o", "JobName,State")
		return err == nil && strings.Contains(out, "hostname|COMPLETED")
	}, time.Minute, time.Second, "job not recorded by sacct")

	// get cluster reports mariadb and slurmdbd healthy on the db node.
	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	var dbNode *NodeStatus
	for _, n := range status.Nodes {
		if n.Role == config.RoleDB {
			dbNode = n
		}
	}
	require.NotNil(t, dbNode, "db node missing from status")
	assert.Equal(t, ServiceHealth{
		probe.ServiceMunge: true, probe.ServiceSSHD: true,
		probe.ServiceMariadb: true, probe.ServiceSlurmdbd: true,
	}, dbNode.Health.Services)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestUnmanagedDBNode(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-db-unmanaged")
	clusterName := "it-db-unmanaged"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
nodes:
  - controller
  - role: db
    managed: false
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)

	// sind configures no accounting and starts nothing on the db node.
	controller := ContainerName(realm, clusterName, "controller")
	conf, err := c.ReadFile(ctx, controller, slurm.ConfDir+"/slurm.conf")
	require.NoError(t, err)
	assert.NotContains(t, conf, "AccountingStorage")
	assert.NotContains(t, conf, "JobAcctGather")
	_, err = c.Exec(ctx, controller, "test", "-e", slurm.SlurmdbdConfPath)
	assert.Error(t, err, "no slurmdbd.conf")
	db := ContainerName(realm, clusterName, "db")
	for _, unit := range []string{"mariadb", "slurmdbd"} {
		out, err := c.ExecAllowNonZero(ctx, db, "systemctl", "is-active", unit)
		require.NoError(t, err)
		assert.NotEqual(t, "active", strings.TrimSpace(out), "%s on the unmanaged db node", unit)
	}

	// Slurm itself is sind's and runs jobs.
	waitNodeIdle(t, c, controller, "worker-0")
	out, err := c.Exec(ctx, controller, "srun", "-N1", "hostname")
	require.NoError(t, err)
	assert.Equal(t, "worker-0", strings.TrimSpace(out))

	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	var dbNode *NodeStatus
	for _, n := range status.Nodes {
		if n.Role == config.RoleDB {
			dbNode = n
		}
	}
	require.NotNil(t, dbNode, "db node missing from status")
	assert.False(t, dbNode.Managed)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true}, dbNode.Health.Services)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestClusterUsers(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-users")
	clusterName := "it-users"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
groups:
  - name: hpc
    gid: 3000
users:
  - name: alice
    groups: [hpc]
  - name: bob
    uid: 2001
    group: hpc
nodes:
  - controller
  - submitter
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)

	controller := ContainerName(realm, clusterName, "controller")
	submitter := ContainerName(realm, clusterName, "submitter")
	worker := ContainerName(realm, clusterName, "worker-0")

	// Every node has the users and groups, with the same IDs.
	for _, n := range []docker.ContainerName{controller, submitter, worker} {
		out, err := c.Exec(ctx, n, "id", "alice")
		require.NoError(t, err, n)
		assert.Equal(t, "uid=1000(alice) gid=1000(alice) groups=1000(alice),3000(hpc)", strings.TrimSpace(out), n)
		out, err = c.Exec(ctx, n, "id", "bob")
		require.NoError(t, err, n)
		assert.Equal(t, "uid=2001(bob) gid=3000(hpc) groups=3000(hpc)", strings.TrimSpace(out), n)
	}

	// The home directories are on the shared home volume and belong to
	// their users.
	out, err := c.Exec(ctx, worker, "stat", "-c", "%U:%G %a %n", "/home/alice", "/home/alice/.ssh", "/home/alice/.ssh/authorized_keys")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"alice:alice 700 /home/alice",
		"alice:alice 700 /home/alice/.ssh",
		"alice:alice 600 /home/alice/.ssh/authorized_keys",
	}, strings.Split(strings.TrimSpace(out), "\n"))

	// The realm's key logs in as a user through the SSH relay.
	out, err = c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-l", "bob", DNSName("worker-0", clusterName, realm), "id", "-un")
	require.NoError(t, err)
	assert.Equal(t, "bob", strings.TrimSpace(out))

	// A job runs as its user, and what it writes to its home directory on
	// the worker shows up on every node. With the default task/cgroup,
	// which binds no tasks, the worker needs no CAP_SYS_NICE for that.
	assert.False(t, hasSysNice(t, c, worker))
	waitNodeIdle(t, c, submitter, "worker-0")
	_, err = c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-l", "alice", DNSName("submitter", clusterName, realm), "srun -N1 sh -c 'id -un > from-job; id -Gn > groups-in-job'")
	require.NoError(t, err)
	out, err = c.Exec(ctx, controller, "stat", "-c", "%U", "/home/alice/from-job")
	require.NoError(t, err)
	assert.Equal(t, "alice", strings.TrimSpace(out))
	out, err = c.ReadFile(ctx, controller, "/home/alice/from-job")
	require.NoError(t, err)
	assert.Equal(t, "alice", strings.TrimSpace(out))
	out, err = c.ReadFile(ctx, controller, "/home/alice/groups-in-job")
	require.NoError(t, err)
	assert.Equal(t, "alice hpc", strings.TrimSpace(out))

	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Contains(t, status.Mounts, MountPoint{Path: HomeMountPath, Source: string(VolumeName(realm, clusterName, VolumeHome)), Type: config.StorageVolume, OK: true})

	// Workers added later get the users, groups and the shared homes.
	added, err := WorkerAdd(ctx, c, meshMgr, WorkerAddOptions{ClusterName: clusterName, Count: 1}, probeInterval)
	require.NoError(t, err)
	require.Len(t, added, 1)
	newWorker := ContainerName(realm, clusterName, added[0].Name)
	out, err = c.Exec(ctx, newWorker, "id", "alice")
	require.NoError(t, err)
	assert.Equal(t, "uid=1000(alice) gid=1000(alice) groups=1000(alice),3000(hpc)", strings.TrimSpace(out))
	// It has worker-0's shape: the same image, by ID, and limits.
	infos, err := c.InspectContainers(ctx, worker, newWorker)
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, infos[0].Image, infos[1].Image)
	assert.Equal(t, infos[0].HostConfig.NanoCPUs, infos[1].HostConfig.NanoCPUs)
	assert.Equal(t, infos[0].HostConfig.Memory, infos[1].HostConfig.Memory)
	assert.Equal(t, infos[0].HostConfig.Tmpfs["/tmp"], infos[1].HostConfig.Tmpfs["/tmp"])
	assert.Equal(t, infos[0].HostConfig.CapAdd, infos[1].HostConfig.CapAdd)
	out, err = c.Exec(ctx, newWorker, "id", "bob")
	require.NoError(t, err)
	assert.Equal(t, "uid=2001(bob) gid=3000(hpc) groups=3000(hpc)", strings.TrimSpace(out))
	out, err = c.ReadFile(ctx, newWorker, "/home/alice/from-job")
	require.NoError(t, err)
	assert.Equal(t, "alice", strings.TrimSpace(out))
	assert.False(t, hasSysNice(t, c, newWorker))

	// Deleting the cluster removes the home volume.
	require.NoError(t, Delete(ctx, c, meshMgr, clusterName))
	exists, err := c.VolumeExists(ctx, VolumeName(realm, clusterName, VolumeHome))
	require.NoError(t, err)
	assert.False(t, exists)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestClusterAccounts(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-accounts")
	clusterName := "it-accounts"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
slurm:
  main: |
    AccountingStorageEnforce=associations,limits
accounts:
  - name: physics
    limits:
      GrpTRES: cpu=4
  - name: theory
    parent: physics
    limits:
      MaxJobs: 1
users:
  - name: alice
    accounts: [theory]
  - name: bob
    accounts: [physics, theory]
    coordinator: [physics]
  - name: carol
    accounts: [physics]
    adminLevel: operator
  - dave
nodes:
  - controller
  - db
  - submitter
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)

	controller := ContainerName(realm, clusterName, "controller")
	sacctmgr := func(args ...string) []string {
		t.Helper()
		out, err := c.Exec(ctx, controller, append([]string{"sacctmgr", "-n", "-P", "show"}, args...)...)
		require.NoError(t, err)
		return strings.Split(strings.TrimSpace(out), "\n")
	}

	// The account tree, with the limits and the users' associations.
	assert.Subset(t, sacctmgr("assoc", "format=account,parentname"), []string{"physics|root", "theory|physics"})
	assert.Subset(t, sacctmgr("assoc", "format=account,user,grptres,maxjobs"), []string{"physics||cpu=4|", "theory|||1"})
	assert.Subset(t, sacctmgr("assoc", "format=account,user"), []string{"theory|alice", "physics|bob", "theory|bob", "physics|carol"})
	assert.ElementsMatch(t, []string{
		"alice|theory|None",
		"bob|physics|None",
		"carol|physics|Operator",
	}, sacctmgr("user", "names=alice,bob,carol", "format=user,defaultaccount,adminlevel"))
	// A coordinator of physics coordinates its sub-accounts too.
	assert.Equal(t, []string{"bob|physics,theory"}, sacctmgr("user", "names=bob", "withcoord", "format=user,coordinators"))

	// With enforcement, a user's job runs under their default account, and
	// a user without an association cannot submit.
	host := DNSName("submitter", clusterName, realm)
	waitNodeIdle(t, c, controller, "worker-0")
	_, err = c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-l", "alice", host, "srun -N1 true")
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		out, err := c.Exec(ctx, controller, "sacct", "-a", "-n", "-X", "-P", "-o", "User,Account,State")
		return err == nil && slices.Contains(strings.Split(strings.TrimSpace(out), "\n"), "alice|theory|COMPLETED")
	}, time.Minute, time.Second, "alice's job not recorded under theory")
	out, err := c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-l", "dave", host, "sbatch --wrap true 2>&1; echo rc=$?")
	require.NoError(t, err)
	assert.Contains(t, out, "Invalid account or account/partition combination specified")
	assert.Contains(t, out, "rc=1")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// slurmConfig returns scontrol show config on a node as a map from
// parameter to value.
func slurmConfig(ctx context.Context, t *testing.T, c *docker.Client, node docker.ContainerName) map[string]string {
	t.Helper()
	out, err := c.Exec(ctx, node, "scontrol", "show", "config")
	require.NoError(t, err)
	params := map[string]string{}
	for line := range strings.Lines(out) {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			params[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return params
}

// lookupStatus returns the exit status of getent passwd user on a node: 0
// when the node resolves the user, 2 when it does not.
func lookupStatus(ctx context.Context, t *testing.T, c *docker.Client, node docker.ContainerName, user string) string {
	t.Helper()
	out, err := c.ExecAllowNonZero(ctx, node, "sh", "-c", "getent passwd "+user+" >/dev/null; echo $?")
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

func TestIdentityNSSSlurm(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-nss")
	clusterName := "it-nss"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
identity: nssSlurm
groups:
  - name: hpc
    gid: 3000
users:
  - name: alice
    uid: 2001
    groups: [hpc]
nodes:
  - controller
  - submitter
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)

	controller := ContainerName(realm, clusterName, "controller")
	submitter := ContainerName(realm, clusterName, "submitter")
	worker := ContainerName(realm, clusterName, "worker-0")

	// The controller and the submitter have alice; the worker does not,
	// and looks users up with nss_slurm first.
	assert.Equal(t, "0", lookupStatus(ctx, t, c, controller, "alice"))
	assert.Equal(t, "0", lookupStatus(ctx, t, c, submitter, "alice"))
	assert.Equal(t, "2", lookupStatus(ctx, t, c, worker, "alice"))
	out, err := c.Exec(ctx, worker, "grep", "-E", "^(passwd|group):", "/etc/nsswitch.conf")
	require.NoError(t, err)
	for line := range strings.Lines(out) {
		assert.Regexp(t, `^(passwd|group): slurm `, line)
	}
	assert.Contains(t, slurmConfig(ctx, t, c, controller)["LaunchParameters"], "enable_nss_slurm")

	// Inside a job step, nss_slurm serves alice and her groups on the
	// worker, from the job credential.
	waitNodeIdle(t, c, controller, "worker-0")
	out, err = c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-l", "alice", DNSName("submitter", clusterName, realm),
		`srun -N1 sh -c 'id -un; id -Gn; stat -c %U "$HOME"'`)
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "alice hpc", "alice"}, strings.Split(strings.TrimSpace(out), "\n"))

	// Outside a job, the worker does not know alice, so she cannot log in.
	_, err = c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-o", "BatchMode=yes", "-l", "alice", DNSName("worker-0", clusterName, realm), "true")
	assert.Error(t, err, "SSH as alice to a worker")

	// Workers added later resolve users the same way.
	added, err := WorkerAdd(ctx, c, meshMgr, WorkerAddOptions{ClusterName: clusterName, Count: 1}, probeInterval)
	require.NoError(t, err)
	require.Len(t, added, 1)
	newWorker := ContainerName(realm, clusterName, added[0].Name)
	assert.Equal(t, "2", lookupStatus(ctx, t, c, newWorker, "alice"))
	_, err = c.Exec(ctx, newWorker, "grep", "-q", "^passwd: slurm ", "/etc/nsswitch.conf")
	assert.NoError(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestIdentityClientIDs(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}

	realm := testutil.Realm("it-clientids")
	clusterName := "it-clientids"
	meshMgr := mesh.NewManager(c, realm)

	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, clusterName)
		_ = meshMgr.CleanupMesh(bg)
	})

	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf(`
kind: Cluster
name: %s
defaults:
  image: %s
identity: clientIds
slurm:
  main: |
    AccountingStorageEnforce=associations
accounts:
  - physics
users:
  - name: alice
    uid: 2001
    accounts: [physics]
nodes:
  - controller
  - db
  - submitter
  - worker
`, clusterName, img)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	require.NoError(t, err)

	controller := ContainerName(realm, clusterName, "controller")
	db := ContainerName(realm, clusterName, "db")
	submitter := ContainerName(realm, clusterName, "submitter")
	worker := ContainerName(realm, clusterName, "worker-0")
	nodes := []docker.ContainerName{controller, db, submitter, worker}

	// auth/slurm with slurm.key replaces munge, which is masked everywhere.
	params := slurmConfig(ctx, t, c, controller)
	assert.Equal(t, "auth/slurm", params["AuthType"])
	assert.Equal(t, "cred/slurm", params["CredType"])
	assert.Contains(t, params["AuthInfo"], "use_client_ids")
	assert.Contains(t, params["LaunchParameters"], "enable_nss_slurm")
	out, err := c.Exec(ctx, controller, "stat", "-c", "%U:%G %a", "/etc/slurm/slurm.key")
	require.NoError(t, err)
	assert.Equal(t, "slurm:slurm 600", strings.TrimSpace(out))
	for _, n := range nodes {
		out, err := c.ExecAllowNonZero(ctx, n, "systemctl", "is-enabled", "munge")
		require.NoError(t, err)
		assert.Equal(t, "masked", strings.TrimSpace(out), n)
	}

	// Only the login node has alice. Root's client commands there get
	// their tokens from sackd.
	for _, n := range nodes {
		want := "2"
		if n == submitter {
			want = "0"
		}
		assert.Equal(t, want, lookupStatus(ctx, t, c, n, "alice"), n)
	}
	_, err = c.Exec(ctx, submitter, "squeue")
	require.NoError(t, err)

	// alice's job runs under her association although neither the
	// controller nor slurmdbd knows her: her identity comes with her token.
	// On the worker, nss_slurm serves it to the job.
	waitNodeIdle(t, c, controller, "worker-0")
	out, err = c.Exec(ctx, meshMgr.SSHContainerName(), "ssh", "-l", "alice", DNSName("submitter", clusterName, realm),
		`srun -N1 sh -c 'id -un; stat -c %U "$HOME"'`)
	require.NoError(t, err)
	assert.Equal(t, []string{"alice", "alice"}, strings.Split(strings.TrimSpace(out), "\n"))
	assert.Eventually(t, func() bool {
		out, err := c.Exec(ctx, controller, "sacct", "-a", "-n", "-X", "-P", "-o", "User,Account,State")
		return err == nil && slices.Contains(strings.Split(strings.TrimSpace(out), "\n"), "alice|physics|COMPLETED")
	}, time.Minute, time.Second, "alice's job not recorded under physics")

	// get cluster reports sackd instead of munge, and no munge volume.
	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	for _, n := range status.Nodes {
		assert.NotContains(t, n.Health.Services, probe.ServiceMunge, n.Name)
		if n.Role == config.RoleSubmitter {
			assert.Equal(t, ServiceHealth{probe.ServiceSSHD: true, probe.ServiceSackd: true}, n.Health.Services)
		}
	}
	for _, m := range status.Mounts {
		assert.NotEqual(t, slurm.MungeDir, m.Path)
	}
	key, err := GetAuthKey(ctx, c, realm, clusterName)
	require.NoError(t, err)
	assert.Equal(t, AuthSlurm, key.Type)
	assert.Len(t, key.Key, slurm.SlurmKeySize)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// waitNodeIdle polls sinfo on from until the Slurm node is idle.
func waitNodeIdle(t *testing.T, c *docker.Client, from docker.ContainerName, node string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		out, err := c.Exec(t.Context(), from, "sinfo", "-h", "-n", node, "-o", "%T")
		last = fmt.Sprintf("%q (err: %v)", out, err)
		if err == nil && strings.TrimSpace(out) == "idle" {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s not idle after 2m; last sinfo: %s", node, last)
}

// waitInControl polls GetStatus until the controller shortName is reported
// in control of the cluster.
func waitInControl(t *testing.T, c *docker.Client, realm, clusterName, shortName string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		status, err := GetStatus(t.Context(), c, realm, clusterName)
		if err == nil {
			last = ""
			for _, n := range status.Nodes {
				if n.Health.HA == nil {
					continue
				}
				last += fmt.Sprintf("%s=%+v ", n.Name, *n.Health.HA)
				if n.Name == shortName+"."+clusterName && n.Health.HA.InControl {
					return
				}
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s not in control after 2m; last HA state: %s", shortName, last)
}

func checkPrerequisites(t *testing.T, c *docker.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.ServerVersion(ctx)
	if err != nil {
		t.Skipf("cannot query Docker version: %v", err)
	}
	if vErr := doctor.CheckDockerVersion(version); vErr != nil {
		t.Skipf("%v", vErr)
	}
	_, _, hasNsd := doctor.CgroupInfo(afero.NewOsFs())
	if !hasNsd {
		t.Skip("host cgroup mount lacks nsdelegate")
	}
}
