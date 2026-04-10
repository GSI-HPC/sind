// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

		// Cleanup: ListContainers for deleteClusterResources
		if args[0] == "ps" {
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
				case cmd == "mkdir":
					return mock.Result{}
				case cmd == "ssh-keyscan":
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
		t.Errorf("unexpected docker call: %v", args)
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

func TestCreate_FullCluster(t *testing.T) {
	exitErr := notFoundErr(t)

	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, nil)

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

func TestCreate_PreflightFails(t *testing.T) {
	// Network already exists → preflight returns conflict error.
	var m mock.Executor
	m.AddResult("", "", nil) // network exists (no error = exists)
	exitErr := notFoundErr(t)
	for i := 0; i < 5; i++ {
		m.AddResult("", "Error: No such object\n", exitErr)
	}
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cluster, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
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
	// Verify cleanup ran: look for "docker ps" call from deleteClusterResources.
	var psCalls int
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			psCalls++
		}
	}
	assert.Equal(t, 2, psCalls, "cleanup should call ListContainers for diagnostics and deletion")
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
	// Verify mesh cleanup ran: CleanupMesh calls ContainerExists (container inspect)
	// for sind-ssh and sind-dns after the cluster cleanup's "docker ps" call.
	var containerInspectAfterPs int
	seenPs := false
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			seenPs = true
		}
		if seenPs && len(call.Args) >= 2 && call.Args[0] == "container" && call.Args[1] == "inspect" {
			containerInspectAfterPs++
		}
	}
	assert.GreaterOrEqual(t, containerInspectAfterPs, 1, "mesh cleanup should check mesh containers")
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
	// After the "docker ps" cleanup call, there should be NO container inspect
	// calls for mesh cleanup.
	seenPs := false
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			seenPs = true
			continue
		}
		if seenPs && len(call.Args) >= 2 && call.Args[0] == "container" && call.Args[1] == "inspect" {
			t.Fatal("mesh cleanup should not run when mesh was pre-existing")
		}
	}
}

func TestCreate_NoCleanupOnPreflightFailure(t *testing.T) {
	// When preflight fails (before any resources), cleanup should NOT run.
	var m mock.Executor
	m.AddResult("", "", nil) // network exists → conflict
	exitErr := notFoundErr(t)
	for i := 0; i < 5; i++ {
		m.AddResult("", "Error: No such object\n", exitErr)
	}
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	// No "docker ps" calls → cleanup did not run.
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			t.Fatal("cleanup should not run when preflight fails")
		}
	}
}

func TestCreate_NoClusterCleanupOnResolveInfraFailure(t *testing.T) {
	// When resolveInfra fails, cluster resource cleanup should NOT run
	// (no "docker ps" call), but mesh cleanup should run if freshly created.
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

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)

	require.Error(t, err)
	for _, call := range m.Calls {
		if len(call.Args) > 0 && call.Args[0] == "ps" {
			t.Fatal("cluster cleanup should not run when resolveInfra fails")
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
	// Mesh cleanup should run: ContainerExists (container inspect) for mesh containers.
	var meshInspects int
	for _, call := range m.Calls {
		if len(call.Args) >= 2 && call.Args[0] == "container" && call.Args[1] == "inspect" {
			meshInspects++
		}
	}
	assert.GreaterOrEqual(t, meshInspects, 1, "mesh cleanup should run on resolveInfra failure")
}

func TestCreate_CleanupResourcesError(t *testing.T) {
	// When Create fails and the cleanup itself fails, the error from Create
	// should still be the original failure (cleanup errors are logged, not returned).
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
}

func TestCreate_CleanupMeshError(t *testing.T) {
	// When Create fails with freshly-created mesh and mesh cleanup also fails,
	// the original error should still be returned.
	exitErr := notFoundErr(t)
	inCleanup := false
	var m mock.Executor
	m.OnCall = happyOnCall(t, exitErr, func(args []string, _ string) (mock.Result, bool) {
		// Make enableSlurm fail to trigger cleanup.
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			inCleanup = true
			return mock.Result{Err: fmt.Errorf("systemctl failed")}, true
		}
		// Make mesh cleanup fail: ContainerExists errors for mesh containers.
		if inCleanup && args[0] == "container" && args[1] == "inspect" {
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

func TestCreate_SubmitterSkipsSlurm(t *testing.T) {
	exitErr := notFoundErr(t)

	cfg := &config.Cluster{
		Name: "dev",
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
			{Role: config.RoleSubmitter, Image: "img:1", CPUs: 2, Memory: "2g", TmpSize: "1g"},
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
	assert.Equal(t, "submitter", cluster.Nodes[1].Name)
	// Only controller gets slurmctld; submitter is skipped entirely.
	assert.Equal(t, []string{"sind-dev-controller"}, slurmCmds)
}

// --- Direct tests for unexported helpers ---

func TestResolveInfra_SSHKeyError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dns" {
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
	_, _, _, err := resolveInfra(t.Context(), client, meshMgr, cfg)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading SSH public key")
}

func TestResolveInfra_SlurmVersionError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dns" {
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
	_, _, _, err := resolveInfra(t.Context(), client, meshMgr, createCfg())

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
	_, err := setupNodes(ctx, client, mesh.DefaultRealm, "dev", "ssh-key", configs, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting node controller")
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
			case args[2] == "mkdir":
				return mock.Result{Err: fmt.Errorf("permission denied")}
			}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := setupNodes(ctx, client, mesh.DefaultRealm, "dev", "ssh-key", configs, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "injecting SSH key")
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
			case args[2] == "mkdir":
				return mock.Result{}
			case args[2] == "ssh-keyscan":
				return mock.Result{Err: fmt.Errorf("keyscan failed")}
			}
		}
		return mock.Result{}
	}

	client := docker.NewClient(&m)
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := setupNodes(ctx, client, mesh.DefaultRealm, "dev", "ssh-key", configs, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collecting host key")
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
		// SignalContainer
		if args[0] == "kill" {
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
	t.Run("no privileges", func(t *testing.T) {
		configs := []RunConfig{
			{ShortName: "controller"},
			{ShortName: "worker-0"},
		}
		// Should not panic or log anything.
		logExtraPrivileges(t.Context(), configs)
	})

	t.Run("capAdd only", func(t *testing.T) {
		configs := []RunConfig{
			{ShortName: "worker-0", CapAdd: []string{"SYS_ADMIN"}},
		}
		logExtraPrivileges(t.Context(), configs)
	})

	t.Run("devices only", func(t *testing.T) {
		configs := []RunConfig{
			{ShortName: "worker-0", Devices: []string{"/dev/fuse"}},
		}
		logExtraPrivileges(t.Context(), configs)
	})

	t.Run("securityOpt only", func(t *testing.T) {
		configs := []RunConfig{
			{ShortName: "worker-0", SecurityOpt: []string{"apparmor=unconfined"}},
		}
		logExtraPrivileges(t.Context(), configs)
	})

	t.Run("all fields", func(t *testing.T) {
		configs := []RunConfig{
			{ShortName: "worker-0",
				CapAdd:      []string{"SYS_ADMIN", "NET_ADMIN"},
				Devices:     []string{"/dev/fuse"},
				SecurityOpt: []string{"apparmor=unconfined"},
			},
		}
		logExtraPrivileges(t.Context(), configs)
	})
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
	configs := []RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := enableSlurm(ctx, client, mesh.DefaultRealm, "dev", configs, 10*time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}
