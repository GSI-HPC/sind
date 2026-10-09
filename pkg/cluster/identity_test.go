// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
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
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityFromLabels(t *testing.T) {
	assert.Equal(t, config.IdentityLocal, IdentityFromLabels(docker.Labels{}))
	assert.Equal(t, config.IdentityNSSSlurm, IdentityFromLabels(docker.Labels{LabelIdentity: "nssSlurm"}))
	assert.Equal(t, config.IdentityClientIDs, IdentityFromLabels(docker.Labels{LabelIdentity: "clientIds"}))
}

func TestNodeGetsUsers(t *testing.T) {
	local := config.Identity{Mode: config.IdentityLocal}
	nss := config.Identity{Mode: config.IdentityNSSSlurm}
	clientIDs := config.Identity{Mode: config.IdentityClientIDs}
	controllerUsers := config.Identity{Mode: config.IdentityClientIDs, ControllerUsers: true}
	tests := []struct {
		identity     config.Identity
		role         config.Role
		managed      bool
		hasSubmitter bool
		want         bool
	}{
		{local, config.RoleWorker, true, true, true},
		{local, config.RoleController, true, true, true},
		{config.Identity{}, config.RoleWorker, true, true, true},
		{nss, config.RoleController, true, true, true},
		{nss, config.RoleSubmitter, true, true, true},
		{nss, config.RoleDB, true, true, true},
		{nss, config.RoleWorker, true, true, false},
		{nss, config.RoleWorker, false, true, true},
		{clientIDs, config.RoleSubmitter, true, true, true},
		{clientIDs, config.RoleController, true, true, false},
		{clientIDs, config.RoleController, true, false, true},
		{controllerUsers, config.RoleController, true, true, true},
		{clientIDs, config.RoleDB, true, true, false},
		{clientIDs, config.RoleDB, false, true, true},
		{clientIDs, config.RoleWorker, true, false, false},
		{clientIDs, config.RoleWorker, false, false, true},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("%s/%s/managed=%v/submitter=%v/controllerUsers=%v", tt.identity.Mode, tt.role, tt.managed, tt.hasSubmitter, tt.identity.ControllerUsers)
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, nodeGetsUsers(tt.identity, tt.role, tt.managed, tt.hasSubmitter))
		})
	}
}

func TestNodeSlurmService(t *testing.T) {
	svc, ok := nodeSlurmService(RunConfig{Role: config.RoleSubmitter, Identity: config.IdentityClientIDs})
	assert.True(t, ok)
	assert.Equal(t, probe.ServiceSackd, svc)

	_, ok = nodeSlurmService(RunConfig{Role: config.RoleSubmitter, Identity: config.IdentityNSSSlurm})
	assert.False(t, ok)

	svc, ok = nodeSlurmService(RunConfig{Role: config.RoleWorker, Identity: config.IdentityClientIDs})
	assert.True(t, ok)
	assert.Equal(t, probe.ServiceSlurmd, svc)
}

func TestBuildRunArgs_ClientIDs(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.Identity = config.IdentityClientIDs
	args := BuildRunArgs(cfg)

	for _, v := range testutil.ArgValues(args, "-v") {
		assert.NotContains(t, v, "/etc/munge")
	}
	assert.Contains(t, testutil.ArgValues(args, "--label"), "sind.identity=clientIds")
	entrypoint := args[len(args)-1]
	assert.Equal(t, MaskMunge+NodeEntrypoint, entrypoint)
	assert.True(t, strings.HasPrefix(entrypoint, "ln -sf /dev/null /etc/systemd/system/munge.service\n"))
}

func TestBuildRunArgs_NSSSlurm(t *testing.T) {
	cfg := defaultRunConfig()
	cfg.Identity = config.IdentityNSSSlurm
	args := BuildRunArgs(cfg)

	assert.Contains(t, testutil.ArgValues(args, "-v"), "sind-dev-munge:/etc/munge:ro")
	assert.Contains(t, testutil.ArgValues(args, "--label"), "sind.identity=nssSlurm")
	assert.Equal(t, NodeEntrypoint, args[len(args)-1])
}

func TestBuildRunArgs_LocalIdentity(t *testing.T) {
	// local, like no mode at all, is recorded as local.
	for _, mode := range []config.IdentityMode{"", config.IdentityLocal} {
		cfg := defaultRunConfig()
		cfg.Identity = mode
		assert.Contains(t, testutil.ArgValues(BuildRunArgs(cfg), "--label"), "sind.identity=local")
	}
}

// identityCfg returns a cluster with users, a submitter, a db node and two
// workers, the second unmanaged, in the given identity.
func identityCfg(identity config.Identity) *config.Cluster {
	cfg := usersCfg()
	cfg.Identity = identity
	node := config.Node{Image: "img:1", CPUs: 1, Memory: "1g", TmpSize: "1g"}
	controller, submitter, db, worker, unmanaged := node, node, node, node, node
	controller.Role = config.RoleController
	submitter.Role = config.RoleSubmitter
	db.Role = config.RoleDB
	worker.Role = config.RoleWorker
	unmanaged.Role = config.RoleWorker
	unmanaged.Managed = testutil.Ptr(false)
	cfg.Nodes = []config.Node{controller, submitter, db, worker, unmanaged}
	return cfg
}

// placement returns, per node of NodeRunConfigs, whether it gets the users
// and whether it switches to nss_slurm.
func placement(configs []RunConfig) (users, nss map[string]bool) {
	users, nss = map[string]bool{}, map[string]bool{}
	for _, c := range configs {
		users[c.ShortName] = c.AddUsers
		nss[c.ShortName] = c.NSSSlurm
	}
	return users, nss
}

func TestNodeRunConfigs_Identity(t *testing.T) {
	tests := []struct {
		identity config.Identity
		users    map[string]bool
		nss      map[string]bool
	}{
		{
			config.Identity{Mode: config.IdentityLocal},
			map[string]bool{"controller": true, "submitter": true, "db": true, "worker-0": true, "worker-1": true},
			map[string]bool{"controller": false, "submitter": false, "db": false, "worker-0": false, "worker-1": false},
		},
		{
			config.Identity{Mode: config.IdentityNSSSlurm},
			map[string]bool{"controller": true, "submitter": true, "db": true, "worker-0": false, "worker-1": true},
			map[string]bool{"controller": false, "submitter": false, "db": false, "worker-0": true, "worker-1": false},
		},
		{
			config.Identity{Mode: config.IdentityClientIDs},
			map[string]bool{"controller": false, "submitter": true, "db": false, "worker-0": false, "worker-1": true},
			map[string]bool{"controller": false, "submitter": false, "db": false, "worker-0": true, "worker-1": false},
		},
		{
			config.Identity{Mode: config.IdentityClientIDs, ControllerUsers: true},
			map[string]bool{"controller": true, "submitter": true, "db": false, "worker-0": false, "worker-1": true},
			map[string]bool{"controller": false, "submitter": false, "db": false, "worker-0": true, "worker-1": false},
		},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/controllerUsers=%v", tt.identity.Mode, tt.identity.ControllerUsers), func(t *testing.T) {
			configs := NodeRunConfigs(identityCfg(tt.identity), mesh.DefaultRealm, "", "", "")
			users, nss := placement(configs)
			assert.Equal(t, tt.users, users)
			assert.Equal(t, tt.nss, nss)
			for _, c := range configs {
				assert.Equal(t, tt.identity.Mode, c.Identity, c.ShortName)
				assert.Equal(t, testUsers, c.Users, c.ShortName)
			}
		})
	}
}

func TestNodeRunConfigs_ClientIDsWithoutSubmitter(t *testing.T) {
	// Without a submitter, the controllers are the login nodes.
	cfg := usersCfg()
	cfg.Identity = config.Identity{Mode: config.IdentityClientIDs}
	cfg.Nodes = []config.Node{{Role: config.RoleController, BackupController: true}, {Role: config.RoleWorker}}

	users, _ := placement(NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", ""))

	assert.Equal(t, map[string]bool{"controller": true, "controller-backup": true, "worker-0": false}, users)
}

func TestNodeRunConfigs_APIUsers(t *testing.T) {
	// The api node gets the users like any node that is neither a worker
	// nor a controller: slurmrestd itself needs none.
	tests := []struct {
		mode    config.IdentityMode
		managed *bool
		want    bool
	}{
		{config.IdentityLocal, nil, true},
		{config.IdentityNSSSlurm, nil, true},
		{config.IdentityClientIDs, nil, false},
		{config.IdentityClientIDs, testutil.Ptr(false), true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/managed=%v", tt.mode, tt.managed), func(t *testing.T) {
			cfg := usersCfg()
			cfg.Identity = config.Identity{Mode: tt.mode}
			cfg.Nodes = []config.Node{{Role: config.RoleController}, {Role: config.RoleAPI, Managed: tt.managed}, {Role: config.RoleSubmitter}, {Role: config.RoleWorker}}

			users, nss := placement(NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", ""))

			assert.Equal(t, tt.want, users["api"])
			assert.False(t, nss["api"])
		})
	}
}

// Labels of sind-node images from before and since identity modes.
const (
	oldSindNodeLabels     = `{"org.opencontainers.image.title":"sind-node","sind.slurm.version":"25.11.8"}`
	currentSindNodeLabels = `{"org.opencontainers.image.title":"sind-node","sind.slurm.version":"25.11.8","sind.libjwt.version":"1.18.3"}`
)

func TestOfficialImageRepo(t *testing.T) {
	assert.True(t, strings.HasPrefix(config.DefaultImage, officialImageRepo+":"), config.DefaultImage)
}

func TestCheckIdentityImage(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		result  mock.Result
		wantErr string
	}{
		{"not local", "img:1", mock.Result{Stderr: "Error: No such image: img:1\n", Err: notFoundErr(t)}, ""},
		{"current sind-node", "img:1", mock.Result{Stdout: currentSindNodeLabels}, ""},
		{"other image", "img:1", mock.Result{Stdout: `{"org.opencontainers.image.title":"site-node"}`}, ""},
		{"no labels", "img:1", mock.Result{Stdout: "null\n"}, ""},
		{"old official tag", "ghcr.io/gsi-hpc/sind-node:25.11.8", mock.Result{Stdout: oldSindNodeLabels},
			"image ghcr.io/gsi-hpc/sind-node:25.11.8 is a sind-node image from before identity modes, without the nss_slurm and auth/slurm that identity nssSlurm needs; pull a current one with --pull"},
		{"old official digest", "ghcr.io/gsi-hpc/sind-node@sha256:abc", mock.Result{Stdout: oldSindNodeLabels},
			"image ghcr.io/gsi-hpc/sind-node@sha256:abc is a sind-node image from before identity modes, without the nss_slurm and auth/slurm that identity nssSlurm needs; pull a current one with --pull"},
		{"old local build", "sind-node:dev", mock.Result{Stdout: oldSindNodeLabels},
			"image sind-node:dev is a sind-node image from before identity modes, without the nss_slurm and auth/slurm that identity nssSlurm needs; rebuild it from the current sind-node Dockerfile"},
		{"inspect error", "img:1", mock.Result{Err: fmt.Errorf("daemon gone")}, "inspecting image img:1: daemon gone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(tt.result.Stdout, tt.result.Stderr, tt.result.Err)

			err := checkIdentityImage(t.Context(), docker.NewClient(&m), tt.image, config.IdentityNSSSlurm)

			require.Len(t, m.Calls, 1)
			assert.Equal(t, []string{"image", "inspect", tt.image, "--format", "{{json .Config.Labels}}"}, m.Calls[0].Args)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestCheckIdentityImages(t *testing.T) {
	cfgWith := func(identity config.IdentityMode, images map[config.Role]string) *config.Cluster {
		cfg := identityCfg(config.Identity{Mode: identity})
		for i, n := range cfg.Nodes {
			if img, ok := images[n.Role]; ok {
				cfg.Nodes[i].Image = img
			}
		}
		return cfg
	}
	tests := []struct {
		name    string
		cfg     *config.Cluster
		checked []string
	}{
		{"local", cfgWith(config.IdentityLocal, nil), nil},
		{"nssSlurm checks the managed workers", cfgWith(config.IdentityNSSSlurm, map[config.Role]string{config.RoleController: "ctl:1", config.RoleWorker: "wrk:1"}), []string{"wrk:1"}},
		{"clientIds checks every managed node, each image once", cfgWith(config.IdentityClientIDs, map[config.Role]string{config.RoleController: "ctl:1", config.RoleWorker: "wrk:1"}), []string{"ctl:1", "img:1", "wrk:1"}},
		{"no image", cfgWith(config.IdentityNSSSlurm, map[config.Role]string{config.RoleWorker: ""}), nil},
		{"pull", func() *config.Cluster { c := cfgWith(config.IdentityNSSSlurm, nil); c.Pull = true; return c }(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.OnCall = func([]string, string) mock.Result { return mock.Result{Stdout: currentSindNodeLabels} }

			require.NoError(t, checkIdentityImages(t.Context(), docker.NewClient(&m), tt.cfg))

			var checked []string
			for _, c := range m.Calls {
				checked = append(checked, c.Args[2])
			}
			slices.Sort(checked)
			assert.Equal(t, tt.checked, checked)
		})
	}
}

func TestCheckIdentityImages_Old(t *testing.T) {
	var m mock.Executor
	m.OnCall = func([]string, string) mock.Result { return mock.Result{Stdout: oldSindNodeLabels} }

	err := checkIdentityImages(t.Context(), docker.NewClient(&m), identityCfg(config.Identity{Mode: config.IdentityClientIDs}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "image img:1 is a sind-node image from before identity modes")
	assert.Len(t, m.Calls, 1)
}

func TestCreateResources_ClientIDs(t *testing.T) {
	cfg := createCfg()
	cfg.Identity = config.Identity{Mode: config.IdentityClientIDs}
	assert.NotContains(t, createdVolumes(t, cfg), "sind-dev-munge")
	assert.Contains(t, createdVolumes(t, createCfg()), "sind-dev-munge")
}

func TestPreflightCheck_ClientIDsSkipsMungeVolume(t *testing.T) {
	var m mock.Executor
	m.OnCall = preflightOnCall(t, map[string]bool{"sind-dev-munge": true})
	client := docker.NewClient(&m)

	require.Error(t, PreflightCheck(t.Context(), client, mesh.DefaultRealm, minimalConfig()))

	cfg := minimalConfig()
	cfg.Identity = config.Identity{Mode: config.IdentityClientIDs}
	require.NoError(t, PreflightCheck(t.Context(), client, mesh.DefaultRealm, cfg))
}

func TestWriteClusterConfig_ClientIDs(t *testing.T) {
	var m mock.Executor
	m.AddResult("abc123\n", "", nil) // RunContainer (helper with sleep)
	m.AddResult("", "", nil)         // CopyToContainer
	m.AddResult("", "", nil)         // chown slurmdbd.conf and slurm.key
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name:     "dev",
		Identity: config.Identity{Mode: config.IdentityClientIDs},
		Nodes:    []config.Node{{Role: config.RoleController}, {Role: config.RoleDB}, {Role: config.RoleWorker, CPUs: 1, Memory: "512m"}},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest")

	require.NoError(t, err)
	require.Len(t, m.Calls, 4)
	cpStdin := m.Calls[1].Stdin
	assert.Contains(t, cpStdin, "slurm.key")
	assert.Contains(t, cpStdin, "AuthType=auth/slurm\nCredType=cred/slurm\nAuthInfo=use_client_ids\nLaunchParameters=enable_nss_slurm\n")
	assert.Contains(t, cpStdin, "AuthType=auth/slurm\nAuthInfo=use_client_ids\nDbdHost=db\n")
	modes := tarModes(t, cpStdin)
	assert.Equal(t, int64(0o600), modes["slurm.key"])
	assert.Equal(t, int64(0o600), modes["slurmdbd.conf"])
	assert.Equal(t, []string{"exec", "sind-dev-config-helper", "chown", "slurm:slurm", "/etc/slurm/slurmdbd.conf", "/etc/slurm/slurm.key"}, m.Calls[2].Args)
}

func TestWriteClusterConfig_SlurmKeyErrors(t *testing.T) {
	// Without a db node, slurm.key is the only file to secure.
	cfg := &config.Cluster{
		Name:     "dev",
		Identity: config.Identity{Mode: config.IdentityClientIDs},
		Nodes:    []config.Node{{Role: config.RoleController}, {Role: config.RoleWorker, CPUs: 1, Memory: "512m"}},
	}
	for _, tt := range []struct {
		name    string
		results []error // RunContainer, CopyToContainer, chown
		wantErr string
	}{
		{"chown", []error{nil, nil, fmt.Errorf("chown failed")}, "fixing ownership of slurm.key: chown failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			for _, err := range tt.results {
				m.AddResult("", "", err)
			}
			m.AddResult("", "", nil) // RemoveContainer (defer)
			c := docker.NewClient(&m)

			err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest")

			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
			assert.Equal(t, "run", m.Calls[0].Args[0], "helper runs to fix ownership")
		})
	}
}

// identityCalls sorts the calls of a Create or WorkerAdd run that concern
// the identity mode, by container.
type identityCalls struct {
	nssCheck  []string
	nssSwitch []string
	users     []string
	mungeWait []string
	enabled   map[string][]string
	created   map[string][]string // node container → docker create args
	helpers   []string            // helper containers run
}

func collectIdentityCalls(calls []mock.Call) *identityCalls {
	ic := &identityCalls{enabled: map[string][]string{}, created: map[string][]string{}}
	for _, c := range calls {
		a := c.Args
		switch {
		case a[0] == "create" && slices.Contains(a, "--hostname"):
			ic.created[testutil.ArgValues(a, "--name")[0]] = a
		case a[0] == "run" && slices.Contains(a, "--name"):
			ic.helpers = append(ic.helpers, testutil.ArgValues(a, "--name")[0])
		case a[0] != "exec" || len(a) < 4:
		case a[2] == "sh":
			container, steps, ok := setupCall(a)
			if !ok {
				break
			}
			if steps["nss_slurm"] == nssSlurmCheck {
				ic.nssCheck = append(ic.nssCheck, container)
			}
			if steps["nsswitch"] == nssSlurmSwitch {
				ic.nssSwitch = append(ic.nssSwitch, container)
			}
			if strings.HasPrefix(steps["users"], "groupadd ") {
				ic.users = append(ic.users, container)
			}
		case a[2] == "systemctl" && a[3] == "is-active" && a[len(a)-1] == "munge":
			if !slices.Contains(ic.mungeWait, a[1]) {
				ic.mungeWait = append(ic.mungeWait, a[1])
			}
		case a[2] == "systemctl" && a[3] == "enable":
			ic.enabled[a[1]] = append(ic.enabled[a[1]], a[len(a)-1])
		}
	}
	for _, s := range [][]string{ic.nssCheck, ic.nssSwitch, ic.users, ic.mungeWait} {
		slices.Sort(s)
	}
	return ic
}

// createIdentityCluster runs Create on cfg against happyOnCall and returns
// its calls.
func createIdentityCluster(t *testing.T, cfg *config.Cluster) *identityCalls {
	t.Helper()
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), imageLabels(currentSindNodeLabels))
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "img:1"), "image checked once")
	return collectIdentityCalls(m.Calls)
}

// imageLabels is a happyOnCall override under which docker image inspect
// reports labels, in JSON.
func imageLabels(labels string) func([]string, string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "image" && args[1] == "inspect" {
			return mock.Result{Stdout: labels}, true
		}
		return mock.Result{}, false
	}
}

func TestCreate_IdentityOldImage(t *testing.T) {
	// A cached sind-node image from before identity modes is refused
	// before anything is created.
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), imageLabels(oldSindNodeLabels))
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := Create(t.Context(), client, meshMgr, identityCfg(config.Identity{Mode: config.IdentityNSSSlurm}), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "image img:1 is a sind-node image from before identity modes")
	assert.Zero(t, countCalls(m.Calls, "create"), "no node")
	assert.Zero(t, countCalls(m.Calls, "network", "create"), "no network")
	assert.Zero(t, countCalls(m.Calls, "volume", "create"), "no volume")
}

func TestCreate_NSSSlurm(t *testing.T) {
	ic := createIdentityCluster(t, identityCfg(config.Identity{Mode: config.IdentityNSSSlurm}))

	assert.Equal(t, []string{"sind-dev-worker-0"}, ic.nssCheck)
	assert.Equal(t, []string{"sind-dev-worker-0"}, ic.nssSwitch)
	assert.Equal(t, []string{"sind-dev-controller", "sind-dev-db", "sind-dev-submitter", "sind-dev-worker-1"}, ic.users)
	assert.Len(t, ic.mungeWait, 5, "every node waits for munge")
	assert.Empty(t, ic.enabled["sind-dev-submitter"])
	assert.Contains(t, ic.helpers, "sind-dev-munge-helper")
}

func TestCreate_ClientIDs(t *testing.T) {
	ic := createIdentityCluster(t, identityCfg(config.Identity{Mode: config.IdentityClientIDs}))

	assert.Equal(t, []string{"sind-dev-worker-0"}, ic.nssCheck)
	assert.Equal(t, []string{"sind-dev-worker-0"}, ic.nssSwitch)
	assert.Equal(t, []string{"sind-dev-submitter", "sind-dev-worker-1"}, ic.users)
	assert.Empty(t, ic.mungeWait, "munge is masked")
	assert.NotContains(t, ic.helpers, "sind-dev-munge-helper", "no munge key")
	assert.Equal(t, []string{"sackd"}, ic.enabled["sind-dev-submitter"])
	assert.Equal(t, []string{"slurmctld"}, ic.enabled["sind-dev-controller"])
	require.Len(t, ic.created, 5)
	for name, args := range ic.created {
		assert.Contains(t, testutil.ArgValues(args, "--label"), "sind.identity=clientIds", name)
		assert.True(t, strings.HasPrefix(args[len(args)-1], MaskMunge), name)
	}
}

func TestCreate_NSSSlurmModuleMissing(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, stdin string) (mock.Result, bool) {
		if _, steps, ok := setupCall(args); ok && steps["nss_slurm"] != "" {
			return failedSetup(t, 1, "", "nss_slurm"), true
		}
		// A custom image: only the check on the node finds what it lacks.
		return imageLabels("{}")(args, stdin)
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, identityCfg(config.Identity{Mode: config.IdentityNSSSlurm}), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "node worker-0: image img:1 has no libnss_slurm.so.2, which identity nssSlurm needs on managed workers; use a current sind-node image (--pull refreshes a cached one), or build yours with contribs/nss_slurm: exit status 1")
}

func TestWorkerAdd_Identity(t *testing.T) {
	for _, tt := range []struct {
		mode      string
		unmanaged bool
		nss       bool
		users     bool
	}{
		{"nssSlurm", false, true, false},
		{"clientIds", false, true, false},
		{"clientIds", true, false, true},
	} {
		t.Run(fmt.Sprintf("%s/unmanaged=%v", tt.mode, tt.unmanaged), func(t *testing.T) {
			var m mock.Executor
			m.OnCall = psWithLabels(workerAddOnCall(t), "sind.identity="+tt.mode+",sind.users=alice:1000:1000 bob:2001:3000,sind.groups=alice:1000 hpc:3000:alice")
			client := docker.NewClient(&m)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1, Unmanaged: tt.unmanaged}, time.Millisecond)
			require.NoError(t, err)

			ic := collectIdentityCalls(m.Calls)
			worker := "sind-dev-worker-0"
			assert.Equal(t, tt.nss, slices.Contains(ic.nssSwitch, worker))
			assert.Equal(t, tt.users, slices.Contains(ic.users, worker))
			require.Contains(t, ic.created, worker)
			assert.Contains(t, testutil.ArgValues(ic.created[worker], "--label"), "sind.identity="+tt.mode)
			assert.Equal(t, tt.mode == "clientIds", strings.HasPrefix(ic.created[worker][len(ic.created[worker])-1], MaskMunge))
		})
	}
}

func TestWorkerAdd_IdentityImage(t *testing.T) {
	// Managed workers of an identity cluster need a current sind-node
	// image, which sind checks before it creates them.
	for _, tt := range []struct {
		name    string
		opts    WorkerAddOptions
		result  mock.Result
		checked string
		wantErr string
	}{
		{"current", WorkerAddOptions{}, mock.Result{Stdout: currentSindNodeLabels}, testControllerImageID, ""},
		{"old explicit image", WorkerAddOptions{Image: "ghcr.io/gsi-hpc/sind-node:25.11.0"}, mock.Result{Stdout: oldSindNodeLabels}, "ghcr.io/gsi-hpc/sind-node:25.11.0",
			"image ghcr.io/gsi-hpc/sind-node:25.11.0 is a sind-node image from before identity modes, without the nss_slurm and auth/slurm that identity nssSlurm needs; pull a current one with --pull"},
		{"inspect error", WorkerAddOptions{}, mock.Result{Err: fmt.Errorf("daemon gone")}, testControllerImageID, "inspecting image " + testControllerImageID + ": daemon gone"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := psWithLabels(workerAddOnCall(t), "sind.identity=nssSlurm")
			var m mock.Executor
			m.OnCall = func(args []string, stdin string) mock.Result {
				if args[0] == "image" {
					return tt.result
				}
				return base(args, stdin)
			}
			client := docker.NewClient(&m)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			tt.opts.ClusterName, tt.opts.Count = "dev", 1

			_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), tt.opts, time.Millisecond)

			assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", tt.checked))
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
			assert.Zero(t, countCalls(m.Calls, "create"))
		})
	}
}

func TestWorkerAdd_IdentityImagePulled(t *testing.T) {
	// --pull pulls an explicit image once, before anything checks it; the
	// pulled image is then checked like a local one.
	base := psWithLabels(workerAddOnCall(t), "sind.identity=nssSlurm")
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dev-controller" {
			return mock.Result{Stdout: nodeInspectJSON(t, args[1], nodeInspect{Image: testControllerImageID, Labels: docker.Labels{LabelIdentity: "nssSlurm"}})}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1, Image: "img:2", Pull: true}, time.Millisecond)

	require.NoError(t, err)
	assert.Equal(t, 1, countCalls(m.Calls, "pull", "img:2"))
	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "img:2"))
}

func TestNodeServices(t *testing.T) {
	managed := docker.Labels{}
	unmanaged := docker.Labels{LabelManaged: "false"}
	clientIDs := docker.Labels{LabelIdentity: "clientIds"}
	clientIDsUnmanaged := docker.Labels{LabelIdentity: "clientIds", LabelManaged: "false"}
	nss := docker.Labels{LabelIdentity: "nssSlurm"}
	m, s := probe.ServiceMunge, probe.ServiceSSHD
	tests := []struct {
		role   config.Role
		labels docker.Labels
		want   []probe.Service
	}{
		{config.RoleController, managed, []probe.Service{m, s, probe.ServiceSlurmctld}},
		{config.RoleWorker, managed, []probe.Service{m, s, probe.ServiceSlurmd}},
		{config.RoleSubmitter, managed, []probe.Service{m, s}},
		{config.RoleDB, managed, []probe.Service{m, s, probe.ServiceMariadb, probe.ServiceSlurmdbd}},
		{config.RoleWorker, unmanaged, []probe.Service{m, s}},
		{config.RoleSubmitter, nss, []probe.Service{m, s}},
		{config.RoleController, clientIDs, []probe.Service{s, probe.ServiceSlurmctld}},
		{config.RoleSubmitter, clientIDs, []probe.Service{s, probe.ServiceSackd}},
		{config.RoleDB, clientIDs, []probe.Service{s, probe.ServiceMariadb, probe.ServiceSlurmdbd}},
		{config.RoleWorker, clientIDsUnmanaged, []probe.Service{s}},
		{config.RoleAPI, managed, []probe.Service{m, s, probe.ServiceSlurmrestd}},
		{config.RoleAPI, clientIDs, []probe.Service{s, probe.ServiceSlurmrestd}},
		{config.RoleAPI, unmanaged, []probe.Service{m, s}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%v", tt.role, tt.labels), func(t *testing.T) {
			assert.Equal(t, tt.want, nodeServices(tt.role, tt.labels))
		})
	}
}

func TestGetMountPoints_ClientIDs(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-data")
	c := docker.NewClient(&m)

	containers := []*docker.ContainerInfo{
		{Name: "sind-dev-controller", Labels: docker.Labels{"sind.role": "controller", LabelIdentity: "clientIds"}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	var paths []string
	for _, mp := range mounts {
		paths = append(paths, mp.Path)
	}
	assert.Equal(t, []string{"/etc/slurm", "/data"}, paths)
}

func TestGetAuthKey_ClientIDs(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "c1", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller,sind.identity=clientIds",
	}), "", nil)
	m.AddResult(testutil.TarArchive("slurm.key", "slurm-key-bytes"), "", nil)
	c := docker.NewClient(&m)

	key, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev", "")

	require.NoError(t, err)
	assert.Equal(t, &AuthKey{Type: AuthSlurm, Key: []byte("slurm-key-bytes")}, key)
	assert.Equal(t, []string{"cp", "sind-dev-controller:/etc/slurm/slurm.key", "-"}, m.Calls[1].Args)
}

func TestGetAuthKey_ClientIDsCopyError(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "c1", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller,sind.identity=clientIds",
	}), "", nil)
	m.AddResult("", "", fmt.Errorf("cp failed"))
	c := docker.NewClient(&m)

	_, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev", "")

	require.Error(t, err)
	assert.Equal(t, "reading slurm key: cp failed", err.Error())
}
