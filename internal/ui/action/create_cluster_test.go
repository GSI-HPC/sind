// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valid is a form that builds; each case changes one field of it.
func valid(kv ...string) Form {
	f := form("name", "hpc", "w0-count", "2", "dir", "/scratch/hpc")
	for i := 0; i+1 < len(kv); i += 2 {
		f.Set(kv[i], kv[i+1])
	}
	return f
}

const wantMinimalConfig = `kind: Cluster
name: hpc
defaults:
  cpus: 1
  memory: 512m
storage:
  dataStorage:
    type: hostPath
    hostPath: /scratch/hpc
nodes:
  - role: controller
  - role: worker
    count: 2
`

func TestBuildCreateCluster_Minimal(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid())
	require.NoError(t, err)

	assert.Equal(t, []string{"sind", "create", "cluster", "--config", "/state/sind/ui/configs/hpc.yaml"}, cmd.Argv)
	assert.Equal(t, "sind create cluster --config /state/sind/ui/configs/hpc.yaml", cmd.Display)
	require.NotNil(t, cmd.Config)
	assert.Equal(t, "/state/sind/ui/configs/hpc.yaml", cmd.Config.Path)
	assert.Equal(t, wantMinimalConfig, cmd.Config.Content)
	assert.Equal(t, []PlannedNode{{"controller", "controller", "controller"}, {"worker-0", "worker", "w0"}, {"worker-1", "worker", "w0"}}, cmd.Nodes)
	assert.Equal(t, "3 containers, 3 CPUs, 1.5 GiB of memory.", cmd.Summary)
	assert.Empty(t, cmd.Warns)
	require.Len(t, cmd.Notes, 1)
	assert.Contains(t, cmd.Notes[0], "saves the config to /state/sind/ui/configs/hpc.yaml (mode 0600)")
}

func TestBuildCreateCluster_Everything(t *testing.T) {
	f := valid(
		"realm", "ci", "image", "ghcr.io/gsi-hpc/sind-node:26.05.4", "cpus", "2", "memory", "1g",
		"backup", "1", "db", "1", "api", "1", "submitter", "1", "w0-count", "3",
		"monitoring", "on", "pull", "on", "wait", "10m", "dtype", "hostPath", "dir", "~/sind/hpc")
	rows(f, UserRows, "u0", "name", "alice")
	rows(f, UserRows, "u1", "name", "bob")
	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)

	assert.Equal(t, "sind --realm ci create cluster --config /state/sind/ui/configs/hpc.yaml --pull --wait 10m", cmd.Display)
	assert.Equal(t, `kind: Cluster
name: hpc
realm: ci
defaults:
  image: ghcr.io/gsi-hpc/sind-node:26.05.4
  cpus: 2
  memory: 1g
storage:
  dataStorage:
    type: hostPath
    hostPath: /home/ada/sind/hpc
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
	assert.Equal(t, "8 containers, 16 CPUs, 8 GiB of memory.", cmd.Summary)
	assert.Contains(t, cmd.Nodes, PlannedNode{"controller-backup", "controller-backup", "backup"})
	assert.Contains(t, cmd.Nodes, PlannedNode{"api", "api", "api"})
	require.Len(t, cmd.Notes, 3)
	assert.Contains(t, cmd.Notes[1], "Monitoring needs Slurm 26.05 or later")
	assert.Contains(t, cmd.Notes[2], "The api node needs slurmrestd")
	assert.Empty(t, cmd.Problems)
}

func TestBuildCreateCluster_Variants(t *testing.T) {
	const cmdLine = "sind create cluster --config /state/sind/ui/configs/hpc.yaml"
	const hostPath = "storage:\n  dataStorage:\n    type: hostPath\n    hostPath: /scratch/hpc\n"
	for _, tc := range []struct {
		name          string
		f             Form
		want, storage string
	}{
		{"volume", valid("dtype", "volume", "dir", ""), cmdLine, "storage:\n  dataStorage:\n    type: volume\n"},
		{"volume at a path", valid("dtype", "volume", "mount", "/work/"), cmdLine, "storage:\n  dataStorage:\n    type: volume\n    mountPath: /work\n"},
		{"mount path", valid("mount", "/scratch"), cmdLine, "    hostPath: /scratch/hpc\n    mountPath: /scratch\n"},
		{"wait default", valid("wait", "5m"), cmdLine, hostPath},
		{"wait none", valid("wait", "0"), cmdLine + " --wait 0", hostPath},
		{"dir cleaned", valid("dir", "/scratch//a/../hpc/"), cmdLine, hostPath},
		{"hostPath implied", valid("dtype", ""), cmdLine, hostPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			assert.Empty(t, cmd.Problems)
			assert.Equal(t, tc.want, cmd.Display)
			assert.Contains(t, cmd.Config.Content, tc.storage)
		})
	}
}

func TestBuildCreateCluster_Wait(t *testing.T) {
	for wait, want := range map[string]string{
		"": "", "5m": "", "300s": "", "0": " --wait 0", "0s": " --wait 0", "7m": " --wait 7m", "other": "",
	} {
		f := valid("wait", wait)
		if wait == "other" {
			f = valid("wait", "other", "wait_other", "1h30m")
			want = " --wait 1h30m"
		}
		cmd, err := build(t, "create-cluster", f)
		require.NoError(t, err)
		assert.Empty(t, cmd.Problems, wait)
		assert.Equal(t, "sind create cluster --config /state/sind/ui/configs/hpc.yaml"+want, cmd.Display, wait)
	}
}

func TestPullByDefault(t *testing.T) {
	for image, want := range map[string]string{
		"":                                    "on: the tag latest can move to a newer image",
		"ghcr.io/gsi-hpc/sind-node":           "on: the tag latest can move to a newer image",
		"ghcr.io/gsi-hpc/sind-node:26.05":     "on: the tag 26.05 can move to a newer image",
		"localhost/sind-node:v26":             "on: the tag v26 can move to a newer image",
		"registry:5000/sind-node:latest":      "on: the tag latest can move to a newer image",
		"ghcr.io/gsi-hpc/sind-node:26.05.4":   "off: the tag 26.05.4 is fixed",
		"ghcr.io/gsi-hpc/sind-node:v0.12.0":   "off: the tag v0.12.0 is fixed",
		"ghcr.io/gsi-hpc/sind-node@sha256:ab": "off: the image names a digest, which is fixed",
		"sind-node:latest":                    "off: the image names no registry, as an image you built does",
		"gsi-hpc/sind-node:latest":            "off: the image names no registry, as an image you built does",
	} {
		pull, why := PullByDefault(image)
		got := "off: " + why
		if pull {
			got = "on: " + why
		}
		assert.Equal(t, want, got, image)
	}
}

func TestBuildCreateCluster_PullFollowsTheImage(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("pull_auto", "1", "pull", "on", "image", "ghcr.io/gsi-hpc/sind-node:26.05.4"))
	require.NoError(t, err)
	assert.NotContains(t, cmd.Display, "--pull", "the fixed tag wins over the box")

	cmd, err = build(t, "create-cluster", valid("pull_auto", "1"))
	require.NoError(t, err)
	assert.Contains(t, cmd.Display, "--pull", "the default image is latest")

	cmd, err = build(t, "create-cluster", valid("pull", "on", "image", "ghcr.io/gsi-hpc/sind-node:26.05.4"))
	require.NoError(t, err)
	assert.Contains(t, cmd.Display, "--pull", "without pull_auto, the box decides")
}

func TestBuildCreateCluster_Script(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("dir", "/scratch/missing it's", "mkdir", "on", "pull", "on", "wait", "0"))
	require.NoError(t, err)
	require.Empty(t, cmd.Problems)
	lines := strings.Split(cmd.Script, "\n")
	tag := strings.TrimPrefix(lines[2], `(umask 077 && cat >"$cfg") <<'`)
	tag = strings.TrimSuffix(tag, "'")
	assert.Regexp(t, `^SIND_CONFIG_[0-9a-f]{16}$`, tag)
	assert.Equal(t, `cfg="${XDG_STATE_HOME:-$HOME/.local/state}/sind/ui/configs/hpc.yaml"
mkdir -p "$(dirname "$cfg")"
(umask 077 && cat >"$cfg") <<'`+tag+`'
`+cmd.Config.Content+tag+`
mkdir -p '/scratch/missing it'\''s'
sind --realm sind create cluster --config "$cfg" --pull --wait 0
`, cmd.Script)

	cmd, err = build(t, "create-cluster", valid("realm", "ci", "name", "Bad"))
	require.NoError(t, err)
	assert.Contains(t, cmd.Script, "/configs/NAME.yaml\"\n")
	assert.True(t, strings.HasSuffix(cmd.Script, "\nsind --realm ci create cluster --config \"$cfg\"\n"))
}

func TestBuildCreateCluster_DataDirectory(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("dir", "/scratch/missing", "mkdir", "on"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Problems)
	assert.Equal(t, []string{"/scratch/missing"}, cmd.Dirs)
	assert.Contains(t, cmd.Notes, "sind ui creates the directory /scratch/missing first. It stays if the create fails.")

	cmd, err = build(t, "create-cluster", valid("dir", "/scratch/missing"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Dirs)
	assert.Equal(t, []Problem{{Field: "dir", Label: "Directory", Text: "/scratch/missing does not exist; tick Create the directory, or make it first"}}, cmd.Problems)

	cmd, err = build(t, "create-cluster", valid("dir", "/scratch/file", "mkdir", "on"))
	require.NoError(t, err)
	assert.Equal(t, []Problem{{Field: "dir", Label: "Directory", Text: "/scratch/file is not a directory"}}, cmd.Problems)

	cmd, err = build(t, "create-cluster", valid("dir", "/scratch/denied", "mkdir", "on"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Problems)
	assert.Empty(t, cmd.Dirs)
	assert.Equal(t, []string{"sind ui cannot check the directory /scratch/denied: permission denied. Docker needs it to exist."}, cmd.Warns)

	ctx := testContext()
	ctx.Stat = nil
	a, _ := Lookup("create-cluster")
	cmd, err = a.Build(valid("dir", "/scratch/missing", "mkdir", "on"), ctx)
	require.NoError(t, err)
	assert.Empty(t, cmd.Dirs, "without Stat, nothing is checked or made")
	assert.Empty(t, cmd.Problems)
}

func TestCommand_FingerprintCoversTheDirs(t *testing.T) {
	cmd := NewCommand("sind")
	with := cmd
	with.Dirs = []string{"/x"}
	assert.NotEqual(t, cmd.Fingerprint(), with.Fingerprint())
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

func TestBuildCreateCluster_HostMemory(t *testing.T) {
	st := testStore()
	st.SetDocker(store.DockerState{Checked: true, MemTotal: 4 << 30})
	a, _ := Lookup("create-cluster")

	cmd, err := a.Build(valid("w0-count", "4", "size", "m"), st.ctx())
	require.NoError(t, err)
	assert.Equal(t, 5*1024, cmd.MemoryMB)
	assert.Equal(t, 5, cmd.Containers, "controller and four workers")
	assert.Equal(t, 10, cmd.CPUs)
	assert.True(t, cmd.OverHost)
	assert.Equal(t, []Problem{{Field: "overcommit", Label: "Memory", Text: "the nodes may use 5 GiB, more than the 4 GiB of the Docker host; give them less memory, or tick Create anyway"}}, cmd.Problems)
	assert.Empty(t, cmd.Warns, "the problem says it")

	cmd, err = a.Build(valid("w0-count", "4", "size", "m", "overcommit", "on"), st.ctx())
	require.NoError(t, err)
	assert.True(t, cmd.OverHost, "the plan keeps the box")
	assert.Empty(t, cmd.Problems)
	assert.Equal(t, []string{"The nodes may use 5 GiB of memory, 125% of the 4 GiB of the Docker host, which then can run out of memory."}, cmd.Warns)

	cmd, err = a.Build(valid("w0-count", "3", "size", "m"), st.ctx())
	require.NoError(t, err)
	assert.False(t, cmd.OverHost)
	assert.Equal(t, []string{"The nodes may use 4 GiB of memory, 100% of the 4 GiB of the Docker host, which then can run out of memory."}, cmd.Warns, "all of it is no problem yet")

	cmd, err = a.Build(valid("w0-count", "2", "size", "m"), st.ctx())
	require.NoError(t, err)
	assert.Empty(t, cmd.Warns, "3 GiB is 75%")

	cmd, err = build(t, "create-cluster", valid("w0-count", "400", "size", "l"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Warns, "a host of unknown size")
	assert.False(t, cmd.OverHost)
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

func TestBuildCreateCluster_Problems(t *testing.T) {
	long := strings.Repeat("a", config.MaxAccountingClusterName+1)
	for _, tc := range []struct {
		name  string
		f     Form
		field string
		want  string
		def   bool
	}{
		{"realm", valid("realm", "No Realm"), "realm", "use only lowercase letters, digits and -, no spaces, such as no-realm", false},
		{"name empty", valid("name", ""), "name", "give the cluster a name, such as hpc", false},
		{"name upper", valid("name", "Hpc"), "name", "use lowercase letters, such as hpc", false},
		{"name space", valid("name", "My Cluster"), "name", "use only lowercase letters, digits and -, no spaces, such as my-cluster", false},
		{"name taken", valid("name", "dev"), "name", "realm sind has a cluster dev already; pick another name, or delete that cluster first", false},
		{"name long with db", valid("name", long, "db", "on"), "name", "with a db node, use at most 40 characters", false},
		{"image", valid("image", "bad image"), "image", "use no spaces", true},
		{"image option", valid("image", "-v"), "image", "write an image reference", true},
		{"cpus", valid("cpus", "0"), "cpus", "write a number from 1 to 1024", true},
		{"memory", valid("memory", "lots"), "memory", `invalid memory "lots"`, true},
		{"workers missing", valid("w0-count", ""), "w0-count", "write a number from 1 to 1024", true},
		{"workers many", valid("w0-count", "1025"), "w0-count", "write a number from 1 to 1024", true},
		{"checkbox", valid("db", "2"), "db", "tick the box or leave it empty", true},
		{"wait", valid("wait", "soon"), "wait", "write a time such as 15m", true},
		{"wait negative", valid("wait", "-1m"), "wait", "write a time such as 15m", true},
		{"wait other empty", valid("wait", "other"), "wait_other", "write a time such as 15m", true},
		{"wait other", valid("wait", "other", "wait_other", "1 hour"), "wait_other", "write a time such as 15m", true},
		{"data type", valid("dtype", "nfs"), "dtype", `pick a directory or a Docker volume, not "nfs"`, false},
		{"dir empty", valid("dir", ""), "dir", "say which directory", false},
		{"dir relative", valid("dir", "data"), "dir", "write the path in full, starting with / or ~", false},
		{"dir control", valid("dir", "/a\x01b"), "dir", "the path holds control characters", false},
		{"mkdir", valid("mkdir", "yes"), "mkdir", "tick the box", true},
		{"mount relative", valid("mount", "data"), "mount", "write a path in the nodes that starts with /", true},
		{"mount comma", valid("mount", "/a,b"), "mount", "write a path in the nodes", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			require.Len(t, cmd.Problems, 1, "%+v", cmd.Problems)
			pr := cmd.Problems[0]
			assert.Equal(t, tc.field, pr.Field)
			assert.Contains(t, pr.Text, tc.want)
			assert.Equal(t, tc.def, pr.Default)
			assert.NotEmpty(t, pr.Label)
			require.NotNil(t, cmd.Config, "the plan shows also with a problem")
			assert.Contains(t, cmd.Config.Content, "kind: Cluster")
			assert.NotEmpty(t, cmd.Nodes)
		})
	}
}

func TestBuildCreateCluster_PlanTakesTheDefaultsOfWrongFields(t *testing.T) {
	f := valid("cpus", "x", "memory", "8 gigs", "w0-count", "x", "image", "bad image", "wait", "7 min")
	rows(f, UserRows, "u0", "name", "ada")
	rows(f, UserRows, "u1", "name", "Bad")
	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)

	assert.Equal(t, "sind create cluster --config /state/sind/ui/configs/hpc.yaml", cmd.Display)
	assert.Equal(t, `kind: Cluster
name: hpc
defaults:
  cpus: 1
storage:
  dataStorage:
    type: hostPath
    hostPath: /scratch/hpc
users:
  - name: ada
nodes:
  - role: controller
  - role: worker
    count: 1
`, cmd.Config.Content)
	var fields []string
	for _, p := range cmd.Problems {
		fields = append(fields, p.Field)
	}
	assert.Equal(t, []string{"image", "w0-count", "cpus", "memory", "u1-name", "wait"}, fields, "in the order of the dialog")
}

func TestBuildCreateCluster_WrongNameKeepsThePlan(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("name", "../x"))
	require.NoError(t, err)

	assert.Equal(t, "/state/sind/ui/configs/NAME.yaml", cmd.Config.Path, "never a path from a name sind refuses")
	assert.Contains(t, cmd.Config.Content, "name: ../x")
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, "name", cmd.Problems[0].Field)
}

func TestBuildCreateCluster_ConfigThatSindRefuses(t *testing.T) {
	defer func(p func([]byte) (*config.Cluster, error)) { parseConfig = p }(parseConfig)
	parseConfig = func(data []byte) (*config.Cluster, error) {
		c, err := config.Parse(data)
		c.Nodes = append(c.Nodes, config.Node{Role: "bogus"}, config.Node{Role: config.RoleController})
		return c, err
	}

	cmd, err := build(t, "create-cluster", valid("name", "Bad"))

	require.NoError(t, err)
	require.Len(t, cmd.Problems, 3)
	assert.Equal(t, "name", cmd.Problems[0].Field)
	assert.Equal(t, Problem{Label: "Config", Text: `invalid role "bogus", must be one of: controller, db, api, submitter, worker`}, cmd.Problems[1],
		"the check takes a valid name in place of the wrong one, and finds the rest")
	assert.Equal(t, Problem{Label: "Config", Text: "exactly one controller required, got 2"}, cmd.Problems[2], "all of them")
}

func TestBuildCreateCluster_ContextErrors(t *testing.T) {
	a, _ := Lookup("create-cluster")
	ctx := testContext()

	ctx.Home = ""
	cmd, err := a.Build(valid("dir", "~/x"), ctx)
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, Problem{Field: "dir", Label: "Directory", Text: "sind ui does not know your home directory; write the path in full"}, cmd.Problems[0])
	cmd, err = a.Build(valid("dir", "/home/ada"), ctx)
	require.NoError(t, err)
	assert.Empty(t, cmd.Warns, "without a home there is nothing to warn about")

	ctx = testContext()
	ctx.ConfigDir = ""
	_, err = a.Build(valid(), ctx)
	assert.ErrorContains(t, err, "no state directory")
}

func TestFreeName(t *testing.T) {
	snap := testStore().ctx().Snap
	assert.Equal(t, "hpc", FreeName(snap, "sind", "hpc"))
	assert.Equal(t, "dev-2", FreeName(snap, "sind", "dev"))
	assert.Equal(t, "dev", FreeName(snap, "other", "dev"))
}

func TestBuildCreateCluster_ConfigThatDoesNotParse(t *testing.T) {
	defer func(p func([]byte) (*config.Cluster, error)) { parseConfig = p }(parseConfig)
	parseConfig = func([]byte) (*config.Cluster, error) { return nil, errors.New("kind is required") }

	cmd, err := build(t, "create-cluster", valid())

	require.NoError(t, err)
	assert.Equal(t, []Problem{{Label: "Config", Text: "kind is required"}}, cmd.Problems)
	assert.Contains(t, cmd.Config.Content, "name: hpc", "the plan shows what does not parse")
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
