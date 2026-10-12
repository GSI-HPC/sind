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
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createMonitored runs Create for a monitored cfg against happyOnCall with
// the stack calls answered by f, and the override before both.
func createMonitored(t *testing.T, f *stackFake, cfg *config.Cluster, override func([]string, string) (mock.Result, bool)) (*Cluster, *mock.Executor, error) {
	t.Helper()
	pipes := &mock.Pipes{}
	t.Cleanup(pipes.CloseAll)
	m := &mock.Executor{OnStart: pipes.OnStart}
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, stdin string) (mock.Result, bool) {
		if override != nil {
			if r, ok := override(args, stdin); ok {
				return r, true
			}
		}
		return f.handle(args, stdin)
	})
	client := docker.NewClient(m)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg.Monitoring = true
	c, err := Create(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), cfg, time.Millisecond)
	return c, m, err
}

// callsWith returns the calls whose joined arguments contain s.
func callsWith(m *mock.Executor, s string) [][]string {
	var calls [][]string
	for _, c := range m.Calls {
		if strings.Contains(strings.Join(c.Args, " "), s) {
			calls = append(calls, c.Args)
		}
	}
	return calls
}

func TestCreate_Monitoring(t *testing.T) {
	f := newStackFake(t)
	cluster, m, err := createMonitored(t, f, createCfg(), nil)
	require.NoError(t, err)
	assert.Equal(t, "26.05.4", cluster.SlurmVersion)

	for _, s := range observability.Services {
		c := f.containers[string(observability.ContainerName("sind", s))]
		require.NotNil(t, c, s)
		assert.Equal(t, docker.StateRunning, c.state)
	}
	assert.True(t, f.volumes["sind-journal"])
	assert.True(t, f.called("run --rm --name sind-dev-journal-helper"))
	helper := callsWith(m, "sind-dev-journal-helper")[0]
	assert.Equal(t, []string{"dev", "controller", "worker-0"}, helper[len(helper)-3:])

	// Every node mounts its journal directory.
	for _, c := range m.Calls {
		if c.Args[0] != "create" || !slices.Contains(c.Args, "sind.role=worker") && !slices.Contains(c.Args, "sind.role=controller") {
			continue
		}
		name, _ := testutil.ArgValue(c.Args, "--name")
		short := strings.TrimPrefix(name, "sind-dev-")
		assert.Contains(t, c.Args, "type=volume,source=sind-journal,target=/var/log/journal,volume-subpath=dev/"+short, name)
		assert.Contains(t, c.Args, "sind.monitoring=true")
	}
	// slurmctld serves the metrics; the stack's names are in the mesh DNS.
	assert.True(t, slices.ContainsFunc(callsWith(m, "6817"), func(a []string) bool { return a[len(a)-1] == "/metrics/nodes" }))
	assert.True(t, slices.ContainsFunc(m.Calls, func(c mock.Call) bool {
		return strings.Join(c.Args, " ") == "cp - sind-dns:/" && strings.Contains(c.Stdin, "grafana.sind.sind")
	}))
	// The stack's images are pulled with the nodes'.
	pulled := false
	for _, c := range m.Calls {
		if isImageExists(c.Args) && c.Args[4] == observability.Images()[0] {
			pulled = true
		}
	}
	assert.True(t, pulled)
}

func TestCreate_NotMonitored(t *testing.T) {
	// A cluster without monitoring makes no call of the stack.
	f := newStackFake(t)
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()
	m := &mock.Executor{OnStart: pipes.OnStart}
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	client := docker.NewClient(m)
	_, err := Create(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), createCfg(), time.Millisecond)
	require.NoError(t, err)
	for _, c := range m.Calls {
		r, mine := f.handle(c.Args, c.Stdin)
		assert.False(t, mine, "%v %v", c.Args, r)
		assert.NotContains(t, c.Args, "sind.monitoring=true")
	}
}

func TestCreate_MonitoringBackupController(t *testing.T) {
	f := newStackFake(t)
	cfg := createCfg()
	cfg.Nodes[0].BackupController = true
	_, m, err := createMonitored(t, f, cfg, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\nSlurmctld(backup) at controller-backup is UP\n"}, true
		}
		return mock.Result{}, false
	})
	require.NoError(t, err)
	var paths []string
	for _, a := range callsWith(m, "6817") {
		if a[1] == "sind-dev-controller-backup" {
			paths = append(paths, a[len(a)-1])
		}
	}
	assert.Equal(t, []string{"/livez"}, paths)
}

func TestCreate_MonitoringUnsupportedImage(t *testing.T) {
	f := newStackFake(t)
	f.version = "25.11.2"
	_, _, err := createMonitored(t, f, createCfg(), nil)
	require.ErrorIs(t, err, ErrImageUnsupported)
	assert.Empty(t, f.containers)
}

func TestCreate_MonitoringPullFails(t *testing.T) {
	f := newStackFake(t)
	cfg := createCfg()
	cfg.Pull = true
	_, _, err := createMonitored(t, f, cfg, pullOnCall(mock.Result{Err: fmt.Errorf("manifest unknown")}))
	require.ErrorContains(t, err, "manifest unknown")
	assert.False(t, f.called("run --rm --name sind-dev-journal-helper"))
}

func TestCreate_MonitoringResourcesFail(t *testing.T) {
	for _, prefix := range []string{"volume create", "run --rm --name sind-dev-journal-helper"} {
		t.Run(prefix, func(t *testing.T) {
			f := newStackFake(t)
			f.fail[prefix] = mock.Result{Err: testutil.ExitCode1(t)}
			_, _, err := createMonitored(t, f, createCfg(), nil)
			require.Error(t, err)
			assert.Empty(t, f.containers)
		})
	}
}

func TestCreate_MonitoringRollback(t *testing.T) {
	tests := []struct {
		name      string
		other     bool
		wantStack bool
	}{
		{"removes the stack it created", false, false},
		{"keeps the stack of another monitored cluster", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newStackFake(t)
			if tt.other {
				f.nodes = []docker.ContainerListEntry{monitoredNode("prod", "controller", "controller")}
			}
			f.fail["inspect sind-grafana sind-prometheus sind-loki"] = mock.Result{Err: testutil.ExitCode1(t)}
			_, _, err := createMonitored(t, f, createCfg(), func(args []string, _ string) (mock.Result, bool) {
				// The mesh is in use by the other cluster, so it stays.
				if tt.other && args[0] == "ps" && slices.Contains(args, "label=sind.realm=sind") && !slices.Contains(args, "label=sind.role") {
					return mock.Result{Stdout: testutil.NDJSON(map[string]string{"Names": "sind-prod-controller", "Labels": "sind.cluster=prod"})}, true
				}
				return mock.Result{}, false
			})
			require.ErrorContains(t, err, "inspecting the monitoring stack")
			assert.Equal(t, tt.wantStack, len(f.containers) == len(observability.Services), "%v", f.containers)
			if !tt.wantStack {
				assert.Empty(t, f.containers)
				assert.Empty(t, f.volumes)
			}
		})
	}
}

func TestCreate_MonitoringRollbackErrors(t *testing.T) {
	f := newStackFake(t)
	f.fail["inspect sind-grafana sind-prometheus sind-loki"] = mock.Result{Err: testutil.ExitCode1(t)}
	f.fail["ps -a --no-trunc --format json --filter label=com.docker.compose.project=sind-mesh"] = mock.Result{Stderr: "ps failed\n", Err: testutil.ExitCode1(t)}
	f.fail["volume ls --format json --filter label=com.docker.compose.project=sind-mesh"] = mock.Result{Stderr: "ls failed\n", Err: testutil.ExitCode1(t)}
	_, _, err := createMonitored(t, f, createCfg(), nil)
	require.ErrorContains(t, err, "rolling back: removing the monitoring stack: ")
	require.ErrorContains(t, err, "rolling back: removing the monitoring stack's volumes: ")
}

func TestRollbackKeepsStack(t *testing.T) {
	f := newStackFake(t)
	client, _ := f.client()
	assert.False(t, rollbackKeepsStack(t.Context(), client, "sind", "dev"))
	f.nodes = []docker.ContainerListEntry{monitoredNode("prod", "controller", "controller")}
	assert.True(t, rollbackKeepsStack(t.Context(), client, "sind", "dev"))
	f.fail["ps"] = mock.Result{Err: testutil.ExitCode1(t)}
	assert.True(t, rollbackKeepsStack(t.Context(), client, "sind", "dev"))
}
