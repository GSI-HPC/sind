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

func TestEnableNSSSlurm(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)

	err := enableNSSSlurm(t.Context(), docker.NewClient(&m), "sind-dev-worker-0", RunConfig{Image: "img:1", Identity: config.IdentityNSSSlurm})

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "sh", "-c", nssSlurmCheck}, m.Calls[0].Args)
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "sh", "-ec", nssSlurmSwitch}, m.Calls[1].Args)
}

func TestEnableNSSSlurm_NoModule(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))

	err := enableNSSSlurm(t.Context(), docker.NewClient(&m), "sind-dev-worker-0", RunConfig{Image: "old:1", Identity: config.IdentityClientIDs})

	require.Error(t, err)
	assert.Equal(t, "image old:1 has no libnss_slurm.so.2, which identity clientIds needs on managed workers; build the image with contribs/nss_slurm: exit status 1", err.Error())
	assert.Len(t, m.Calls, 1, "nsswitch.conf left alone")
}

func TestEnableNSSSlurm_SwitchFails(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	m.AddResult("", "", fmt.Errorf("exit status 1"))

	err := enableNSSSlurm(t.Context(), docker.NewClient(&m), "sind-dev-worker-0", RunConfig{Image: "img:1", Identity: config.IdentityNSSSlurm})

	require.Error(t, err)
	assert.Equal(t, "switching passwd and group lookups to nss_slurm: exit status 1", err.Error())
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
	m.AddResult("", "", nil)         // chown slurmdbd.conf
	m.AddResult("", "", nil)         // chmod slurmdbd.conf
	m.AddResult("", "", nil)         // chown slurm.key
	m.AddResult("", "", nil)         // chmod slurm.key
	m.AddResult("", "", nil)         // KillContainer (defer)
	m.AddResult("", "", nil)         // RemoveContainer (defer)
	c := docker.NewClient(&m)

	cfg := &config.Cluster{
		Name:     "dev",
		Identity: config.Identity{Mode: config.IdentityClientIDs},
		Nodes:    []config.Node{{Role: config.RoleController}, {Role: config.RoleDB}, {Role: config.RoleWorker}},
	}
	err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

	require.NoError(t, err)
	require.Len(t, m.Calls, 8)
	cpStdin := m.Calls[1].Stdin
	assert.Contains(t, cpStdin, "slurm.key")
	assert.Contains(t, cpStdin, "AuthType=auth/slurm\nCredType=cred/slurm\nAuthInfo=use_client_ids\nLaunchParameters=enable_nss_slurm\n")
	assert.Contains(t, cpStdin, "AuthType=auth/slurm\nAuthInfo=use_client_ids\nDbdHost=db\n")
	assert.Equal(t, []string{"exec", "sind-dev-config-helper", "chown", "slurm:slurm", "/etc/slurm/slurm.key"}, m.Calls[4].Args)
	assert.Equal(t, []string{"exec", "sind-dev-config-helper", "chmod", "0600", "/etc/slurm/slurm.key"}, m.Calls[5].Args)
}

func TestWriteClusterConfig_SlurmKeyErrors(t *testing.T) {
	// Without a db node, slurm.key is the only file to secure.
	cfg := &config.Cluster{
		Name:     "dev",
		Identity: config.Identity{Mode: config.IdentityClientIDs},
		Nodes:    []config.Node{{Role: config.RoleController}, {Role: config.RoleWorker}},
	}
	for _, tt := range []struct {
		name    string
		results []error // RunContainer, CopyToContainer, chown, chmod
		wantErr string
	}{
		{"chown", []error{nil, nil, fmt.Errorf("chown failed")}, "fixing slurm.key ownership: chown failed"},
		{"chmod", []error{nil, nil, nil, fmt.Errorf("chmod failed")}, "fixing slurm.key permissions: chmod failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			for _, err := range tt.results {
				m.AddResult("", "", err)
			}
			m.AddResult("", "", nil) // KillContainer (defer)
			m.AddResult("", "", nil) // RemoveContainer (defer)
			c := docker.NewClient(&m)

			err := WriteClusterConfig(t.Context(), c, mesh.DefaultRealm, cfg, "busybox:latest", false)

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
		case a[2] == "sh" && a[len(a)-1] == nssSlurmCheck:
			ic.nssCheck = append(ic.nssCheck, a[1])
		case a[2] == "sh" && a[len(a)-1] == nssSlurmSwitch:
			ic.nssSwitch = append(ic.nssSwitch, a[1])
		case a[2] == "sh" && a[3] == "-ec" && strings.HasPrefix(a[4], "groupadd "):
			ic.users = append(ic.users, a[1])
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
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)
	return collectIdentityCalls(m.Calls)
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
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && args[len(args)-1] == nssSlurmCheck {
			return mock.Result{Err: fmt.Errorf("exit status 1")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, identityCfg(config.Identity{Mode: config.IdentityNSSSlurm}), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "node worker-0: image img:1 has no libnss_slurm.so.2, which identity nssSlurm needs on managed workers")
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

	containers := []docker.ContainerListEntry{
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

	key, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev")

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

	_, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Equal(t, "reading slurm key: cp failed", err.Error())
}
