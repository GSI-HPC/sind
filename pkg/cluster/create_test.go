// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/monitor"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- controllerImage ---

func TestControllerImage(t *testing.T) {
	cfg := &config.Cluster{
		Nodes: []config.Node{
			{Role: config.RoleWorker, Image: "compute:1"},
			{Role: config.RoleController, Image: "ctrl:1"},
		},
	}
	assert.Equal(t, "ctrl:1", controllerImage(cfg))
}

func TestControllerImage_Fallback(t *testing.T) {
	cfg := &config.Cluster{
		Nodes: []config.Node{
			{Role: config.RoleWorker, Image: "compute:1"},
		},
	}
	assert.Equal(t, config.DefaultImage, controllerImage(cfg))
}

// --- Create (end-to-end) ---

// inspectJSONLabels builds docker inspect JSON output with custom labels.
func inspectJSONLabels(t *testing.T, name, status string, networks map[docker.NetworkName]string, labels docker.Labels) string {
	t.Helper()
	type netInfo struct {
		IPAddress string `json:"IPAddress"`
	}
	type result struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]netInfo `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	r := result{ID: "id-" + name, Name: "/" + name}
	r.State.Status = status
	r.Config.Labels = labels
	nets := make(map[string]netInfo)
	for n, ip := range networks {
		nets[string(n)] = netInfo{IPAddress: ip}
	}
	r.NetworkSettings.Networks = nets
	data, err := json.Marshal([]result{r})
	require.NoError(t, err)
	return string(data)
}

// inspectJSON builds the JSON output that docker inspect returns for a container.
func inspectJSON(t *testing.T, name, status string, networks map[docker.NetworkName]string) string {
	t.Helper()
	return inspectJSONLabels(t, name, status, networks, docker.Labels{})
}

// inspectEntry describes one entry for inspectJSONBatch.
type inspectEntry struct {
	Name     string
	Status   string
	Networks map[docker.NetworkName]string
}

// inspectJSONBatch builds a JSON array for docker inspect with multiple
// containers, matching the format of a single batched inspect call.
func inspectJSONBatch(t *testing.T, entries ...inspectEntry) string {
	t.Helper()
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, strings.TrimPrefix(strings.TrimSuffix(inspectJSON(t, e.Name, e.Status, e.Networks), "]"), "["))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// notFoundErr returns an exec.ExitError with exit code 1 for "not found" mocking.
func notFoundErr(t *testing.T) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

// emptyCorefileContent returns a Corefile with no host entries.
func emptyCorefileContent() string {
	return "sind.sind:53 {\n    hosts {\n        fallthrough\n    }\n    log\n    errors\n}\n\n.:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"
}

// emptyCorefileTar returns a tar archive containing an empty Corefile (no host entries).
func emptyCorefileTar() string {
	return testutil.TarArchive("Corefile", emptyCorefileContent())
}

// happyOnCall returns an OnCall function that handles the full Create flow
// for a cluster with controller + 1 managed worker. The optional override
// function can intercept specific calls; returning (result, true) uses the
// override result, returning (_, false) falls through to the default.
func happyOnCall(t *testing.T, exitErr *exec.ExitError, override func(args []string, stdin string) (mock.Result, bool)) func([]string, string) mock.Result {
	t.Helper()
	return func(args []string, stdin string) mock.Result {
		if override != nil {
			if r, ok := override(args, stdin); ok {
				return r
			}
		}
		joined := strings.Join(args, " ")

		// Cleanup: ListContainers and ListVolumes for deleteClusterResources
		if args[0] == "ps" || (args[0] == "volume" && args[1] == "ls") {
			return mock.Result{Stdout: ""}
		}
		// Cleanup: resource removal (best-effort during rollback)
		if args[0] == "network" && args[1] == "disconnect" {
			return mock.Result{}
		}
		if args[0] == "network" && args[1] == "rm" {
			return mock.Result{}
		}
		if args[0] == "volume" && args[1] == "rm" {
			return mock.Result{}
		}

		// PreflightCheck: exists checks → "not found"
		if len(args) >= 2 && args[1] == "inspect" {
			switch args[0] {
			case "network", "volume", "container":
				return mock.Result{Stderr: "Error: No such object\n", Err: exitErr}
			}
		}

		// resolveInfra
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.0.2",
			})}
		}
		if args[0] == "exec" && args[1] == "sind-ssh" && len(args) > 2 && args[2] == "cat" {
			return mock.Result{Stdout: "ssh-ed25519 AAAA-test-key\n"}
		}
		if args[0] == "run" && args[1] == "--rm" {
			return mock.Result{Stdout: "slurm 25.11.0\n"}
		}

		// createResources
		if args[0] == "network" && args[1] == "create" {
			return mock.Result{Stdout: "net-id\n"}
		}
		if args[0] == "volume" && args[1] == "create" {
			return mock.Result{}
		}
		if args[0] == "create" {
			return mock.Result{Stdout: "cid\n"}
		}
		if args[0] == "run" {
			return mock.Result{Stdout: "cid\n"}
		}
		if args[0] == "cp" {
			if len(args) == 3 && args[2] == "-" && strings.Contains(args[1], ":") {
				return mock.Result{Stdout: emptyCorefileTar()}
			}
			return mock.Result{}
		}
		if args[0] == "rm" {
			return mock.Result{}
		}
		if args[0] == "network" && args[1] == "connect" {
			return mock.Result{}
		}
		if args[0] == "start" {
			return mock.Result{}
		}
		if args[0] == "inspect" {
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}
		}
		if args[0] == "exec" {
			if args[1] == "-i" {
				return mock.Result{}
			}
			container := args[1]
			if len(args) > 2 {
				switch cmd := args[2]; {
				case cmd == "sh" && strings.Contains(joined, "is-system-running"):
					return mock.Result{Stdout: "running\n"}
				case cmd == "bash" && strings.Contains(joined, "/dev/tcp"):
					return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
				case cmd == "sh" && strings.Contains(joined, "ssh-keyscan"):
					return mock.Result{Stdout: "localhost ssh-ed25519 AAAA-hostkey-" + container + "\n"}
				case cmd == "systemctl" && len(args) > 3 && args[3] == "enable":
					return mock.Result{}
				case cmd == "scontrol":
					return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
				case cmd == "systemctl" && len(args) > 3 && args[3] == "is-active":
					return mock.Result{Stdout: "active\n"}
				}
			}
			return mock.Result{}
		}
		if args[0] == "kill" {
			return mock.Result{}
		}
		assert.Failf(t, "unexpected docker call", "%v", args)
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

// createCfg returns a minimal cluster config with 1 controller + 1 managed worker.
func createCfg() *config.Cluster {
	return &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}
}

func TestCreateResources_BackupControllerStateVolume(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	client := docker.NewClient(&m)

	cfg := createCfg()
	cfg.Nodes[0].BackupController = true
	require.NoError(t, createResources(t.Context(), client, mesh.DefaultRealm, cfg, "sind-ssh", startPull(t.Context(), nil, client, nil)))

	var created []string
	for _, c := range m.Calls {
		if len(c.Args) > 2 && c.Args[0] == "volume" && c.Args[1] == "create" {
			created = append(created, c.Args[len(c.Args)-1])
		}
	}
	assert.Contains(t, created, "sind-dev-state")
}

// createdVolumes runs createResources for cfg and returns the volumes it
// created.
func createdVolumes(t *testing.T, cfg *config.Cluster) []string {
	t.Helper()
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	require.NoError(t, createResources(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, cfg, "sind-ssh", startPull(t.Context(), nil, docker.NewClient(&m), nil)))

	var created []string
	for _, c := range m.Calls {
		if len(c.Args) > 2 && c.Args[0] == "volume" && c.Args[1] == "create" {
			created = append(created, c.Args[len(c.Args)-1])
		}
	}
	return created
}

func TestCreateResources_DataVolume(t *testing.T) {
	tests := []struct {
		name       string
		ds         config.DataStorage
		wantVolume bool
	}{
		{"no storage", config.DataStorage{}, true},
		{"volume", config.DataStorage{Type: config.StorageVolume}, true},
		// The nodes mount the volume, so it must exist with its labels
		// rather than be created by docker run without them.
		{"volume ignores hostPath", config.DataStorage{Type: config.StorageVolume, HostPath: "/srv/data"}, true},
		{"hostPath", config.DataStorage{Type: config.StorageHostPath, HostPath: "/srv/data"}, false},
		{"hostPath without type", config.DataStorage{HostPath: "/srv/data"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createCfg()
			cfg.Storage.DataStorage = tt.ds
			created := createdVolumes(t, cfg)
			if tt.wantVolume {
				assert.Contains(t, created, "sind-dev-data")
			} else {
				assert.NotContains(t, created, "sind-dev-data")
			}
		})
	}
}

func TestCreate_FullCluster(t *testing.T) {
	exitErr := notFoundErr(t)

	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, nil)
	m.OnStart = pipes.OnStart

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), 1*time.Millisecond)

	require.NoError(t, err)
	require.NotNil(t, cluster)
	assert.Equal(t, "dev", cluster.Name)
	assert.Equal(t, "25.11.0", cluster.SlurmVersion)
	assert.Equal(t, StateRunning, cluster.State)
	require.Len(t, cluster.Nodes, 2)
	assert.Equal(t, "controller", cluster.Nodes[0].Name)
	assert.Equal(t, config.RoleController, cluster.Nodes[0].Role)
	assert.Equal(t, "worker-0", cluster.Nodes[1].Name)
	assert.Equal(t, config.RoleWorker, cluster.Nodes[1].Role)
	assert.Equal(t, StateRunning, cluster.Nodes[0].State)
	assert.Equal(t, StateRunning, cluster.Nodes[1].State)
}

func TestCreate_NonPositiveReadinessInterval(t *testing.T) {
	// A zero interval, Go's usual "default", polls every
	// DefaultReadinessInterval instead of panicking in time.NewTicker.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), 0)
	require.NoError(t, err)
	assert.Equal(t, probe.DefaultInterval, DefaultReadinessInterval)
}

func TestCreate_InvalidConfig(t *testing.T) {
	// Without ApplyDefaults the config has no nodes; Create fails before it
	// creates anything instead of returning a cluster without nodes.
	var m mock.Executor
	client := docker.NewClient(&m)
	_, err := Create(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), &config.Cluster{Kind: "Cluster", Name: "dev"}, time.Millisecond)
	require.EqualError(t, err, "invalid cluster config: exactly one controller required, got 0")
	assert.Empty(t, m.Calls)

	// Validate guards the names the node scripts use unquoted.
	cfg := createCfg()
	cfg.Users = []config.User{{Name: "bad name", UID: 1000}}
	_, err = Create(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), cfg, time.Millisecond)
	require.ErrorContains(t, err, "invalid cluster config: ")
	assert.Empty(t, m.Calls)
}

func TestCreate_RealmMismatch(t *testing.T) {
	var m mock.Executor
	client := docker.NewClient(&m)
	cfg := createCfg()
	cfg.Realm = "ci"
	_, err := Create(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), cfg, time.Millisecond)
	require.EqualError(t, err, `config realm "ci" differs from the mesh manager's realm "sind"`)
	assert.Empty(t, m.Calls)
}

func TestCreate_ConfigRealmMatches(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	cfg := createCfg()
	cfg.Realm = mesh.DefaultRealm
	_, err := Create(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), cfg, time.Millisecond)
	require.NoError(t, err)
}

func TestCreate_BackupController(t *testing.T) {
	exitErr := notFoundErr(t)

	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\nSlurmctld(backup) at controller-backup is UP\n"}, true
		}
		return mock.Result{}, false
	})
	m.OnStart = pipes.OnStart

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cfg := createCfg()
	cfg.Nodes[0].BackupController = true
	cluster, err := Create(ctx, client, meshMgr, cfg, 1*time.Millisecond)

	require.NoError(t, err)
	require.Len(t, cluster.Nodes, 3)
	assert.Equal(t, "controller-backup", cluster.Nodes[1].Name)
	assert.Equal(t, config.RoleController, cluster.Nodes[1].Role)

	// slurmctld is enabled on both controllers; each container mounts the
	// shared state volume.
	enabled := map[string]bool{}
	stateMounts := map[string]bool{}
	for _, c := range m.Calls {
		a := c.Args
		if a[0] == "exec" && len(a) > 5 && a[2] == "systemctl" && a[3] == "enable" && a[5] == "slurmctld" {
			enabled[a[1]] = true
		}
		if a[0] == "create" && slices.Contains(a, "sind-dev-state:/var/spool/slurmctld:rw") {
			stateMounts[testutil.ArgValues(a, "--name")[0]] = true
		}
	}
	assert.Equal(t, map[string]bool{"sind-dev-controller": true, "sind-dev-controller-backup": true}, enabled)
	assert.Equal(t, map[string]bool{"sind-dev-controller": true, "sind-dev-controller-backup": true}, stateMounts)
}

func TestCreate_BackupControllerNotReady(t *testing.T) {
	exitErr := notFoundErr(t)

	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\nSlurmctld(backup) at controller-backup is DOWN\n"}, true
		}
		return mock.Result{}, false
	})
	m.OnStart = pipes.OnStart

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	cfg := createCfg()
	cfg.Nodes[0].BackupController = true
	_, err := Create(ctx, client, meshMgr, cfg, 1*time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller-backup is DOWN")
}

func TestCreate_CVMFS(t *testing.T) {
	tests := []struct {
		name      string
		plugins   string
		wantMount string
		wantLabel string
		wantProbe bool
	}{
		{"volume plugin", "cvmfs:latest\n", "type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly", "sind.cvmfs=volume", false},
		{"host bind mount", "", "type=bind,source=/cvmfs,target=/cvmfs,readonly,bind-propagation=rslave", "sind.cvmfs=hostPath", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipes := &mock.Pipes{}
			defer pipes.CloseAll()

			var m mock.Executor
			m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
				if args[0] == "plugin" {
					return mock.Result{Stdout: tt.plugins}, true
				}
				return mock.Result{}, false
			})
			m.OnStart = pipes.OnStart

			client := docker.NewClient(&m)
			meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			cfg := createCfg()
			cfg.Storage.CVMFS = true
			_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
			require.NoError(t, err)

			var probed bool
			mounted := map[string]bool{}
			for _, c := range m.Calls {
				a := c.Args
				if a[0] == "run" && slices.Contains(a, "--entrypoint") {
					probed = true
				}
				if a[0] == "create" && slices.Contains(a, "--hostname") {
					assert.Equal(t, []string{tt.wantMount}, testutil.ArgValues(a, "--mount"))
					assert.Contains(t, testutil.ArgValues(a, "--label"), tt.wantLabel)
					mounted[testutil.ArgValues(a, "--name")[0]] = true
				}
			}
			assert.Equal(t, tt.wantProbe, probed)
			assert.Equal(t, map[string]bool{"sind-dev-controller": true, "sind-dev-worker-0": true}, mounted)
		})
	}
}

func TestCreate_CVMFSUnavailable(t *testing.T) {
	// Neither the plugin nor the host's /cvmfs: creation fails and cleans
	// up whatever the other preparation branches created.
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "plugin" {
			return mock.Result{}, true
		}
		if args[0] == "run" && slices.Contains(args, "--entrypoint") {
			return mock.Result{Stderr: "bind source path does not exist: /cvmfs", Err: fmt.Errorf("exit status 125")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cfg := createCfg()
	cfg.Storage.CVMFS = true
	cluster, err := Create(t.Context(), client, meshMgr, cfg, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.cvmfs")
	assert.Nil(t, cluster)
	for _, c := range m.Calls {
		assert.False(t, c.Args[0] == "create" && slices.Contains(c.Args, "--hostname"), "no node container: %v", c.Args)
	}
}

func TestCreate_NoCVMFS(t *testing.T) {
	// Without storage.cvmfs, sind neither looks for the plugin nor probes
	// the host.
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
	for _, c := range m.Calls {
		assert.NotEqual(t, "plugin", c.Args[0])
		assert.False(t, c.Args[0] == "run" && slices.Contains(c.Args, "--entrypoint"), "no probe: %v", c.Args)
		assert.Empty(t, testutil.ArgValues(c.Args, "--mount"))
	}
}

func TestCreate_PreflightFails(t *testing.T) {
	// Network already exists → preflight returns conflict error.
	// resolveInfra runs in parallel, so its happy-path responses are supplied
	// too; otherwise the race could surface a resolveInfra error instead.
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
			return mock.Result{}, true // network exists → preflight conflict
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.ErrorIs(t, err, ErrClusterExists)
	assert.Contains(t, err.Error(), "conflicting resources")
	assert.Nil(t, cluster)
}

func TestCreate_ResolveInfraFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return mock.Result{Err: fmt.Errorf("container not running")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container")
	assert.Nil(t, cluster)
}

func TestCreate_CreateResourcesFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "network" && args[1] == "create" {
			return mock.Result{Err: fmt.Errorf("network quota exceeded")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating cluster network")
	assert.Nil(t, cluster)
}

func TestCreate_VolumeCreateFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "volume" && args[1] == "create" {
			return mock.Result{Err: fmt.Errorf("volume quota exceeded")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume")
	assert.Nil(t, cluster)
}

func TestCreate_SSHRelayConnectFails(t *testing.T) {
	exitErr := notFoundErr(t)
	connectCount := 0
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// First network connect is SSH relay → cluster network; fail it.
		if args[0] == "network" && args[1] == "connect" {
			connectCount++
			if connectCount == 1 {
				return mock.Result{Err: fmt.Errorf("network connect denied")}, true
			}
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting SSH relay")
	assert.Nil(t, cluster)
}

func TestCreate_NodeCreationFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// Let helper containers succeed, fail node containers
		if args[0] == "create" && !strings.Contains(strings.Join(args, " "), "helper") && !strings.Contains(strings.Join(args, " "), "busybox") {
			return mock.Result{Err: fmt.Errorf("image not found")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "node")
	assert.Nil(t, cluster)
}

func TestCreate_SetupNodesFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// Probes inspect the node: return "created" instead of "running" to fail ContainerRunning
		if args[0] == "inspect" && strings.HasPrefix(args[1], "sind-dev-") {
			return mock.Result{Stdout: inspectJSON(t, args[1], "created", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), 10*time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "waiting for")
	assert.Nil(t, cluster)
}

func TestCreate_RegisterMeshFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// CopyFromContainer for Corefile fails
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns") {
			return mock.Result{Err: fmt.Errorf("dns container stopped")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registering DNS")
	assert.Nil(t, cluster)
}

func TestCreate_EnableSlurmFails(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabling")
	assert.Nil(t, cluster)
}

func TestCreate_CleansUpOnFailure(t *testing.T) {
	// When enableSlurm fails (after resources + nodes exist), cleanup should run.
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	// Verify cleanup ran: look for "docker ps" calls from diagnostics and
	// deleteClusterResources. Preflight also calls ps once, hence 3 total.
	var psCalls int
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			psCalls++
		}
	}
	assert.Equal(t, 3, psCalls, "preflight + diagnostics + deletion each call ListContainers")
}

func TestCreate_CleansUpMeshWhenFreshlyCreated(t *testing.T) {
	// When mesh was freshly created and Create fails, mesh should also be cleaned up.
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	// Simulate freshly created mesh by calling EnsureMeshNetwork on a "new" network.
	// The happyOnCall mock returns "not found" for network inspect, triggering creation.
	_ = meshMgr.EnsureMeshNetwork(t.Context())
	require.True(t, meshMgr.Created())

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	// Verify mesh cleanup ran: CleanupMesh issues `rm -f` for the mesh
	// helpers after the cluster cleanup's "docker ps" call.
	var meshRmAfterPs int
	seenPs := false
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			seenPs = true
		}
		if seenPs && len(call.Args) >= 3 && call.Args[0] == "rm" && call.Args[1] == "-f" &&
			(strings.Contains(call.Args[len(call.Args)-1], "sind-ssh") || strings.Contains(call.Args[len(call.Args)-1], "sind-dns")) {
			meshRmAfterPs++
		}
	}
	assert.GreaterOrEqual(t, meshRmAfterPs, 1, "mesh cleanup should remove mesh containers")
}

func TestCreate_SkipsMeshCleanupWhenPreExisting(t *testing.T) {
	// When mesh already existed, cleanup should NOT remove it.
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	// Don't call EnsureMeshNetwork → Created() stays false (pre-existing mesh).
	require.False(t, meshMgr.Created())

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	// After the "docker ps" cleanup call, there should be NO mesh-helper
	// `rm -f` calls.
	seenPs := false
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			seenPs = true
			continue
		}
		if seenPs && len(call.Args) >= 3 && call.Args[0] == "rm" && call.Args[1] == "-f" &&
			(strings.Contains(call.Args[len(call.Args)-1], "sind-ssh") || strings.Contains(call.Args[len(call.Args)-1], "sind-dns")) {
			require.Fail(t, "mesh cleanup should not run when mesh was pre-existing")
		}
	}
}

func TestCreate_NoCleanupOnPreflightFailure(t *testing.T) {
	// When preflight fails (before resourcesCreated=true), cluster cleanup
	// should NOT run. resolveInfra is allowed to succeed in parallel; the
	// absence of network/volume rm calls proves no cluster cleanup happened.
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
			return mock.Result{}, true // network exists → preflight conflict
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	for _, call := range m.Calls {
		if len(call.Args) >= 2 && call.Args[1] == "rm" &&
			(call.Args[0] == "network" || call.Args[0] == "volume") {
			require.Fail(t, "cleanup should not run when preflight fails")
		}
	}
}

func TestCreate_MeshCleanupOnResolveInfraFailure(t *testing.T) {
	// When resolveInfra fails and mesh was freshly created, mesh should be cleaned up.
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return mock.Result{Err: fmt.Errorf("container not running")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_ = meshMgr.EnsureMeshNetwork(t.Context())
	require.True(t, meshMgr.Created())

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	// Mesh cleanup should run: `rm -f` for mesh containers.
	var meshRm int
	for _, call := range m.Calls {
		if len(call.Args) >= 3 && call.Args[0] == "rm" && call.Args[1] == "-f" &&
			(strings.Contains(call.Args[len(call.Args)-1], "sind-ssh") || strings.Contains(call.Args[len(call.Args)-1], "sind-dns")) {
			meshRm++
		}
	}
	assert.GreaterOrEqual(t, meshRm, 1, "mesh cleanup should run on resolveInfra failure")
}

// meshRemoved reports whether m removed a mesh container.
func meshRemoved(m *mock.Executor) bool {
	for _, call := range m.Calls {
		if len(call.Args) >= 4 && call.Args[0] == "rm" && (call.Args[3] == "sind-ssh" || call.Args[3] == "sind-dns") {
			return true
		}
	}
	return false
}

// isRealmListing reports whether args list every container of the realm,
// as HasOtherClusters does.
func isRealmListing(args []string) bool {
	return args[0] == "ps" && args[len(args)-1] == "label=sind.realm=sind"
}

// TestCreate_KeepsMeshOfOtherClusters covers a Manager that created the mesh
// for an earlier cluster and then serves a create that fails: the mesh stays
// for the earlier cluster.
func TestCreate_KeepsMeshOfOtherClusters(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		if isRealmListing(args) {
			return mock.Result{Stdout: testutil.NDJSON(testutil.PsEntry{ID: "x", Names: "sind-first-controller",
				State: "running", Image: "img", Labels: "sind.cluster=first,sind.realm=sind"})}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_ = meshMgr.EnsureMeshNetwork(t.Context())
	require.True(t, meshMgr.Created())

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.False(t, meshRemoved(&m), "mesh kept for cluster first")
}

// TestCreate_KeepsMeshOnDuplicateName covers a create of a cluster that
// exists: the preflight check fails, and the existing cluster keeps its
// mesh.
func TestCreate_KeepsMeshOnDuplicateName(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if len(args) >= 2 && args[0] == "network" && args[1] == "inspect" && args[2] == "sind-dev-net" {
			return mock.Result{}, true // network exists → preflight conflict
		}
		if isRealmListing(args) {
			return mock.Result{Stdout: testutil.NDJSON(testutil.PsEntry{ID: "x", Names: "sind-dev-controller",
				State: "running", Image: "img", Labels: "sind.cluster=dev,sind.realm=sind"})}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_ = meshMgr.EnsureMeshNetwork(t.Context())
	require.True(t, meshMgr.Created())

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.False(t, meshRemoved(&m), "mesh kept for the existing cluster dev")
}

// TestCreate_KeepsMeshWhenUsageUnknown covers a rollback that cannot tell
// whether clusters use the mesh: it keeps the mesh.
func TestCreate_KeepsMeshWhenUsageUnknown(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		if isRealmListing(args) {
			return mock.Result{Err: fmt.Errorf("docker daemon unavailable")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_ = meshMgr.EnsureMeshNetwork(t.Context())

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemctl failed")
	assert.False(t, meshRemoved(&m))
}

// TestCreate_RollbackSkipsDeregistrationOfRemovedMesh covers the rollback
// of a create that made the mesh: the mesh goes, so the nodes are not
// deregistered from it first.
func TestCreate_RollbackSkipsDeregistrationOfRemovedMesh(t *testing.T) {
	failed := false
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			failed = true
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		if failed && args[0] == "ps" && args[len(args)-1] == "label=sind.cluster=dev" {
			return mock.Result{Stdout: testutil.NDJSON(testutil.PsEntry{ID: "a", Names: "sind-dev-controller",
				State: "running", Image: "img", Labels: "sind.cluster=dev,sind.realm=sind"})}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_ = meshMgr.EnsureMeshNetwork(t.Context())

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.True(t, meshRemoved(&m))
	var reads int
	for _, call := range m.Calls {
		if call.Args[0] == "exec" && call.Args[1] == "sind-ssh" && len(call.Args) > 3 && call.Args[3] == "/root/.ssh/known_hosts" {
			reads++
		}
	}
	assert.Equal(t, 1, reads, "known_hosts read once, by the registration, not by the rollback")
}

func TestResolveMeshInfra_NoDNSContainer(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			return mock.Result{Stderr: testutil.NoSuchContainer(args[1]), Err: notFoundErr(t)}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	_, _, err := resolveMeshInfra(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container: sind-dns not found")
}

// TestResolveMeshInfra_StartsStoppedMesh covers a create after a host
// reboot: the stopped mesh starts, DNS first, and new nodes get the
// address the DNS container has now.
func TestResolveMeshInfra_StartsStoppedMesh(t *testing.T) {
	started := map[string]bool{}
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect" && args[1] == "sind-dns":
			state, ips := "exited", map[docker.NetworkName]string(nil)
			if started["sind-dns"] {
				state, ips = "running", map[docker.NetworkName]string{"sind-mesh": "10.0.0.2"}
			}
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", state, ips)}
		case args[0] == "inspect" && args[1] == "sind-ssh":
			return mock.Result{Stdout: inspectJSON(t, "sind-ssh", "exited", nil)}
		case args[0] == "start":
			if args[1] == "sind-ssh" {
				assert.True(t, started["sind-dns"], "DNS starts before the relay")
			}
			started[args[1]] = true
			return mock.Result{}
		case args[0] == "exec" && args[1] == "sind-ssh":
			return mock.Result{Stdout: "ssh-ed25519 AAAA-key\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	dnsIP, key, err := resolveMeshInfra(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm))

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.2", dnsIP)
	assert.Equal(t, "ssh-ed25519 AAAA-key\n", key)
	assert.True(t, started["sind-ssh"])
}

func TestCreate_CleanupResourcesError(t *testing.T) {
	// When Create fails and the cleanup itself fails, the error carries the
	// original failure and the cleanup's, so the caller learns that
	// resources were left behind.
	exitErr := notFoundErr(t)
	inCleanup := false
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// Make enableSlurm fail to trigger cleanup.
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			inCleanup = true
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		// Make deleteClusterResources fail: ListContainers errors during cleanup.
		if inCleanup && args[0] == "ps" {
			return mock.Result{Err: fmt.Errorf("docker daemon unavailable")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemctl failed")
	assert.Contains(t, err.Error(), "\nrolling back: removing cluster resources (sind delete cluster removes what is left): ")
	assert.Contains(t, err.Error(), "docker daemon unavailable")
}

func TestCreate_CleanupMeshError(t *testing.T) {
	// When Create fails with freshly-created mesh and mesh cleanup also fails,
	// the error carries the original failure and the mesh cleanup's.
	exitErr := notFoundErr(t)
	inCleanup := false
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// Make enableSlurm fail to trigger cleanup.
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			inCleanup = true
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		// Make mesh cleanup fail: rm -f on a mesh container errors with a
		// non-IsNotFound failure (real docker daemon issue).
		if inCleanup && args[0] == "rm" && len(args) >= 3 && args[1] == "-f" &&
			(strings.Contains(args[len(args)-1], "sind-ssh") || strings.Contains(args[len(args)-1], "sind-dns")) {
			return mock.Result{Err: fmt.Errorf("docker daemon unavailable")}, true
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_ = meshMgr.EnsureMeshNetwork(t.Context())
	require.True(t, meshMgr.Created())

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemctl failed")
	assert.Contains(t, err.Error(), "\nrolling back: removing the mesh: ")
}

func TestCreate_UnmanagedComputeSkipsSlurm(t *testing.T) {
	exitErr := notFoundErr(t)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g",
				Managed: testutil.Ptr(false)},
		},
	}

	var slurmCmds []string
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			slurmCmds = append(slurmCmds, args[1])
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, cluster.Nodes, 2)
	// Only controller should get slurm enabled, not unmanaged worker-0.
	assert.Equal(t, []string{"sind-dev-controller"}, slurmCmds)
}

func TestCreate_UnmanagedCluster(t *testing.T) {
	exitErr := notFoundErr(t)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Managed: testutil.Ptr(false), BackupController: true,
				Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleSubmitter, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Count: 1, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, nil)
	m.OnStart = pipes.OnStart

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)

	require.NoError(t, err)
	assert.Empty(t, cluster.SlurmVersion, "no version discovered")
	require.Len(t, cluster.Nodes, 4)

	var volumes, nodes []string
	for _, c := range m.Calls {
		args := c.Args
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "run" && args[1] == "--rm":
			assert.Failf(t, "Slurm version discovered", "%v", args)
		case args[0] == "volume" && args[1] == "create":
			volumes = append(volumes, args[len(args)-1])
		case args[0] == "create" && strings.Contains(joined, "config-helper"):
			assert.Failf(t, "Slurm configuration written", "%v", args)
		case args[0] == "create":
			name, _ := testutil.ArgValue(args, "--name")
			nodes = append(nodes, name)
			labels := testutil.ArgValues(args, "--label")
			assert.Contains(t, labels, LabelManaged+"=false", name)
			assert.Contains(t, labels, LabelSlurmVersion+"=", name)
		case args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable",
			args[0] == "exec" && len(args) > 2 && args[2] == "scontrol":
			assert.Failf(t, "Slurm daemon touched", "%v", args)
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-config", "sind-dev-munge", "sind-dev-data", "sind-dev-state"}, volumes)
	assert.ElementsMatch(t, []string{"sind-dev-controller", "sind-dev-controller-backup", "sind-dev-submitter", "sind-dev-worker-0"}, nodes)
	assert.True(t, slices.ContainsFunc(m.Calls, func(c mock.Call) bool {
		return c.Args[0] == "run" && slices.Contains(c.Args, "sind-dev-munge-helper")
	}), "munge key written")
}

func TestCreate_SubmitterSkipsSlurm(t *testing.T) {
	exitErr := notFoundErr(t)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleSubmitter, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleWorker, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
		},
	}

	var slurmCmds []string
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			slurmCmds = append(slurmCmds, args[1])
		}
		return mock.Result{}, false
	})

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, cluster.Nodes, 3)
	assert.Equal(t, "submitter", cluster.Nodes[1].Name)
	// The controller gets slurmctld and the worker slurmd; the submitter
	// is skipped entirely.
	slices.Sort(slurmCmds)
	assert.Equal(t, []string{"sind-dev-controller", "sind-dev-worker-0"}, slurmCmds)
}

// --- Direct tests for unexported helpers ---

func TestResolveInfra_SSHKeyError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" && (args[1] == "sind-dns" || args[1] == "sind-ssh") {
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.0.2",
			})}
		}
		if args[0] == "exec" && args[1] == "sind-ssh" {
			return mock.Result{Err: fmt.Errorf("ssh container crashed")}
		}
		if args[0] == "run" && args[1] == "--rm" {
			return mock.Result{Stdout: "slurm 25.11.0\n"}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	cfg := createCfg()

	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, _, _, err := resolveInfra(t.Context(), client, meshMgr, cfg, startPull(t.Context(), nil, client, nil))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH public key")
}

func TestResolveInfra_SlurmVersionError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" && (args[1] == "sind-dns" || args[1] == "sind-ssh") {
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.0.2",
			})}
		}
		if args[0] == "exec" && args[1] == "sind-ssh" {
			return mock.Result{Stdout: "ssh-ed25519 AAAA-key\n"}
		}
		if args[0] == "run" && args[1] == "--rm" {
			return mock.Result{Err: fmt.Errorf("image pull failed")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, _, _, err := resolveInfra(t.Context(), client, meshMgr, createCfg(), startPull(t.Context(), nil, client, nil))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "discovering Slurm version")
}

func TestSetupNodes_InspectError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			// First call: ContainerRunning probe → running
			name := args[1]
			return mock.Result{Stdout: inspectJSON(t, name, "running", nil)}
		}
		if args[0] == "exec" {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "is-system-running") {
				return mock.Result{Stdout: "running\n"}
			}
			if strings.Contains(joined, "/dev/tcp") {
				return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			}
			if strings.Contains(joined, "is-active") {
				return mock.Result{Stdout: "active\n"}
			}
			if strings.Contains(joined, "ssh-keyscan") {
				return mock.Result{Stdout: "localhost ssh-ed25519 AAAA-hostkey\n"}
			}
		}
		return mock.Result{}
	}

	// Override: after probes pass, the second inspect (for IP collection) fails.
	callCount := 0
	origOnCall := m.OnCall
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && strings.HasPrefix(args[1], "sind-dev-") {
			callCount++
			if callCount > 1 {
				return mock.Result{Err: fmt.Errorf("inspect network error")}
			}
		}
		return origOnCall(args, stdin)
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, err := setupNodes(ctx, client, mgr, mesh.DefaultRealm, "dev", "ssh-key", configs, &readiness{interval: time.Millisecond}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting node controller")
}

func TestSetupNodes_WaitsForMunge(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", nil)}
		}
		if args[0] == "exec" {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "is-system-running"):
				return mock.Result{Stdout: "running\n"}
			case strings.Contains(joined, "/dev/tcp"):
				return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			case strings.HasSuffix(joined, "systemctl is-active munge"):
				return mock.Result{Stdout: "activating\n", Err: fmt.Errorf("exit status 3")}
			}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: config.RoleWorker}}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, err := setupNodes(ctx, client, mgr, mesh.DefaultRealm, "dev", "ssh-key", configs, &readiness{interval: time.Millisecond}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "probe munge")
}

func TestSetupNodes_InjectKeyError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}
		}
		if args[0] == "exec" {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "is-system-running"):
				return mock.Result{Stdout: "running\n"}
			case strings.Contains(joined, "/dev/tcp"):
				return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			case strings.Contains(joined, "is-active"):
				return mock.Result{Stdout: "active\n"}
			case strings.Contains(joined, "ssh-keyscan"):
				return mock.Result{Err: fmt.Errorf("permission denied")}
			}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, err := setupNodes(ctx, client, mgr, mesh.DefaultRealm, "dev", "ssh-key", configs, &readiness{interval: time.Millisecond}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "setting up SSH on controller: injecting SSH key")
}

func TestSetupNodes_HostKeyError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" {
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.1",
			})}
		}
		if args[0] == "exec" {
			if args[1] == "-i" {
				return mock.Result{}
			}
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(joined, "is-system-running"):
				return mock.Result{Stdout: "running\n"}
			case strings.Contains(joined, "/dev/tcp"):
				return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
			case strings.Contains(joined, "is-active"):
				return mock.Result{Stdout: "active\n"}
			case strings.Contains(joined, "ssh-keyscan"):
				return mock.Result{Stdout: "# localhost:22 SSH-2.0-OpenSSH_9.0\n"}
			}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	_, err := setupNodes(ctx, client, mgr, mesh.DefaultRealm, "dev", "ssh-key", configs, &readiness{interval: time.Millisecond}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "setting up SSH on controller: no ed25519 host key found")
}

func TestRegisterMesh_DNSError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		// CopyFromContainer (read Corefile) fails
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" {
			return mock.Result{Err: fmt.Errorf("dns container not running")}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	configs := []RunConfig{{ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}
	results := []nodeResult{{
		info:    &docker.ContainerInfo{ID: "id1", IPs: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"}},
		hostKey: "ssh-ed25519 AAAA-key",
	}}

	_, err := registerMesh(t.Context(), meshMgr, "dev", "25.11.0", configs, results)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registering DNS")
}

func TestRegisterMesh_KnownHostError(t *testing.T) {
	var m mock.Executor
	callIdx := 0
	m.OnCall = func(args []string, _ string) mock.Result {
		// CopyFromContainer (read Corefile)
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" {
			return mock.Result{Stdout: emptyCorefileTar()}
		}
		// CopyToContainer (write Corefile)
		if args[0] == "cp" && args[1] == "-" {
			return mock.Result{}
		}
		// InspectContainer (state check before DNS reload)
		if args[0] == "inspect" && len(args) >= 2 && strings.Contains(args[1], "sind-dns") {
			return mock.Result{Stdout: dnsRunningInspectJSON}
		}
		// SignalContainer / StartContainer
		if args[0] == "kill" || args[0] == "start" {
			return mock.Result{}
		}
		// AppendFile → ExecWithStdin (exec -i)
		if args[0] == "exec" && args[1] == "-i" {
			callIdx++
			if callIdx == 1 {
				return mock.Result{Err: fmt.Errorf("container stopped")}
			}
			return mock.Result{}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	configs := []RunConfig{{ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}
	results := []nodeResult{{
		info:    &docker.ContainerInfo{ID: "id1", IPs: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"}},
		hostKey: "ssh-ed25519 AAAA-key",
	}}

	_, err := registerMesh(t.Context(), meshMgr, "dev", "25.11.0", configs, results)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "registering host key")
}

func TestLogExtraPrivileges(t *testing.T) {
	tests := []struct {
		name string
		cfg  RunConfig
		want string // the notice's config, empty for none
	}{
		{"no privileges", RunConfig{ShortName: "worker-0"}, ""},
		{"data volume and cvmfs volume", RunConfig{ShortName: "worker-0", CVMFS: config.StorageVolume}, ""},
		{"capAdd", RunConfig{ShortName: "worker-0", CapAdd: []string{"SYS_ADMIN", "NET_ADMIN"}}, "capAdd=[SYS_ADMIN,NET_ADMIN]"},
		{"devices", RunConfig{ShortName: "worker-0", Devices: []string{"/dev/fuse"}}, "devices=[/dev/fuse]"},
		{"securityOpt", RunConfig{ShortName: "worker-0", SecurityOpt: []string{"apparmor=unconfined"}}, "securityOpt=[apparmor=unconfined]"},
		{"data host path", RunConfig{ShortName: "worker-0", DataHostPath: "/home/u/proj"}, "hostMounts=[/home/u/proj:/data]"},
		{"all", RunConfig{
			ShortName:     "worker-0",
			CapAdd:        []string{"SYS_ADMIN"},
			Devices:       []string{"/dev/fuse"},
			SecurityOpt:   []string{"apparmor=unconfined"},
			DataHostPath:  "/",
			DataMountPath: "/host",
			CVMFS:         config.StorageHostPath,
		}, "capAdd=[SYS_ADMIN] devices=[/dev/fuse] securityOpt=[apparmor=unconfined] hostMounts=[/:/host,/cvmfs:/cvmfs:ro]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			logExtraPrivileges(sindlog.With(t.Context(), logger), []RunConfig{tt.cfg})

			if tt.want == "" {
				assert.Empty(t, buf.String())
				return
			}
			assert.Contains(t, buf.String(), `level=INFO msg="extra privileges" node=worker-0 config="`+tt.want+`"`)
		})
	}
}

func TestEnableSlurm_ProbeTimeout(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{}
		}
		// scontrol ping always fails → probe times out
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Err: fmt.Errorf("slurmctld not responding")}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController, Managed: true}}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := enableSlurm(ctx, client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestEnableSlurm_DBFirst(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) > 3 && args[2] == "systemctl" && args[3] == "is-active" {
			return mock.Result{Stdout: "active\n"}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "db", Role: config.RoleDB, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: config.RoleWorker, Managed: true},
	}

	err := enableSlurm(t.Context(), client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond, nil)
	require.NoError(t, err)

	// The db node's four calls (mariadb, SQL, slurmdbd, slurmdbd probe) come
	// first and in order; slurmctld and slurmd follow in either order.
	require.GreaterOrEqual(t, len(m.Calls), 4)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "enable", "--now", "mariadb"}, m.Calls[0].Args)
	assert.Equal(t, "mysql", m.Calls[1].Args[2])
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "enable", "--now", "slurmdbd"}, m.Calls[2].Args)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "is-active", "slurmdbd"}, m.Calls[3].Args)
	var enabled []string
	for _, call := range m.Calls[4:] {
		assert.NotEqual(t, "sind-dev-db", call.Args[1], "db handled only in the first phase")
		if len(call.Args) > 4 && call.Args[3] == "enable" {
			enabled = append(enabled, call.Args[1]+" "+call.Args[5])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-controller slurmctld", "sind-dev-worker-0 slurmd"}, enabled)
}

func TestEnableSlurm_DBFailedIsTerminal(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) > 4 && args[2] == "systemctl" && args[3] == "is-active" && args[4] == "slurmdbd" {
			return mock.Result{Stdout: "failed\n", Err: exitErr}
		}
		if len(args) > 2 && args[2] == "journalctl" {
			return mock.Result{Stdout: "slurmdbd: fatal: example\n"}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "db", Role: config.RoleDB, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController, Managed: true},
	}

	// No deadline: a failed slurmdbd unit must end the wait on its own.
	err := enableSlurm(t.Context(), client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmdbd failed:\nslurmdbd: fatal: example")
	for _, call := range m.Calls {
		assert.NotEqual(t, "sind-dev-controller", call.Args[1], "controller untouched after the db failed")
	}
}

func TestEnableSlurm_DBEnableError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("mariadb failed"))
	client := docker.NewClient(&m)
	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "db", Role: config.RoleDB, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController, Managed: true},
	}

	err := enableSlurm(t.Context(), client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabling mariadb on db")
	require.Len(t, m.Calls, 2, "enable, then the journal; the controller is untouched")
	assert.Equal(t, "journalctl", m.Calls[1].Args[2])
}

func TestEnableSlurm_UnmanagedDBSkipped(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "db", Role: config.RoleDB},
	}

	err := enableSlurm(t.Context(), client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond, nil)

	require.NoError(t, err)
	require.NotEmpty(t, m.Calls)
	assert.Equal(t, []string{"exec", "sind-dev-controller", "systemctl", "enable", "--now", "slurmctld"}, m.Calls[0].Args,
		"slurmctld starts without waiting for the unmanaged db node")
	for _, call := range m.Calls {
		assert.NotEqual(t, "sind-dev-db", call.Args[1], "nothing runs on the unmanaged db node")
	}
}

func TestStartWatcher_Success(t *testing.T) {
	pipes := &mock.Pipes{}
	m := &mock.Executor{OnStart: pipes.OnStart}
	client := docker.NewClient(m)

	w, stop := startWatcher(t.Context(), client, "sind-dev-", "dev")
	assert.NotNil(t, w)
	// Close pipe writers to unblock scanner reads before stopping.
	pipes.CloseAll()
	stop()
}

func TestStartWatcher_Fallback(t *testing.T) {
	// When docker events fails to start, startWatcher returns nil
	// and callers fall back to poll-only mode.
	m := &mock.Executor{
		OnStart: func(_ []string) mock.StreamResult {
			return mock.StreamResult{Err: errors.New("docker not available")}
		},
	}
	client := docker.NewClient(m)

	w, stop := startWatcher(t.Context(), client, "sind-dev-", "dev")
	defer stop()
	assert.Nil(t, w)
}

func TestWaitReady_NilWatcher(t *testing.T) {
	// waitReady with nil watcher should fall back to UntilReady.
	containerName := ContainerName(mesh.DefaultRealm, "dev", "controller")
	var m mock.Executor
	m.AddResult(inspectJSON(t, string(containerName), "running", nil), "", nil)
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	probes := []probe.Probe{{Name: "container", Check: probe.ContainerRunning}}
	err := waitReady(ctx, client, containerName, probes, time.Millisecond, nil)
	require.NoError(t, err)
}

func TestWaitReady_WithWatcher(t *testing.T) {
	// waitReady with a watcher should use UntilReadyWithEvents.
	containerName := ContainerName(mesh.DefaultRealm, "dev", "controller")

	pipes := &mock.Pipes{}
	m := &mock.Executor{OnStart: pipes.OnStart}
	m.AddResult(inspectJSON(t, string(containerName), "running", nil), "", nil)
	client := docker.NewClient(m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	w := monitor.NewWatcher(m, "sind-dev-", "dev")
	require.NoError(t, w.Start(ctx, nil))

	probes := []probe.Probe{{Name: "container", Check: probe.ContainerRunning}}
	err := waitReady(ctx, client, containerName, probes, time.Millisecond, w)
	require.NoError(t, err)

	cancel()
	pipes.CloseAll()
	w.Wait()
}

func TestEnableService(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := enableService(t.Context(), c, "sind-dev-worker-0", "worker-0", probe.ServiceSlurmd)

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "systemctl", "enable", "--now", "slurmd"}, m.Calls[0].Args)
}

func TestEnableService_FailureCarriesJournal(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Job for slurmd.service failed", fmt.Errorf("exit status 1"))
	m.AddResult("slurmd: error: memory cgroup controller is not available.\n", "", nil)
	c := docker.NewClient(&m)

	err := enableService(t.Context(), c, "sind-dev-worker-0", "worker-0", probe.ServiceSlurmd)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabling slurmd on worker-0: ")
	assert.Contains(t, err.Error(), "\nslurmd journal:\nslurmd: error: memory cgroup controller is not available.")
	require.Len(t, m.Calls, 2)
	assert.Equal(t,
		[]string{"exec", "sind-dev-worker-0", "journalctl", "-u", "slurmd", "-n", "20", "--no-pager", "-o", "cat"},
		m.Calls[1].Args)
}
