// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
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

func TestSlurmrestdStep(t *testing.T) {
	api := RunConfig{Role: config.RoleAPI, Managed: true, Image: "sind-node:25.11", AddUsers: true, Users: testUsers}
	assert.Equal(t, []string{"slurmrestd", "slurmrestd-security", "users", "ssh"}, stepNames(nodeSetupSteps(api)), "checked before the users")
	assert.Equal(t, slurmrestdCheck, nodeSetupSteps(api)[0].script)
	assert.Equal(t, slurmrestdSecurity, nodeSetupSteps(api)[1].script)
	bare := api
	bare.Managed = false
	assert.Equal(t, []string{"users", "ssh"}, stepNames(nodeSetupSteps(bare)), "a bare api node runs no slurmrestd")

	var m mock.Executor
	r := failedSetup(t, 1, "", "slurmrestd")
	m.AddResult(r.Stdout, r.Stderr, r.Err)
	_, err := setupNode(t.Context(), docker.NewClient(&m), "sind-dev-api", api, "ssh-ed25519 AAAA-test-key\n")
	require.EqualError(t, err, "image sind-node:25.11 has no slurmrestd with its systemd unit, which the api node runs; sind-node images ship it from Slurm 26.05 on (--pull refreshes a cached one), and a custom image needs it installed but not enabled: exit status 1")
}

func TestSlurmrestdSecurity(t *testing.T) {
	assert.Equal(t, `mkdir -p /etc/systemd/system/slurmrestd.service.d
printf '[Service]\nEnvironment=SLURMRESTD_SECURITY=disable_unshare_sysv,disable_unshare_files\n' >/etc/systemd/system/slurmrestd.service.d/sind.conf
systemctl daemon-reload`, slurmrestdSecurity)
	assert.True(t, strings.HasPrefix(slurmrestdDropIn, "/etc/systemd/system/slurmrestd.service.d/"), "a drop-in of the unit")

	var m mock.Executor
	r := failedSetup(t, 1, "Failed to reload daemon", "slurmrestd", "slurmrestd-security")
	m.AddResult(r.Stdout, r.Stderr, r.Err)
	api := RunConfig{Role: config.RoleAPI, Managed: true, Image: "sind-node:26.05"}
	_, err := setupNode(t.Context(), docker.NewClient(&m), "sind-dev-api", api, "ssh-ed25519 AAAA-test-key\n")
	require.EqualError(t, err, "configuring slurmrestd to start without unsharing namespaces, which the container denies: exit status 1: Failed to reload daemon")
}

// slurmrestdChecked returns the containers whose node setup checks for
// slurmrestd and adds its drop-in.
func slurmrestdChecked(calls []mock.Call) []string {
	var checked []string
	for _, c := range calls {
		if container, steps, ok := setupCall(c.Args); ok && steps["slurmrestd"] == slurmrestdCheck && steps["slurmrestd-security"] == slurmrestdSecurity {
			checked = append(checked, container)
		}
	}
	return checked
}

// apiCfg returns a cluster with a db node and an api node, managed or not.
func apiCfg(identity config.IdentityMode, apiManaged *bool) *config.Cluster {
	node := config.Node{Image: "img:1", CPUs: 1, Memory: "1g", TmpSize: "1g"}
	controller, db, api, worker := node, node, node, node
	controller.Role = config.RoleController
	db.Role = config.RoleDB
	api.Role = config.RoleAPI
	api.Managed = apiManaged
	worker.Role = config.RoleWorker
	return &config.Cluster{
		Name:     "dev",
		Identity: config.Identity{Mode: identity},
		Nodes:    []config.Node{controller, db, api, worker},
	}
}

// configCopy returns the tar archive Create copied into the config helper.
func configCopy(t *testing.T, calls []mock.Call) string {
	t.Helper()
	for _, c := range calls {
		if c.Args[0] == "cp" && strings.HasPrefix(c.Args[len(c.Args)-1], "sind-dev-config-helper:") {
			return c.Stdin
		}
	}
	require.Fail(t, "no copy into the config helper")
	return ""
}

// createAPICluster runs Create on cfg against happyOnCall, with override,
// and returns its calls and error.
func createAPICluster(t *testing.T, cfg *config.Cluster, override func([]string, string) (mock.Result, bool)) ([]mock.Call, error) {
	t.Helper()
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), override)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	return m.Calls, err
}

func TestCreate_APINode(t *testing.T) {
	for _, tt := range []struct {
		identity config.IdentityMode
		params   string
	}{
		{config.IdentityLocal, "AuthAltParameters=jwt_key=/etc/slurm/jwt_hs256.key\n"},
		{config.IdentityClientIDs, "AuthAltParameters=jwt_key=/etc/slurm/jwt_hs256.key,use_jwt_client_ids\n"},
	} {
		t.Run(string(tt.identity), func(t *testing.T) {
			calls, err := createAPICluster(t, apiCfg(tt.identity, nil), imageLabels(currentSindNodeLabels))
			require.NoError(t, err)

			ic := collectIdentityCalls(calls)
			assert.Equal(t, []string{"slurmrestd"}, ic.enabled["sind-dev-api"])
			assert.Equal(t, []string{"sind-dev-api"}, slurmrestdChecked(calls))
			assert.Positive(t, countCalls(calls, "exec", "sind-dev-api", "systemctl", "is-active", "slurmrestd"))

			archive := configCopy(t, calls)
			assert.Equal(t, int64(0o600), tarModes(t, archive)["jwt_hs256.key"])
			assert.Contains(t, archive, "\nAuthAltTypes=auth/jwt\n"+tt.params)
			assert.Contains(t, archive, "DbdHost=db\n", "slurmdbd.conf")
			assert.Equal(t, 2, strings.Count(archive, tt.params), "slurm.conf and slurmdbd.conf")
			chown := slices.IndexFunc(calls, func(c mock.Call) bool {
				return slices.Contains(c.Args, "chown") && slices.Contains(c.Args, "/etc/slurm/jwt_hs256.key")
			})
			assert.NotEqual(t, -1, chown, "jwt_hs256.key owned by slurm")
		})
	}
}

func TestCreate_UnmanagedAPINode(t *testing.T) {
	calls, err := createAPICluster(t, apiCfg(config.IdentityLocal, testutil.Ptr(false)), nil)
	require.NoError(t, err)

	ic := collectIdentityCalls(calls)
	assert.Empty(t, ic.enabled["sind-dev-api"])
	assert.Empty(t, slurmrestdChecked(calls))
	archive := configCopy(t, calls)
	assert.NotContains(t, archive, "jwt_hs256.key")
	assert.NotContains(t, archive, "AuthAlt")
	assert.Contains(t, testutil.ArgValues(ic.created["sind-dev-api"], "--label"), "sind.managed=false")
}

func TestCreate_APINodeWithoutSlurmrestd(t *testing.T) {
	calls, err := createAPICluster(t, apiCfg(config.IdentityLocal, nil), func(args []string, _ string) (mock.Result, bool) {
		if container, steps, ok := setupCall(args); ok && container == "sind-dev-api" && steps["slurmrestd"] != "" {
			return failedSetup(t, 1, "", "slurmrestd"), true
		}
		return mock.Result{}, false
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "node api: image img:1 has no slurmrestd")
	assert.Empty(t, collectIdentityCalls(calls).enabled["sind-dev-api"])
}
