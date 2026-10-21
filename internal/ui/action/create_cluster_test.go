// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valid is a form that builds; each case changes one field of it.
func valid(kv ...string) Form {
	f := form("name", "hpc", "workers", "2", "dir", "/scratch/hpc")
	for i := 0; i+1 < len(kv); i += 2 {
		f.Set(kv[i], kv[i+1])
	}
	return f
}

const wantMinimalConfig = `kind: Cluster
name: hpc
defaults:
  cpus: 1
nodes:
  - role: controller
  - role: worker
    count: 2
`

func TestBuildCreateCluster_Minimal(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid())
	require.NoError(t, err)

	assert.Equal(t, []string{"sind", "create", "cluster", "--config", "/state/sind/ui/configs/hpc.yaml", "--data", "/scratch/hpc"}, cmd.Argv)
	assert.Equal(t, "sind create cluster --config /state/sind/ui/configs/hpc.yaml --data /scratch/hpc", cmd.Display)
	require.NotNil(t, cmd.Config)
	assert.Equal(t, "/state/sind/ui/configs/hpc.yaml", cmd.Config.Path)
	assert.Equal(t, wantMinimalConfig, cmd.Config.Content)
	assert.Equal(t, []PlannedNode{{"controller", "controller"}, {"worker-0", "worker"}, {"worker-1", "worker"}}, cmd.Nodes)
	assert.Equal(t, "3 containers, 3 CPUs, 512m each.", cmd.Summary)
	assert.Empty(t, cmd.Warns)
	require.Len(t, cmd.Notes, 1)
	assert.Contains(t, cmd.Notes[0], "saves the config to /state/sind/ui/configs/hpc.yaml (mode 0600)")
}

func TestBuildCreateCluster_Everything(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid(
		"realm", "ci", "image", "ghcr.io/gsi-hpc/sind-node:26.05.4", "cpus", "2", "memory", "1g",
		"backup", "1", "db", "1", "api", "1", "submitter", "1", "workers", "3", "users", "alice, bob alice",
		"monitoring", "on", "pull", "on", "wait", "10m", "dtype", "hostPath", "dir", "~/sind/hpc"))
	require.NoError(t, err)

	assert.Equal(t, "sind --realm ci create cluster --config /state/sind/ui/configs/hpc.yaml --data /home/ada/sind/hpc --pull --wait 10m", cmd.Display)
	assert.Equal(t, `kind: Cluster
name: hpc
realm: ci
defaults:
  image: ghcr.io/gsi-hpc/sind-node:26.05.4
  cpus: 2
  memory: 1g
users:
  - name: alice
  - name: bob
nodes:
  - role: controller
    backupController: true
  - role: db
  - role: api
  - role: submitter
  - role: worker
    count: 3
monitoring: true
`, cmd.Config.Content)
	assert.Equal(t, "8 containers, 16 CPUs, 1g each.", cmd.Summary)
	assert.Contains(t, cmd.Nodes, PlannedNode{"controller-backup", "controller-backup"})
	assert.Contains(t, cmd.Nodes, PlannedNode{"api", "api"})
	require.Len(t, cmd.Notes, 2)
	assert.Contains(t, cmd.Notes[1], "need Slurm 26.05")
}

func TestBuildCreateCluster_Variants(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Form
		want string
	}{
		{"volume", valid("dtype", "volume", "dir", ""), "sind create cluster --config /state/sind/ui/configs/hpc.yaml --data volume"},
		{"wait default", valid("wait", "5m"), "sind create cluster --config /state/sind/ui/configs/hpc.yaml --data /scratch/hpc"},
		{"wait none", valid("wait", "0"), "sind create cluster --config /state/sind/ui/configs/hpc.yaml --data /scratch/hpc --wait 0"},
		{"dir cleaned", valid("dir", "/scratch//a/../hpc/"), "sind create cluster --config /state/sind/ui/configs/hpc.yaml --data /scratch/hpc"},
		{"hostPath implied", valid("dtype", ""), "sind create cluster --config /state/sind/ui/configs/hpc.yaml --data /scratch/hpc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cmd.Display)
		})
	}
}

func TestBuildCreateCluster_HomeDirectoryWarns(t *testing.T) {
	for _, dir := range []string{"~", "/home/ada", "/home/ada/"} {
		cmd, err := build(t, "create-cluster", valid("dir", dir))
		require.NoError(t, err)
		require.Len(t, cmd.Warns, 1, dir)
		assert.Contains(t, cmd.Warns[0], "/home/ada is your home directory")
		assert.Contains(t, cmd.Warns[0], "/home/ada/sind/hpc")
	}
	cmd, err := build(t, "create-cluster", valid("dir", "/home/ada/scratch"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Warns)
}

func TestBuildCreateCluster_MonitoringStackNote(t *testing.T) {
	st := testStore()
	st.SetMonitoring("sind", &cluster.Monitoring{})
	a, _ := Lookup("create-cluster")

	cmd, err := a.Build(valid("monitoring", "on"), st.ctx())
	require.NoError(t, err)
	assert.Contains(t, cmd.Notes, "Realm sind has a monitoring stack. sind adds the new nodes to it.")

	cmd, err = a.Build(valid(), st.ctx())
	require.NoError(t, err)
	assert.Len(t, cmd.Notes, 1)
}

func TestBuildCreateCluster_Errors(t *testing.T) {
	long := strings.Repeat("a", config.MaxAccountingClusterName+1)
	for _, tc := range []struct {
		name string
		f    Form
		want string
	}{
		{"realm", valid("realm", "No Realm"), "realm: invalid realm name"},
		{"name empty", valid("name", ""), "name: invalid cluster name"},
		{"name upper", valid("name", "Hpc"), "name: invalid cluster name"},
		{"name taken", valid("name", "dev"), "name: realm sind has a cluster dev already; pick another name, or delete that cluster first"},
		{"name free in another realm", valid("name", "ci1", "realm", "sind", "workers", "0"), "Workers: write a number from 1 to 64"},
		{"image", valid("image", "bad image"), "image: write an image reference"},
		{"image option", valid("image", "-v"), "image: write an image reference"},
		{"cpus", valid("cpus", "0"), "CPUs per node: write a number from 1 to 64"},
		{"memory", valid("memory", "lots"), `memory per node: invalid memory "lots"`},
		{"workers missing", valid("workers", ""), "Workers: write a number from 1 to 64"},
		{"workers many", valid("workers", "65"), "Workers: write a number from 1 to 64"},
		{"switch", valid("db", "2"), "db: write a number from 0 to 1"},
		{"user", valid("users", "Alice!"), "users: "},
		{"wait", valid("wait", "7m"), "wait for ready: pick one of 1m, 2m, 5m, 10m, 0"},
		{"data type", valid("dtype", "nfs"), `data: pick a directory or a Docker volume, not "nfs"`},
		{"dir empty", valid("dir", ""), "directory: say which directory"},
		{"dir relative", valid("dir", "data"), "directory: write the path in full, starting with / or ~"},
		{"dir control", valid("dir", "/a\x01b"), "directory: the path holds control characters"},
		{"validate", valid("name", long, "db", "1"), "config: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(t, "create-cluster", tc.f)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestBuildCreateCluster_ContextErrors(t *testing.T) {
	a, _ := Lookup("create-cluster")
	ctx := testContext()

	ctx.Home = ""
	_, err := a.Build(valid("dir", "~/x"), ctx)
	assert.EqualError(t, err, "directory: sind ui does not know your home directory; write the path in full")
	cmd, err := a.Build(valid("dir", "/home/ada"), ctx)
	require.NoError(t, err)
	assert.Empty(t, cmd.Warns, "without a home there is nothing to warn about")

	ctx = testContext()
	ctx.ConfigDir = ""
	_, err = a.Build(valid(), ctx)
	assert.ErrorContains(t, err, "no state directory")
}

func TestBuildCreateCluster_ConfigThatDoesNotParse(t *testing.T) {
	defer func(p func([]byte) (*config.Cluster, error)) { parseConfig = p }(parseConfig)
	parseConfig = func([]byte) (*config.Cluster, error) { return nil, errors.New("kind is required") }

	_, err := build(t, "create-cluster", valid())

	assert.EqualError(t, err, "config: kind is required")
}

func TestBuildCreateCluster_WarningsOfSind(t *testing.T) {
	defer func(p func([]byte) (*config.Cluster, error)) { parseConfig = p }(parseConfig)
	parseConfig = func(data []byte) (*config.Cluster, error) {
		c, err := config.Parse(data)
		c.Accounts = []config.Account{{Name: "a", Limits: config.Limits{"MaxJobs": "1"}}}
		return c, err
	}

	cmd, err := build(t, "create-cluster", valid("db", "1"))

	require.NoError(t, err)
	require.Len(t, cmd.Warns, 1)
	assert.Contains(t, cmd.Warns[0], "Slurm does not enforce the accounts' limits")
}
