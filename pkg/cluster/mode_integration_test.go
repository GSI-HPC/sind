// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cluster

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/GSI-HPC/sind/pkg/mesh"
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

	// Delete.
	err = Delete(ctx, c, meshMgr, clusterName)
	require.NoError(t, err)

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
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
      SchedulerParameters=bf_continue
    resources: |
      SelectType=select/cons_tres
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

	t.Logf("docker I/O:\n%s", rec.Dump())
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
	target, err := EnterTarget(ctx, c, realm, clusterName)
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
