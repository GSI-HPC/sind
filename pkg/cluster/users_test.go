// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testUsers are the users and groups of usersCfg: alice with a private
// group and hpc as a supplementary group, bob with hpc as his primary
// group.
var testUsers = LinuxUsers{
	Users: []LinuxUser{{Name: "alice", UID: 1000, GID: 1000}, {Name: "bob", UID: 2001, GID: 3000}},
	Groups: []LinuxGroup{
		{Name: "alice", GID: 1000},
		{Name: "hpc", GID: 3000, Members: []string{"alice"}},
	},
}

// testUsersLabels are the labels that record testUsers.
var testUsersLabels = docker.Labels{
	LabelUsers:  "alice:1000:1000 bob:2001:3000",
	LabelGroups: "alice:1000 hpc:3000:alice",
}

// testUsersScript is the addUsers script for testUsers.
const testUsersScript = `groupadd --gid 1000 alice
groupadd --gid 3000 hpc
useradd --uid 1000 --gid 1000 --groups hpc --home-dir /home/alice --no-create-home --shell /bin/bash alice
useradd --uid 2001 --gid 3000 --home-dir /home/bob --no-create-home --shell /bin/bash bob
`

// usersCfg returns createCfg with the users and groups of testUsers.
func usersCfg() *config.Cluster {
	cfg := createCfg()
	cfg.Users = []config.User{
		{Name: "alice", UID: 1000, Groups: []string{"hpc"}},
		{Name: "bob", UID: 2001, Group: "hpc"},
	}
	cfg.Groups = []config.Group{{Name: "hpc", GID: 3000}}
	return cfg
}

func TestHomeDir(t *testing.T) {
	assert.Equal(t, "/home/alice", HomeDir("alice"))
}

func TestNewLinuxUsers(t *testing.T) {
	assert.Equal(t, testUsers, NewLinuxUsers(usersCfg()))
	assert.True(t, NewLinuxUsers(createCfg()).IsEmpty())

	// Private groups come first, in user order; a group no user lists has
	// no members.
	cfg := createCfg()
	cfg.Users = []config.User{
		{Name: "carol", UID: 1001, Groups: []string{"physics", "hpc"}},
		{Name: "dave", UID: 1002, Groups: []string{"hpc"}},
		{Name: "erin", UID: 1003, Group: "physics"},
	}
	cfg.Groups = []config.Group{{Name: "hpc", GID: 3000}, {Name: "physics", GID: 3001}, {Name: "empty", GID: 3002}}
	assert.Equal(t, LinuxUsers{
		Users: []LinuxUser{
			{Name: "carol", UID: 1001, GID: 1001},
			{Name: "dave", UID: 1002, GID: 1002},
			{Name: "erin", UID: 1003, GID: 3001},
		},
		Groups: []LinuxGroup{
			{Name: "carol", GID: 1001},
			{Name: "dave", GID: 1002},
			{Name: "hpc", GID: 3000, Members: []string{"carol", "dave"}},
			{Name: "physics", GID: 3001, Members: []string{"carol"}},
			{Name: "empty", GID: 3002},
		},
	}, NewLinuxUsers(cfg))
}

func TestLinuxUsers_IsEmpty(t *testing.T) {
	assert.True(t, LinuxUsers{}.IsEmpty())
	assert.False(t, testUsers.IsEmpty())
	assert.False(t, LinuxUsers{Groups: []LinuxGroup{{Name: "hpc", GID: 3000}}}.IsEmpty())
}

func TestLinuxUsers_Labels(t *testing.T) {
	assert.Equal(t, testUsersLabels, testUsers.Labels())
	assert.Equal(t, docker.Labels{LabelUsers: "", LabelGroups: ""}, LinuxUsers{}.Labels(),
		"both set, empty, so that image labels cannot show through")
	assert.Equal(t, docker.Labels{LabelUsers: "", LabelGroups: "hpc:3000"}, LinuxUsers{Groups: []LinuxGroup{{Name: "hpc", GID: 3000}}}.Labels())

	users, err := LinuxUsersFromLabels(LinuxUsers{}.Labels())
	require.NoError(t, err)
	assert.True(t, users.IsEmpty(), "empty labels record no users")
}

func TestLinuxUsersFromLabels(t *testing.T) {
	users, err := LinuxUsersFromLabels(testUsersLabels)
	require.NoError(t, err)
	assert.Equal(t, testUsers, users)

	users, err = LinuxUsersFromLabels(docker.Labels{LabelRole: "worker"})
	require.NoError(t, err)
	assert.True(t, users.IsEmpty())

	for _, value := range []string{"alice", "alice:1000", "alice:1000:1000:x", ":1000:1000", "Alice:1000:1000", "alice:x:1000", "alice:1000:x", "alice:1000:1000,bob:2001:2001"} {
		_, err := LinuxUsersFromLabels(docker.Labels{LabelUsers: value})
		require.Error(t, err, value)
		assert.Equal(t, `invalid sind.users label "`+value+`": want name:uid:gid entries`, err.Error())
	}
	for _, value := range []string{"hpc", "hpc:3000:alice:bob", "-hpc:3000", "hpc:x", "hpc:3000:", "hpc:3000:alice+", "hpc:3000:Alice"} {
		_, err := LinuxUsersFromLabels(docker.Labels{LabelGroups: value})
		require.Error(t, err, value)
		assert.Equal(t, `invalid sind.groups label "`+value+`": want name:gid[:member+...] entries`, err.Error())
	}
}

func TestLinuxUsersFromLabels_RoundTripsDockerPs(t *testing.T) {
	// docker ps joins a container's labels with commas; the users and
	// groups labels must survive that.
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
		Labels: "sind.cluster=dev," + LabelGroups + "=" + testUsersLabels[LabelGroups] + "," + LabelUsers + "=" + testUsersLabels[LabelUsers] + ",sind.role=controller",
	}), "", nil)
	entries, err := docker.NewClient(&m).ListContainers(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)

	users, err := LinuxUsersFromLabels(entries[0].Labels)
	require.NoError(t, err)
	assert.Equal(t, testUsers, users)
}

func TestAddUsersScript(t *testing.T) {
	assert.Equal(t, testUsersScript, addUsersScript(testUsers))
	assert.Equal(t, "groupadd --gid 3000 hpc\n", addUsersScript(LinuxUsers{Groups: []LinuxGroup{{Name: "hpc", GID: 3000}}}))
}

func TestAddUsers(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)

	err := addUsers(t.Context(), docker.NewClient(&m), "sind-dev-worker-0", testUsers)

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "sh", "-ec", testUsersScript}, m.Calls[0].Args)
}

func TestAddUsers_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "groupadd: group 'alice' already exists\n", fmt.Errorf("exit status 9"))

	err := addUsers(t.Context(), docker.NewClient(&m), "sind-dev-worker-0", testUsers)

	require.Error(t, err)
	assert.Equal(t, "adding users: exit status 9", err.Error())
}

func TestCreateHomes(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)

	err := createHomes(t.Context(), docker.NewClient(&m), "sind-dev-controller", testUsers.Users, "ssh-ed25519 AAAA-key\n")

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "sind-dev-controller", "sh", "-ec", createHomesScript, "sh", "ssh-ed25519 AAAA-key", "alice", "1000", "1000", "bob", "2001", "3000"}, m.Calls[0].Args)
}

func TestCreateHomes_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))

	err := createHomes(t.Context(), docker.NewClient(&m), "sind-dev-controller", testUsers.Users, "ssh-ed25519 AAAA-key")

	require.Error(t, err)
	assert.Equal(t, "creating home directories: exit status 1", err.Error())
}

func TestBuildRunArgs_Users(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.Users = testUsers
	args := BuildRunArgs(cfg)

	assert.Contains(t, testutil.ArgValues(args, "-v"), "sind-dev-home:/home:rw")
	assert.Contains(t, testutil.ArgValues(args, "--label"), "sind.users=alice:1000:1000 bob:2001:3000")
	assert.Contains(t, testutil.ArgValues(args, "--label"), "sind.groups=alice:1000 hpc:3000:alice")
}

func TestBuildRunArgs_TaskAffinityCapability(t *testing.T) {
	// Workers whose tasks task/affinity binds get CAP_SYS_NICE, before
	// the configured capabilities, users or not; other nodes, and workers
	// without task/affinity, do not.
	worker := defaultRunConfig()
	worker.Role = config.RoleWorker
	worker.CapAdd = []string{"SYS_ADMIN"}
	worker.TaskAffinity = true
	assert.Equal(t, []string{"SYS_NICE", "SYS_ADMIN"}, testutil.ArgValues(BuildRunArgs(worker), "--cap-add"))

	controller := worker
	controller.Role = config.RoleController
	assert.Equal(t, []string{"SYS_ADMIN"}, testutil.ArgValues(BuildRunArgs(controller), "--cap-add"))

	withoutAffinity := worker
	withoutAffinity.TaskAffinity = false
	withoutAffinity.Users = testUsers
	assert.Equal(t, []string{"SYS_ADMIN"}, testutil.ArgValues(BuildRunArgs(withoutAffinity), "--cap-add"))

	// A capDrop of SYS_NICE or ALL wins: --cap-drop would not undo
	// --cap-add.
	for _, drop := range []string{"SYS_NICE", "ALL"} {
		dropped := worker
		dropped.CapDrop = []string{drop}
		assert.Equal(t, []string{"SYS_ADMIN"}, testutil.ArgValues(BuildRunArgs(dropped), "--cap-add"), drop)
	}
}

func TestBuildRunArgs_GroupsOnly(t *testing.T) {
	// Groups without users get no home volume.
	cfg := defaultRunConfig()
	cfg.Users = LinuxUsers{Groups: []LinuxGroup{{Name: "hpc", GID: 3000}}}
	args := BuildRunArgs(cfg)

	for _, v := range testutil.ArgValues(args, "-v") {
		assert.NotContains(t, v, ":/home:")
	}
	assert.Contains(t, testutil.ArgValues(args, "--label"), "sind.groups=hpc:3000")
}

func TestBuildRunArgs_NoUsers(t *testing.T) {
	args := BuildRunArgs(defaultRunConfig())

	for _, v := range testutil.ArgValues(args, "-v") {
		assert.NotContains(t, v, ":/home:")
	}
	labels := testutil.ArgValues(args, "--label")
	assert.Contains(t, labels, LabelUsers+"=")
	assert.Contains(t, labels, LabelGroups+"=")
}

func TestNodeRunConfigs_Users(t *testing.T) {
	// Every node gets the users: controllers of a backup pair, submitter
	// and all workers.
	cfg := usersCfg()
	cfg.Nodes = []config.Node{
		{Role: config.RoleController, BackupController: true},
		{Role: config.RoleSubmitter},
		{Role: config.RoleWorker, Count: 2},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 5)
	for _, c := range configs {
		assert.Equal(t, testUsers, c.Users, c.ShortName)
	}
}

func TestCreateResources_HomeVolume(t *testing.T) {
	assert.NotContains(t, createdVolumes(t, createCfg()), "sind-dev-home")
	assert.Contains(t, createdVolumes(t, usersCfg()), "sind-dev-home")
}

func TestPreflightCheck_HomeVolume(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, map[string]bool{"sind-dev-home": true})
	client := docker.NewClient(&m)

	// Without users, sind creates no home volume, so an existing one is
	// not in the way.
	require.NoError(t, PreflightCheck(t.Context(), client, mesh.DefaultRealm, minimalConfig()))

	cfg := minimalConfig()
	cfg.Users = []config.User{{Name: "alice", UID: 1000}}
	err := PreflightCheck(t.Context(), client, mesh.DefaultRealm, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume sind-dev-home")
}

// userCalls sorts the calls of a Create or WorkerAdd run that concern
// users: the containers that got the users with addUsers and the scripts
// they ran, the createHomes runs, and the node containers and their home
// mounts and users and groups labels.
type userCalls struct {
	added   map[string]string
	homes   [][]string
	mounts  map[string][]string
	labels  map[string][]string
	caps    map[string][]string
	volumes []string
}

func collectUserCalls(calls []mock.Call) *userCalls {
	uc := &userCalls{
		added:  map[string]string{},
		mounts: map[string][]string{},
		labels: map[string][]string{},
		caps:   map[string][]string{},
	}
	for _, c := range calls {
		a := c.Args
		switch {
		case a[0] == "volume" && a[1] == "create":
			uc.volumes = append(uc.volumes, a[len(a)-1])
		case a[0] == "create" && slices.Contains(a, "--hostname"):
			name := testutil.ArgValues(a, "--name")[0]
			for _, v := range testutil.ArgValues(a, "-v") {
				if v == "sind-dev-home:/home:rw" {
					uc.mounts[name] = append(uc.mounts[name], v)
				}
			}
			for _, l := range testutil.ArgValues(a, "--label") {
				if strings.HasPrefix(l, LabelUsers+"=") || strings.HasPrefix(l, LabelGroups+"=") {
					uc.labels[name] = append(uc.labels[name], l)
				}
			}
			if caps := testutil.ArgValues(a, "--cap-add"); len(caps) > 0 {
				uc.caps[name] = caps
			}
		case a[0] == "exec" && len(a) == 5 && a[3] == "-ec" && strings.HasPrefix(a[4], "groupadd "):
			uc.added[a[1]] = a[4]
		case a[0] == "exec" && len(a) > 5 && a[4] == createHomesScript:
			uc.homes = append(uc.homes, append([]string{a[1]}, a[6:]...))
		}
	}
	return uc
}

func TestCreate_Users(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, usersCfg(), time.Millisecond)
	require.NoError(t, err)

	uc := collectUserCalls(m.Calls)
	nodes := []string{"sind-dev-controller", "sind-dev-worker-0"}
	assert.Contains(t, uc.volumes, "sind-dev-home")
	assert.Equal(t, map[string]string{nodes[0]: testUsersScript, nodes[1]: testUsersScript}, uc.added)
	for _, n := range nodes {
		assert.Equal(t, []string{"sind-dev-home:/home:rw"}, uc.mounts[n], n)
		assert.Equal(t, []string{"sind.groups=alice:1000 hpc:3000:alice", "sind.users=alice:1000:1000 bob:2001:3000"}, uc.labels[n], n)
	}
	// Without task/affinity no job needs CAP_SYS_NICE.
	assert.Empty(t, uc.caps)
	// The home volume is shared: the homes are created once, with the
	// realm's SSH key.
	assert.Equal(t, [][]string{{"sind-dev-controller", "ssh-ed25519 AAAA-test-key", "alice", "1000", "1000", "bob", "2001", "3000"}}, uc.homes)
}

func TestCreate_TaskAffinity(t *testing.T) {
	// With task/affinity in slurm.main, the worker binds tasks and gets
	// CAP_SYS_NICE; the controller does not.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cfg := createCfg()
	cfg.Slurm.Main = config.Section{Content: "TaskPlugin=task/cgroup,task/affinity\n"}
	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, map[string][]string{"sind-dev-worker-0": {"SYS_NICE"}}, collectUserCalls(m.Calls).caps)
}

func TestCreate_NoUsers(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)
	require.NoError(t, err)

	uc := collectUserCalls(m.Calls)
	assert.NotContains(t, uc.volumes, "sind-dev-home")
	assert.Empty(t, uc.added)
	assert.Empty(t, uc.homes)
	assert.Empty(t, uc.mounts)
	assert.Equal(t, map[string][]string{
		"sind-dev-controller": {"sind.groups=", "sind.users="},
		"sind-dev-worker-0":   {"sind.groups=", "sind.users="},
	}, uc.labels, "empty, so that image labels cannot show through")
	assert.Empty(t, uc.caps)
}

func TestCreate_AddUsersFails(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && args[1] == "sind-dev-worker-0" && len(args) > 4 && args[4] == testUsersScript {
			return mock.Result{Stderr: "useradd: cannot lock /etc/passwd\n", Err: fmt.Errorf("exit status 1")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, usersCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "node worker-0: adding users: exit status 1")
}

func TestCreate_CreateHomesFails(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 4 && args[4] == createHomesScript {
			return mock.Result{Err: fmt.Errorf("exit status 1")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, usersCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating home directories: exit status 1")
}

// psWithLabels returns an OnCall function that lists a running controller
// with the given extra labels for docker ps and hands every other call to
// base.
func psWithLabels(base func([]string, string) mock.Result, labels string) func([]string, string) mock.Result {
	all := "sind.cluster=dev,sind.role=controller," + labels
	inspected := docker.Labels{LabelSlurmVersion: "25.11.0"}
	for pair := range strings.SplitSeq(all, ",") {
		k, v, _ := strings.Cut(pair, "=")
		inspected[k] = v
	}
	return func(args []string, stdin string) mock.Result {
		switch {
		case len(args) > 0 && args[0] == "ps":
			return mock.Result{Stdout: testutil.NDJSON(testutil.PsEntry{
				ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
				Labels: all,
			})}
		case len(args) > 1 && args[0] == "inspect" && args[1] == "sind-dev-controller":
			data, _ := json.Marshal([]map[string]any{{
				"Name": "/sind-dev-controller", "Image": testControllerImageID,
				"State": map[string]string{"Status": "running"}, "Config": map[string]any{"Labels": inspected},
			}})
			return mock.Result{Stdout: string(data)}
		}
		return base(args, stdin)
	}
}

func TestWorkerAdd_InheritsUsers(t *testing.T) {
	// New workers get the cluster's users and groups and mount the home
	// volume; the homes on it exist already.
	var m mock.Executor
	m.OnCall = psWithLabels(workerAddOnCall(t), "sind.users=alice:1000:1000 bob:2001:3000,sind.groups=alice:1000 hpc:3000:alice")
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	uc := collectUserCalls(m.Calls)
	worker := "sind-dev-worker-0"
	assert.Equal(t, map[string]string{worker: testUsersScript}, uc.added)
	assert.Equal(t, map[string][]string{worker: {"sind-dev-home:/home:rw"}}, uc.mounts)
	assert.Empty(t, uc.caps, "no task/affinity in slurm.conf")
	assert.Equal(t, map[string][]string{worker: {"sind.groups=alice:1000 hpc:3000:alice", "sind.users=alice:1000:1000 bob:2001:3000"}}, uc.labels)
	assert.Empty(t, uc.homes)
}

func TestWorkerAdd_TaskAffinity(t *testing.T) {
	// A worker added to a cluster whose slurm.conf enables task/affinity
	// gets CAP_SYS_NICE; sind reads it from the config volume.
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if slices.Equal(args, []string{"exec", "sind-dev-controller", "cat", "/etc/slurm/slurm.conf"}) {
			return mock.Result{Stdout: "ClusterName=dev\ninclude /etc/slurm/slurm.conf.d/tasks.conf\n"}
		}
		if slices.Equal(args, []string{"exec", "sind-dev-controller", "cat", "/etc/slurm/slurm.conf.d/tasks.conf"}) {
			return mock.Result{Stdout: "TaskPlugin=task/cgroup,task/affinity\n"}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, map[string][]string{"sind-dev-worker-1": {"SYS_NICE"}}, collectUserCalls(m.Calls).caps)
}

func TestWorkerAdd_ReadSlurmConfError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if slices.Equal(args, []string{"exec", "sind-dev-controller", "cat", "/etc/slurm/slurm.conf"}) {
			return mock.Result{Err: fmt.Errorf("exit status 1")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.EqualError(t, err, "reading /etc/slurm/slurm.conf: exit status 1")
	for _, c := range m.Calls {
		assert.NotEqual(t, "create", c.Args[0], "no worker container")
	}
}

func TestWorkerAdd_InvalidUsersLabel(t *testing.T) {
	var m mock.Executor
	m.OnCall = psWithLabels(workerAddOnCall(t), "sind.managed=false,sind.users=alice")
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, `invalid sind.users label "alice": want name:uid:gid entries`, err.Error())
	for _, c := range m.Calls {
		assert.NotEqual(t, "create", c.Args[0], "no worker container")
	}
}

func TestGetMountPoints_Home(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-munge", "sind-dev-data") // no home volume
	c := docker.NewClient(&m)

	containers := []*docker.ContainerInfo{
		{Name: "sind-dev-controller", Labels: docker.Labels{"sind.role": "controller", LabelUsers: "alice:1000:1000"}},
		{Name: "sind-dev-worker-0", Labels: docker.Labels{"sind.role": "worker", LabelUsers: "alice:1000:1000"}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	require.Len(t, mounts, 4)
	assert.Equal(t, MountPoint{Path: "/home", Source: "sind-dev-home", Type: config.StorageVolume, OK: false}, mounts[3])
	assert.Len(t, m.Calls, 1)
}
