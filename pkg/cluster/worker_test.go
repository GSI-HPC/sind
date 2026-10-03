// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodesConfMissing is the result of reading a sind-nodes.conf that is not
// there: cat fails inside the running controller.
func nodesConfMissing(t *testing.T) mock.Result {
	t.Helper()
	return mock.Result{Stderr: "cat: /etc/slurm/sind-nodes.conf: No such file or directory\n", Err: notFoundErr(t)}
}

// nodeInspect describes a node container for nodeInspectJSON.
type nodeInspect struct {
	Status     string // running when empty
	Image      string
	Labels     docker.Labels
	Networks   map[docker.NetworkName]string
	HostConfig docker.HostConfig
}

// nodeInspectJSON builds the docker inspect output for a node container,
// with its image ID and host configuration.
func nodeInspectJSON(t *testing.T, name string, n nodeInspect) string {
	t.Helper()
	type netInfo struct {
		IPAddress string `json:"IPAddress"`
	}
	nets := make(map[string]netInfo, len(n.Networks))
	for net, ip := range n.Networks {
		nets[string(net)] = netInfo{IPAddress: ip}
	}
	data, err := json.Marshal([]map[string]any{{
		"Id":              "id-" + name,
		"Name":            "/" + name,
		"Image":           n.Image,
		"State":           map[string]string{"Status": cmp.Or(n.Status, "running")},
		"Config":          map[string]any{"Labels": n.Labels},
		"HostConfig":      n.HostConfig,
		"NetworkSettings": map[string]any{"Networks": nets},
	}})
	require.NoError(t, err)
	return string(data)
}

// Image IDs of the controller and worker-0 in workerAddOnCall's cluster.
const (
	testControllerImageID = "sha256:c0ffee"
	testWorkerImageID     = "sha256:f00d"
)

// testControllerLabels are the labels docker inspect reports for the
// controller in workerAddOnCall's cluster.
func testControllerLabels() docker.Labels {
	return docker.Labels{"sind.cluster": "dev", "sind.role": "controller", "sind.slurm.version": "25.11.0"}
}

// testWorkerHostConfig is the host configuration of worker-0 in
// workerAddOnCall's cluster, created with cpus 2, memory 2g and tmpSize 1g.
func testWorkerHostConfig() docker.HostConfig {
	return docker.HostConfig{
		NanoCPUs:    2e9,
		Memory:      2 << 30,
		Tmpfs:       map[string]string{"/tmp": "rw,nosuid,nodev,size=1g", "/run": "exec,mode=755", "/run/lock": ""},
		SecurityOpt: []string{"writable-cgroups=true", "label=disable"},
	}
}

// withController makes the controller's docker inspect report labels.
func withController(t *testing.T, base func([]string, string) mock.Result, labels docker.Labels) func([]string, string) mock.Result {
	t.Helper()
	return func(args []string, stdin string) mock.Result {
		if len(args) > 1 && args[0] == "inspect" && args[1] == "sind-dev-controller" {
			return mock.Result{Stdout: nodeInspectJSON(t, "sind-dev-controller", nodeInspect{
				Image: testControllerImageID, Labels: labels,
				Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"},
			})}
		}
		return base(args, stdin)
	}
}

// workerContainers returns a standard set of cluster containers for worker tests.
func workerContainers(computes ...string) string {
	entries := []testutil.PsEntry{
		{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller"},
	}
	for _, c := range computes {
		entries = append(entries, testutil.PsEntry{
			ID: "c" + c, Names: "sind-dev-" + c, State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker",
		})
	}
	return testutil.NDJSON(entries...)
}

// --- ValidateWorkerAdd ---

func TestWorkerAdd_RequiresSindNodes(t *testing.T) {
	// When sind-nodes.conf is missing on the controller,
	// managed worker add must fail.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		// ListContainers: controller exists
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		// ReadFile (docker exec controller cat /etc/slurm/sind-nodes.conf) fails
		if args[0] == "exec" && len(args) >= 4 && args[2] == "cat" {
			return nodesConfMissing(t)
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
	})

	require.ErrorIs(t, err, errSindNodesConfMissing)
}

func TestValidateWorkerAdd_ControllerNotRunning(t *testing.T) {
	// A stopped controller is not a missing sind-nodes.conf: the remedy is
	// to start it, not --unmanaged.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "exited", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{ClusterName: "dev", Count: 1})

	require.Error(t, err)
	assert.Equal(t, "controller sind-dev-controller is not running (exited): sind updates sind-nodes.conf and reconfigures Slurm through it; start it with sind power on or sind power unfreeze first", err.Error())
	assert.Len(t, m.Calls, 1, "no docker exec")
}

func TestValidateWorkerAdd_ReadError(t *testing.T) {
	// Only a missing file means that the user replaced the configuration.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers()}
		}
		return mock.Result{Stderr: "Error response from daemon: OCI runtime exec failed\n", Err: notFoundErr(t)}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{ClusterName: "dev", Count: 1})

	require.Error(t, err)
	assert.NotErrorIs(t, err, errSindNodesConfMissing)
	assert.Equal(t, "reading sind-nodes.conf: exit status 1: Error response from daemon: OCI runtime exec failed", err.Error())
}

func TestWorkerAdd_RequiresSindNodes_Present(t *testing.T) {
	// When sind-nodes.conf exists, validation passes for managed workers.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		if args[0] == "exec" && len(args) >= 4 && args[2] == "cat" {
			return mock.Result{Stdout: "# Generated by sind\nNodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n"}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
	})

	require.NoError(t, err)
}

func TestWorkerAdd_AllowsUnmanaged(t *testing.T) {
	// Unmanaged worker add bypasses sind-nodes.conf check.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		// Should never reach ReadFile for unmanaged
		if args[0] == "exec" {
			return mock.Result{Err: fmt.Errorf("should not be called")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		Unmanaged:   true,
	})

	require.NoError(t, err)
}

func TestValidateWorkerAdd_UnmanagedCluster(t *testing.T) {
	// Workers of an unmanaged cluster are unmanaged without --unmanaged.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: unmanagedClusterContainers("worker-0")}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{ClusterName: "dev", Count: 1})

	require.NoError(t, err)
	assert.Len(t, m.Calls, 1, "no sind-nodes.conf read")
}

func TestWorkerAdd_ClusterNotFound(t *testing.T) {
	// If no controller container exists for the cluster, validation fails.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller not found")
}

func TestWorkerAdd_ClusterNotFound_Unmanaged(t *testing.T) {
	// Even unmanaged mode requires the cluster (controller) to exist.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		Unmanaged:   true,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller not found")
}

func TestWorkerAdd_ListContainersError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Err: fmt.Errorf("docker daemon not running")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

// TestValidateWorkerAdd_LabelFilter asserts the container list is scoped by
// both realm and cluster labels so parallel realms do not cross-match.
func TestValidateWorkerAdd_LabelFilter(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		if args[0] == "exec" && len(args) >= 4 && args[2] == "cat" {
			return mock.Result{Stdout: "# Generated by sind\n"}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := ValidateWorkerAdd(t.Context(), client, mesh.DefaultRealm, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
	})
	require.NoError(t, err)

	require.NotEmpty(t, m.Calls)
	psArgs := m.Calls[0].Args
	assert.Contains(t, psArgs, "label=sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, psArgs, "label=sind.cluster=dev")
}

// --- NextComputeIndex ---

func TestNextComputeIndex_Empty(t *testing.T) {
	// No worker containers → next index is 0.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	idx, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, 0, idx)
}

func TestNextComputeIndex_Sequential(t *testing.T) {
	// worker-0 and worker-1 exist → next index is 2.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers("worker-0", "worker-1")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	idx, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, 2, idx)
}

func TestNextComputeIndex_Gap(t *testing.T) {
	// worker-0 and worker-3 exist → next index is 4 (fills after max).
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers("worker-0", "worker-3")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	idx, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, 4, idx)
}

func TestNextComputeIndex_NonComputeIgnored(t *testing.T) {
	// Controller and submitter don't affect worker index.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				testutil.PsEntry{ID: "def", Names: "sind-dev-submitter", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=submitter"},
			)}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	idx, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, 0, idx)
}

func TestNextComputeIndex_ListError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Err: fmt.Errorf("docker daemon not running")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	_, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

// TestNextComputeIndex_LabelFilter asserts the container list is scoped by
// both realm and cluster labels so parallel realms do not cross-match.
func TestNextComputeIndex_LabelFilter(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers("worker-0")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	_, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")
	require.NoError(t, err)

	require.NotEmpty(t, m.Calls)
	psArgs := m.Calls[0].Args
	assert.Contains(t, psArgs, "label=sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, psArgs, "label=sind.cluster=dev")
}

// unmanagedClusterContainers returns the containers of an unmanaged cluster:
// every node carries sind.managed=false.
func unmanagedClusterContainers(computes ...string) string {
	entries := []testutil.PsEntry{
		{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller,sind.managed=false"},
	}
	for _, c := range computes {
		entries = append(entries, testutil.PsEntry{
			ID: "c" + c, Names: "sind-dev-" + c, State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker,sind.managed=false",
		})
	}
	return testutil.NDJSON(entries...)
}

// assertNoSlurmChanges fails when the calls read or wrote sind-nodes.conf,
// ran scontrol or enabled a Slurm daemon.
func assertNoSlurmChanges(t *testing.T, calls []mock.Call) {
	t.Helper()
	for _, call := range calls {
		args := call.Args
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "sind-nodes.conf") && !strings.Contains(joined, "sind-ssh") {
			assert.Failf(t, "sind-nodes.conf touched", "%v", args)
		}
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			assert.Failf(t, "scontrol called", "%v", args)
		}
		if args[0] == "exec" && len(args) > 3 && args[2] == "systemctl" && args[3] == "enable" {
			assert.Failf(t, "Slurm daemon enabled", "%v", args)
		}
	}
}

// --- WorkerAdd (managed) ---

// workerAddOnCall returns a mock OnCall handler for the WorkerAdd managed flow.
// The cluster has 1 controller + 1 existing worker (worker-0).
// The new worker will be worker-1.
func workerAddOnCall(t *testing.T) func([]string, string) mock.Result {
	t.Helper()

	nodesConf := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0 Default=YES MaxTime=INFINITE State=UP\n"

	return func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{}
		}
		joined := strings.Join(args, " ")

		switch {
		// ListContainers: cluster has controller + worker-0
		case args[0] == "ps":
			return mock.Result{Stdout: workerContainers("worker-0")}

		// InspectContainer: sind-dns → return mesh IP
		case args[0] == "inspect" && args[1] == "sind-dns":
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-mesh": "10.0.0.2",
			})}

		// InspectContainer: controller → its labels and image ID
		case args[0] == "inspect" && args[1] == "sind-dev-controller":
			return mock.Result{Stdout: nodeInspectJSON(t, "sind-dev-controller", nodeInspect{
				Image: testControllerImageID, Labels: testControllerLabels(),
				Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"},
			})}

		// InspectContainer: worker-0, the newest worker → its shape
		case args[0] == "inspect" && args[1] == "sind-dev-worker-0":
			return mock.Result{Stdout: nodeInspectJSON(t, args[1], nodeInspect{
				Image: testWorkerImageID, HostConfig: testWorkerHostConfig(),
				Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.2"},
			})}

		// InspectContainer: new worker node
		case args[0] == "inspect" && strings.HasPrefix(args[1], "sind-dev-worker-"):
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.3",
			})}

		// ReadFile: SSH pubkey from sind-ssh
		case args[0] == "exec" && args[1] == "sind-ssh" && len(args) > 2 && args[2] == "cat":
			return mock.Result{Stdout: "ssh-ed25519 AAAA-test-key\n"}

		// ReadFile: sind-nodes.conf (for validation and update)
		case args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "cat":
			return mock.Result{Stdout: nodesConf}

		// WriteFile: updated sind-nodes.conf
		case args[0] == "exec" && args[1] == "-i" && strings.Contains(joined, "sind-dev-controller"):
			return mock.Result{}

		// scontrol reconfigure
		case args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol":
			return mock.Result{}

		// CreateContainer
		case args[0] == "create":
			return mock.Result{Stdout: "new-cid\n"}

		// slurmctld -V in an explicit --image
		case args[0] == "run" && args[1] == "--rm":
			return mock.Result{Stdout: "slurm 25.11.0\n"}

		// ImageLabels: the image is not local
		case args[0] == "image" && args[1] == "inspect":
			return mock.Result{Stderr: "Error: No such image: " + args[2] + "\n", Err: notFoundErr(t)}

		// ConnectNetwork (mesh)
		case args[0] == "network" && args[1] == "connect":
			return mock.Result{}

		// StartContainer
		case args[0] == "start":
			return mock.Result{}

		// Exec on new worker: probes, SSH inject, host key, systemctl
		case args[0] == "exec":
			container := args[1]
			if container == "-i" {
				// ExecWithStdin (WriteFile)
				return mock.Result{}
			}
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
				case cmd == "systemctl" && len(args) > 3 && args[3] == "is-active":
					return mock.Result{Stdout: "active\n"}
				}
			}
			return mock.Result{}

		// DNS record operations (CopyFromContainer, CopyToContainer, kill)
		case args[0] == "cp":
			if len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns") {
				return mock.Result{Stdout: emptyCorefileTar()}
			}
			return mock.Result{}

		case args[0] == "kill":
			return mock.Result{}

		// RemoveContainer (helper cleanup or worker cleanup)
		case args[0] == "rm":
			return mock.Result{}
		}

		t.Logf("unhandled worker mock call: %v", args)
		return mock.Result{}
	}
}

// createArgs returns the docker create arguments of a node container, and
// whether it was created.
func createArgs(calls []mock.Call, name string) ([]string, bool) {
	for _, c := range calls {
		if c.Args[0] == "create" && slices.Contains(c.Args, name) {
			return c.Args, true
		}
	}
	return nil, false
}

// nodesConfWrites returns what was written to sind-nodes.conf, in order.
func nodesConfWrites(calls []mock.Call) []string {
	var writes []string
	for _, c := range calls {
		if c.Args[0] == "exec" && c.Args[1] == "-i" && c.Args[2] == "sind-dev-controller" &&
			strings.Contains(strings.Join(c.Args, " "), "sind-nodes.conf") {
			writes = append(writes, c.Stdin)
		}
	}
	return writes
}

// countCalls counts the calls whose arguments start with prefix.
func countCalls(calls []mock.Call, prefix ...string) int {
	n := 0
	for _, c := range calls {
		if len(c.Args) >= len(prefix) && slices.Equal(c.Args[:len(prefix)], prefix) {
			n++
		}
	}
	return n
}

func TestWorkerAdd_Managed(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        4,
		Memory:      "8g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, config.RoleWorker, nodes[0].Role)
	assert.Equal(t, StateRunning, nodes[0].State)

	// Verify key docker calls were made.
	var createArgs, scontrolCalled, slurmdEnabled bool
	var writeStdin string
	for _, call := range m.Calls {
		args := call.Args
		joined := strings.Join(args, " ")
		// Container created with correct name
		if args[0] == "create" && strings.Contains(joined, "sind-dev-worker-1") {
			createArgs = true
		}
		// sind-nodes.conf updated (WriteFile via exec -i on controller)
		if args[0] == "exec" && args[1] == "-i" && strings.Contains(joined, "sind-dev-controller") {
			writeStdin = call.Stdin
		}
		// scontrol reconfigure called
		if args[0] == "exec" && len(args) > 2 && args[1] == "sind-dev-controller" && args[2] == "scontrol" {
			scontrolCalled = true
		}
		// slurmd enabled on new node
		if args[0] == "exec" && len(args) > 3 && strings.Contains(args[1], "worker-1") && args[2] == "systemctl" && args[3] == "enable" {
			slurmdEnabled = true
		}
	}

	assert.True(t, createArgs, "worker-1 container should be created")
	assert.True(t, scontrolCalled, "scontrol reconfigure should be called")
	assert.True(t, slurmdEnabled, "slurmd should be enabled on new node")
	assert.Contains(t, writeStdin, "worker-1", "sind-nodes.conf should include worker-1")
}

func TestWorkerAdd_UsesNewestWorkerImageID(t *testing.T) {
	// Without --image, new workers run the image the newest worker runs,
	// by ID: its tag may have moved since.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	args, ok := createArgs(m.Calls, "sind-dev-worker-1")
	require.True(t, ok)
	assert.Contains(t, args, testWorkerImageID)
	assert.NotContains(t, args, "img:1", "not the reference docker ps reports")
	assert.NotContains(t, args, "--pull")
	assert.Zero(t, countCalls(m.Calls, "run", "--rm"), "no version check for the cluster's own image")
}

func TestWorkerAdd_InheritsShape(t *testing.T) {
	// New workers get the newest worker's resources and privileges, but
	// not the capability and security options sind adds by itself.
	hostConfig := testWorkerHostConfig()
	hostConfig.CapAdd = []string{"CAP_SYS_ADMIN", "CAP_SYS_NICE"}
	hostConfig.CapDrop = []string{"CAP_NET_RAW"}
	hostConfig.Devices = []docker.DeviceMapping{
		{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"},
		{PathOnHost: "/dev/sda", PathInContainer: "/dev/xvdc", CgroupPermissions: "r"},
	}
	hostConfig.SecurityOpt = append(hostConfig.SecurityOpt, "apparmor=unconfined")
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		switch {
		case args[0] == "ps":
			return mock.Result{Stdout: workerContainers("worker-0", "worker-3")}
		case args[0] == "inspect" && args[1] == "sind-dev-worker-3":
			return mock.Result{Stdout: nodeInspectJSON(t, args[1], nodeInspect{Image: "sha256:beef", HostConfig: hostConfig})}
		case args[0] == "inspect" && args[1] == "sind-dev-worker-0":
			assert.Fail(t, "worker-0 is not the newest worker")
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	args, ok := createArgs(m.Calls, "sind-dev-worker-4")
	require.True(t, ok)
	assert.Contains(t, args, "sha256:beef")
	assert.Equal(t, []string{"2"}, testutil.ArgValues(args, "--cpus"))
	assert.Equal(t, []string{"2g"}, testutil.ArgValues(args, "--memory"))
	assert.Contains(t, testutil.ArgValues(args, "--tmpfs"), "/tmp:rw,nosuid,nodev,size=1g")
	assert.Equal(t, []string{"SYS_ADMIN"}, testutil.ArgValues(args, "--cap-add"))
	assert.Equal(t, []string{"NET_RAW"}, testutil.ArgValues(args, "--cap-drop"))
	assert.Equal(t, []string{"/dev/fuse", "/dev/sda:/dev/xvdc:r"}, testutil.ArgValues(args, "--device"))
	assert.Equal(t, []string{"writable-cgroups=true", "label=disable", "apparmor=unconfined"}, testutil.ArgValues(args, "--security-opt"))
	writes := nodesConfWrites(m.Calls)
	require.Len(t, writes, 1)
	assert.Contains(t, writes[0], "NodeName=worker-4 CPUs=2 RealMemory=2048 State=UNKNOWN")
}

func TestWorkerAdd_OptionsOverrideShape(t *testing.T) {
	// A flag replaces the inherited setting, a list as a whole.
	hostConfig := testWorkerHostConfig()
	hostConfig.CapAdd = []string{"CAP_SYS_ADMIN"}
	hostConfig.Devices = []docker.DeviceMapping{{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"}}
	hostConfig.SecurityOpt = append(hostConfig.SecurityOpt, "apparmor=unconfined")
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Stdout: nodeInspectJSON(t, args[1], nodeInspect{Image: testWorkerImageID, HostConfig: hostConfig})}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, Image: "custom:v1", CPUs: 4, Memory: "8g", TmpSize: "2g",
		CapAdd: []string{"NET_ADMIN"}, CapDrop: []string{"MKNOD"}, Devices: []string{"/dev/kvm"}, SecurityOpt: []string{"no-new-privileges"},
	}, time.Millisecond)
	require.NoError(t, err)

	args, ok := createArgs(m.Calls, "sind-dev-worker-1")
	require.True(t, ok)
	assert.Contains(t, args, "custom:v1")
	assert.NotContains(t, args, testWorkerImageID)
	assert.Equal(t, []string{"4"}, testutil.ArgValues(args, "--cpus"))
	assert.Equal(t, []string{"8g"}, testutil.ArgValues(args, "--memory"))
	assert.Contains(t, testutil.ArgValues(args, "--tmpfs"), "/tmp:rw,nosuid,nodev,size=2g")
	assert.Equal(t, []string{"NET_ADMIN"}, testutil.ArgValues(args, "--cap-add"))
	assert.Equal(t, []string{"MKNOD"}, testutil.ArgValues(args, "--cap-drop"))
	assert.Equal(t, []string{"/dev/kvm"}, testutil.ArgValues(args, "--device"))
	assert.Equal(t, []string{"writable-cgroups=true", "label=disable", "no-new-privileges"}, testutil.ArgValues(args, "--security-opt"))
}

func TestWorkerAdd_NoWorkerUsesControllerImageAndDefaults(t *testing.T) {
	// The first worker of a cluster without one gets the controller's
	// image, by ID, and the built-in resources.
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers()}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	args, ok := createArgs(m.Calls, "sind-dev-worker-0")
	require.True(t, ok)
	assert.Contains(t, args, testControllerImageID)
	assert.Equal(t, []string{"1"}, testutil.ArgValues(args, "--cpus"))
	assert.Equal(t, []string{"512m"}, testutil.ArgValues(args, "--memory"))
	assert.Contains(t, testutil.ArgValues(args, "--tmpfs"), "/tmp:rw,nosuid,nodev,size=256m")
	assert.Empty(t, testutil.ArgValues(args, "--cap-add"))
	assert.Empty(t, testutil.ArgValues(args, "--device"))
}

func TestWorkerAdd_WorkerInspectError(t *testing.T) {
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Err: fmt.Errorf("daemon gone")}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, "inspecting worker sind-dev-worker-0: daemon gone", err.Error())
	assert.Zero(t, countCalls(m.Calls, "create"))
}

func TestWorkerAdd_InheritsDataMount(t *testing.T) {
	// New workers mount the data where the cluster's nodes do. The paths
	// come from docker inspect: docker ps joins the labels with commas,
	// which cuts a path with a comma short.
	labels := testControllerLabels()
	labels[LabelDataHostPath] = "/srv/run,2024"
	labels[LabelDataMountPath] = "/shared"
	base := withController(t, workerAddOnCall(t), labels)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) > 0 && args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(testutil.PsEntry{
				ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
				Labels: "sind.cluster=dev,sind.data.hostpath=/srv/run,2024,sind.data.mountpath=/shared,sind.role=controller",
			})}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	var created bool
	for _, call := range m.Calls {
		if call.Args[0] == "create" {
			created = true
			assert.Contains(t, testutil.ArgValues(call.Args, "--mount"), `type=bind,"source=/srv/run,2024",target=/shared`)
			assert.Contains(t, testutil.ArgValues(call.Args, "--label"), LabelDataMountPath+"=/shared")
		}
	}
	assert.True(t, created)
}

func TestWorkerAdd_RelativeDataHostPath(t *testing.T) {
	// A relative host path, such as one an image label slipped in, would
	// name a Docker volume, maybe another cluster's.
	labels := testControllerLabels()
	labels[LabelDataHostPath] = "sind-other-munge"
	var m mock.Executor
	m.OnCall = withController(t, workerAddOnCall(t), labels)
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, `controller label sind.data.hostpath="sind-other-munge" is not an absolute path: refusing to bind-mount it into new workers`, err.Error())
	assert.Zero(t, countCalls(m.Calls, "create"))
}

func TestWorkerAdd_InheritsCVMFS(t *testing.T) {
	// New workers mount CVMFS the way the cluster's nodes do, without
	// detecting the backend again.
	labels := testControllerLabels()
	labels[LabelCVMFS] = "volume"
	base := withController(t, workerAddOnCall(t), labels)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) > 0 && args[0] == "plugin" {
			assert.Fail(t, "unexpected cvmfs detection", "%v", args)
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	var created bool
	for _, call := range m.Calls {
		if call.Args[0] == "create" {
			created = true
			assert.Equal(t, []string{"type=volume,volume-driver=cvmfs,source=cvmfs,target=/cvmfs,readonly"}, testutil.ArgValues(call.Args, "--mount"))
			assert.Contains(t, testutil.ArgValues(call.Args, "--label"), LabelCVMFS+"=volume")
		}
	}
	assert.True(t, created)
}

func TestWorkerAdd_ChecksCapabilitiesAndDevices(t *testing.T) {
	tests := []struct {
		name    string
		opts    WorkerAddOptions
		wantErr string
	}{
		{"cap-add", WorkerAddOptions{CapAdd: []string{"SYS_ADMIN", "NOT_A_CAP"}}, `unknown capability "NOT_A_CAP" in --cap-add`},
		{"cap-drop", WorkerAddOptions{CapDrop: []string{"net_raw"}}, `unknown capability "net_raw" in --cap-drop`},
		{"device", WorkerAddOptions{Devices: []string{"dev/fuse"}}, `device path must be absolute, got "dev/fuse"`},
		{"security-opt", WorkerAddOptions{SecurityOpt: []string{"privileged"}}, `unknown security option "privileged" in --security-opt`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			client := docker.NewClient(&m)
			tt.opts.ClusterName = "dev"

			_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), tt.opts, time.Millisecond)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, m.Calls, "docker must not be called")
		})
	}
}

func TestWorkerAdd_Managed_ControllerNotFound(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller not found")
}

// --- WorkerAdd (unmanaged) ---

func TestWorkerAdd_Unmanaged(t *testing.T) {
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
		Unmanaged:   true,
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, config.RoleWorker, nodes[0].Role)
	assert.Equal(t, StateRunning, nodes[0].State)

	// Verify slurm config was NOT updated and slurmd was NOT enabled.
	for _, call := range m.Calls {
		args := call.Args
		joined := strings.Join(args, " ")
		// Should not read/write sind-nodes.conf on controller
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "cat" &&
			strings.Contains(joined, "sind-nodes.conf") {
			assert.Fail(t, "should not read sind-nodes.conf for unmanaged worker")
		}
		// Should not call scontrol reconfigure
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol" {
			assert.Fail(t, "should not call scontrol reconfigure for unmanaged worker")
		}
		// Should not enable slurmd on new node
		if args[0] == "exec" && len(args) > 3 && strings.Contains(args[1], "worker-1") &&
			args[2] == "systemctl" && args[3] == "enable" {
			assert.Fail(t, "should not enable slurmd for unmanaged worker")
		}
	}
}

func TestWorkerAdd_UnmanagedCluster(t *testing.T) {
	var m mock.Executor
	labels := testControllerLabels()
	labels[LabelManaged] = "false"
	labels[LabelSlurmVersion] = ""
	base := withController(t, workerAddOnCall(t), labels)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: unmanagedClusterContainers("worker-0")}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// No Unmanaged option: the cluster makes the worker unmanaged.
	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "worker-1", nodes[0].Name)
	assertNoSlurmChanges(t, m.Calls)
	for _, call := range m.Calls {
		if call.Args[0] == "create" {
			assert.Contains(t, testutil.ArgValues(call.Args, "--label"), LabelManaged+"=false")
		}
	}
}

// --- WorkerRemove (managed) ---

// workerRemoveOnCall returns a mock OnCall handler for WorkerRemove tests.
// The cluster has controller + worker-0 + worker-1, removing worker-1.
func workerRemoveOnCall(t *testing.T, nodesConf string) func([]string, string) mock.Result {
	t.Helper()

	return func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{}
		}
		joined := strings.Join(args, " ")

		switch {
		// ListContainers
		case args[0] == "ps":
			return mock.Result{Stdout: workerContainers("worker-0", "worker-1")}

		// ReadFile: sind-nodes.conf
		case args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "cat":
			if nodesConf == "" {
				return nodesConfMissing(t)
			}
			return mock.Result{Stdout: nodesConf}

		// WriteFile: updated sind-nodes.conf
		case args[0] == "exec" && args[1] == "-i" && strings.Contains(joined, "sind-dev-controller"):
			return mock.Result{}

		// scontrol reconfigure
		case args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol":
			return mock.Result{}

		// DNS: CopyFromContainer (read Corefile)
		case args[0] == "cp" && len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: emptyCorefileTar()}

		// DNS: CopyToContainer / signal
		case args[0] == "cp":
			return mock.Result{}
		// DNS: InspectContainer (state check before reload)
		case args[0] == "inspect" && len(args) >= 2 && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: dnsRunningInspectJSON}
		case args[0] == "kill":
			return mock.Result{}

		// Known hosts: exec on sind-ssh
		case args[0] == "exec" && args[1] == "sind-ssh":
			return mock.Result{Stdout: "worker-1.dev.sind.sind ssh-ed25519 AAAA\n"}
		case args[0] == "exec" && args[1] == "-i":
			return mock.Result{}

		// Start (DNS reload after kill)
		case args[0] == "start":
			return mock.Result{}

		// RemoveContainer (rm -f)
		case args[0] == "rm":
			return mock.Result{}
		}

		t.Logf("unhandled worker remove mock call: %v", args)
		return mock.Result{}
	}
}

func TestWorkerRemove_Managed(t *testing.T) {
	nodesConf := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"

	var m mock.Executor
	m.OnCall = workerRemoveOnCall(t, nodesConf)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-1"})

	require.NoError(t, err)

	// Verify key operations.
	var scontrolCalled, containerRemoved bool
	var writeStdin string
	for _, call := range m.Calls {
		args := call.Args
		joined := strings.Join(args, " ")
		if args[0] == "exec" && args[1] == "-i" && strings.Contains(joined, "sind-dev-controller") {
			writeStdin = call.Stdin
		}
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol" {
			scontrolCalled = true
		}
		if args[0] == "rm" && strings.Contains(joined, "worker-1") {
			containerRemoved = true
		}
	}

	assert.True(t, scontrolCalled, "scontrol reconfigure should be called")
	assert.True(t, containerRemoved, "container should be removed")
	assert.Contains(t, writeStdin, "worker-0", "updated conf should still have worker-0")
	assert.NotContains(t, writeStdin, "worker-1", "updated conf should not have worker-1")
}

func TestWorkerRemove_NodeNotFound(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers("worker-0")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-99"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- WorkerRemove (unmanaged) ---

func TestWorkerRemove_Unmanaged(t *testing.T) {
	// sind-nodes.conf missing → treat as unmanaged (no slurm config update).
	var m mock.Executor
	m.OnCall = workerRemoveOnCall(t, "") // empty nodesConf → ReadFile fails
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-1"})

	require.NoError(t, err)

	// Verify no scontrol reconfigure was called.
	for _, call := range m.Calls {
		args := call.Args
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol" {
			assert.Fail(t, "should not call scontrol reconfigure for unmanaged removal")
		}
	}

	// Verify container was removed (rm -f handles kill internally).
	var removed bool
	for _, call := range m.Calls {
		joined := strings.Join(call.Args, " ")
		if call.Args[0] == "rm" && strings.Contains(joined, "worker-1") {
			removed = true
		}
	}
	assert.True(t, removed, "container should be removed")
}

func TestWorkerRemove_ControllerNotRunning(t *testing.T) {
	// Without a running controller sind cannot take managed workers out of
	// sind-nodes.conf, so it removes nothing rather than leave Slurm nodes
	// without containers.
	for _, state := range []string{"exited", "paused"} {
		t.Run(state, func(t *testing.T) {
			var m mock.Executor
			base := workerRemoveOnCall(t, "NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n")
			m.OnCall = func(args []string, stdin string) mock.Result {
				if args[0] == "ps" {
					return mock.Result{Stdout: testutil.NDJSON(
						testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: state, Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=controller"},
						testutil.PsEntry{ID: "c1", Names: "sind-dev-worker-1", State: "running", Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=worker"},
					)}
				}
				return base(args, stdin)
			}
			client := docker.NewClient(&m)

			err := WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "controller sind-dev-controller is not running ("+state+")")
			assert.Len(t, m.Calls, 1, "nothing but the listing")
		})
	}
}

func TestWorkerRemove_UnmanagedWorkerWithStoppedController(t *testing.T) {
	// Unmanaged workers are not in sind-nodes.conf: removing them needs no
	// controller.
	var m mock.Executor
	base := workerRemoveOnCall(t, "")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "exited", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				testutil.PsEntry{ID: "c1", Names: "sind-dev-worker-1", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker,sind.managed=false"},
			)}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	err := WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"})

	require.NoError(t, err)
	assertNoSlurmChanges(t, m.Calls)
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"))
}

func TestWorkerRemove_ReadError(t *testing.T) {
	// Only a missing file means that the user replaced the configuration;
	// another read failure stops the removal.
	var m mock.Executor
	base := workerRemoveOnCall(t, "")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "cat" {
			return mock.Result{Err: fmt.Errorf("daemon gone")}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	err := WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"})

	require.Error(t, err)
	assert.Equal(t, "reading sind-nodes.conf: daemon gone", err.Error())
	assert.Zero(t, countCalls(m.Calls, "rm"))
}

func TestWorkerRemove_NodeNotInConf(t *testing.T) {
	// A managed worker missing from sind-nodes.conf changes nothing there,
	// and needs no reconfigure, which would fail with slurmctld down.
	var m mock.Executor
	m.OnCall = workerRemoveOnCall(t, "# Generated by sind\n"+
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n"+
		"PartitionName=all Nodes=worker-0 Default=YES MaxTime=INFINITE State=UP\n")
	client := docker.NewClient(&m)

	err := WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"})

	require.NoError(t, err)
	assert.Empty(t, nodesConfWrites(m.Calls))
	assert.Zero(t, countCalls(m.Calls, "exec", "sind-dev-controller", "scontrol"))
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"))
}

func TestWorkerRemove_ReconfigureError(t *testing.T) {
	// A failed reconfigure keeps the containers; mesh deregistration has
	// run to the end alongside it.
	nodesConf := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"
	var m mock.Executor
	base := workerRemoveOnCall(t, nodesConf)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "scontrol" {
			return mock.Result{Err: fmt.Errorf("slurmctld down")}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	err := WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"})

	require.Error(t, err)
	assert.Equal(t, "reconfiguring slurmctld: slurmctld down", err.Error())
	assert.Zero(t, countCalls(m.Calls, "rm"))
	assert.Equal(t, 1, countCalls(m.Calls, "start", "sind-dns"), "CoreDNS restarted")
}

func TestWorkerRemove_UnmanagedCluster(t *testing.T) {
	// The user's configuration of an unmanaged cluster is never edited, even
	// if it has a sind-nodes.conf.
	var m mock.Executor
	base := workerRemoveOnCall(t, "NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: unmanagedClusterContainers("worker-0", "worker-1")}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-1"})

	require.NoError(t, err)
	assertNoSlurmChanges(t, m.Calls)
	assert.True(t, slices.ContainsFunc(m.Calls, func(c mock.Call) bool {
		return c.Args[0] == "rm" && slices.Contains(c.Args, "sind-dev-worker-1")
	}), "container removed")
}

// --- Additional test combinatorics ---

func TestWorkerAdd_MultipleNodes(t *testing.T) {
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       2,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, "worker-2", nodes[1].Name)

	// Verify both nodes appear in sind-nodes.conf update.
	for _, call := range m.Calls {
		if call.Args[0] == "exec" && call.Args[1] == "-i" &&
			strings.Contains(strings.Join(call.Args, " "), "sind-dev-controller") {
			assert.Contains(t, call.Stdin, "worker-1")
			assert.Contains(t, call.Stdin, "worker-2")
			break
		}
	}
}

func TestWorkerAdd_ExplicitImage(t *testing.T) {
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		Image:       "custom:v1",
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.NoError(t, err)

	for _, call := range m.Calls {
		if call.Args[0] == "create" {
			assert.Contains(t, call.Args, "custom:v1", "should use explicit image")
			assert.NotContains(t, call.Args, "img:1", "should not use controller image")
			break
		}
	}
	assert.Equal(t, 1, countCalls(m.Calls, "run", "--rm", "custom:v1", "slurmctld", "-V"), "Slurm version checked")
}

func TestWorkerAdd_ExplicitImagePull(t *testing.T) {
	// --pull pulls the image for the version check; creating the workers
	// then uses the pulled image.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, Image: "custom:v2", Pull: true,
	}, time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, 1, countCalls(m.Calls, "run", "--rm", "--pull", "always", "custom:v2", "slurmctld", "-V"))
	args, ok := createArgs(m.Calls, "sind-dev-worker-1")
	require.True(t, ok)
	assert.NotContains(t, args, "--pull")
}

func TestWorkerAdd_ExplicitImageVersionMismatch(t *testing.T) {
	// slurmd must not be newer than slurmctld, and sind labels every node
	// with the cluster's version.
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "run" {
			return mock.Result{Stdout: "slurm 26.05.4\n"}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, Image: "ghcr.io/gsi-hpc/sind-node:latest", Pull: true,
	}, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, `image ghcr.io/gsi-hpc/sind-node:latest has Slurm 26.05.4, but cluster "dev" runs Slurm 25.11.0: managed workers need the cluster's version`, err.Error())
	assert.Zero(t, countCalls(m.Calls, "create"))
}

func TestWorkerAdd_ExplicitImageVersionError(t *testing.T) {
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "run" {
			return mock.Result{Err: fmt.Errorf("pull access denied")}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, Image: "custom:v1",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, "discovering the Slurm version of custom:v1: running slurmctld -V: pull access denied", err.Error())
	assert.Zero(t, countCalls(m.Calls, "create"))
}

func TestWorkerAdd_ExplicitImageUnmanaged(t *testing.T) {
	// Unmanaged workers run no slurmd of sind's: their image is not
	// checked, and docker pulls it when it creates them.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, Image: "plain:1", Pull: true, Unmanaged: true,
	}, time.Millisecond)
	require.NoError(t, err)

	assert.Zero(t, countCalls(m.Calls, "run"))
	args, ok := createArgs(m.Calls, "sind-dev-worker-1")
	require.True(t, ok)
	assert.Equal(t, []string{"always"}, testutil.ArgValues(args, "--pull"))
}

func TestWorkerAddOptions_Check(t *testing.T) {
	tests := []struct {
		name    string
		opts    WorkerAddOptions
		wantErr string
	}{
		{"valid", WorkerAddOptions{Image: "img:1", Pull: true, CPUs: 2, CapAdd: []string{"SYS_ADMIN"}, Devices: []string{"/dev/fuse"}}, ""},
		{"negative cpus", WorkerAddOptions{CPUs: -1}, "--cpus must not be negative, got -1"},
		{"pull without image", WorkerAddOptions{Pull: true}, "--pull needs --image: without one, new workers run the image of the cluster's newest worker, by ID"},
		{"cap-add", WorkerAddOptions{CapAdd: []string{"NOT_A_CAP"}}, `unknown capability "NOT_A_CAP" in --cap-add`},
		{"cap-drop", WorkerAddOptions{CapDrop: []string{"net_raw"}}, `unknown capability "net_raw" in --cap-drop`},
		{"device", WorkerAddOptions{Devices: []string{"dev/fuse"}}, `device path must be absolute, got "dev/fuse"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Check()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestWorkerAdd_InfraError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dns" {
			return mock.Result{Err: fmt.Errorf("DNS container not running")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container")
}

func TestWorkerAdd_SindNodesConfMissing(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		// Make ReadFile for sind-nodes.conf fail
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "cat" {
			return nodesConfMissing(t)
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.ErrorIs(t, err, errSindNodesConfMissing)
	assert.Zero(t, countCalls(m.Calls, "create"))
}

func TestWorkerAdd_ControllerNotRunning(t *testing.T) {
	// Managed workers need the controller to update sind-nodes.conf; a
	// stopped one is not reported as a missing file.
	for _, state := range []string{"exited", "paused"} {
		t.Run(state, func(t *testing.T) {
			base := workerAddOnCall(t)
			var m mock.Executor
			m.OnCall = func(args []string, stdin string) mock.Result {
				if args[0] == "ps" {
					return mock.Result{Stdout: testutil.NDJSON(
						testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: state, Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=controller"},
						testutil.PsEntry{ID: "c0", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
							Labels: "sind.cluster=dev,sind.role=worker"},
					)}
				}
				return base(args, stdin)
			}
			client := docker.NewClient(&m)

			_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "controller sind-dev-controller is not running ("+state+")")
			assert.Zero(t, countCalls(m.Calls, "exec", "sind-dev-controller"))
			assert.Zero(t, countCalls(m.Calls, "create"))
		})
	}
}

func TestWorkerAdd_UnmanagedWithStoppedController(t *testing.T) {
	// Unmanaged workers leave Slurm alone and do not need the controller
	// to run.
	base := workerAddOnCall(t)
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "exited", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
			)}
		}
		return base(args, stdin)
	}
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1, Unmanaged: true}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assertNoSlurmChanges(t, m.Calls)
}

func TestWorkerAdd_InvalidResources(t *testing.T) {
	// Invalid --memory and --tmp-size values fail before any docker call,
	// as in the config.
	for _, tt := range []struct {
		memory, tmpSize, wantErr string
	}{
		{"bogus", "1g", `invalid --memory "bogus"`},
		{"1.5g", "1.5g", `invalid --tmp-size "1.5g"`},
	} {
		var m mock.Executor
		client := docker.NewClient(&m)
		mgr := mesh.NewManager(client, mesh.DefaultRealm)

		_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
			ClusterName: "dev",
			Count:       1,
			CPUs:        2,
			Memory:      tt.memory,
			TmpSize:     tt.tmpSize,
		}, time.Millisecond)

		require.Error(t, err)
		assert.Contains(t, err.Error(), tt.wantErr)
		assert.Empty(t, m.Calls)
	}
}

func TestWorkerAdd_DockerMemorySyntax(t *testing.T) {
	// Docker's size syntax becomes RealMemory in MiB.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        2,
		Memory:      "1.5GiB",
	}, time.Millisecond)
	require.NoError(t, err)

	var written string
	for _, c := range m.Calls {
		if c.Args[0] == "exec" && c.Args[1] == "-i" && strings.Contains(strings.Join(c.Args, " "), "sind-nodes.conf") {
			written = c.Stdin
		}
	}
	assert.Contains(t, written, "NodeName=worker-1 CPUs=2 RealMemory=1536 State=UNKNOWN")
}

func TestUpdateNodesConf_InvalidMemory(t *testing.T) {
	// WorkerAdd checks the memory first; updateNodesConf still refuses a
	// value it cannot convert instead of writing RealMemory=0.
	var m mock.Executor
	client := docker.NewClient(&m)

	err := updateNodesConf(t.Context(), client, "sind-dev-controller", "# Generated by sind\n", []RunConfig{{ShortName: "worker-1", CPUs: 1, Memory: "bogus"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `parsing memory "bogus" for worker-1`)
}

func TestWorkerRemove_MultipleNodes(t *testing.T) {
	nodesConf := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"

	var m mock.Executor
	m.OnCall = workerRemoveOnCall(t, nodesConf)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	// Verify both worker containers were removed (rm -f handles kill internally).
	var removeCount int
	for _, call := range m.Calls {
		if call.Args[0] == "rm" && len(call.Args) >= 2 && strings.HasPrefix(call.Args[len(call.Args)-1], "sind-dev-worker-") {
			removeCount++
		}
	}
	assert.Equal(t, 2, removeCount, "both containers should be removed")
}

func TestWorkerRemove_ListContainersError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Err: fmt.Errorf("docker daemon not running")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")

	// The list call must be scoped by both realm and cluster labels.
	require.NotEmpty(t, m.Calls)
	psArgs := m.Calls[0].Args
	assert.Contains(t, psArgs, "label=sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, psArgs, "label=sind.cluster=dev")
}

func TestWorkerAdd_DefaultCount(t *testing.T) {
	// When Count is 0 (zero value), WorkerAdd defaults to creating 1 node.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		// Count intentionally 0 → should default to 1
		CPUs:    2,
		Memory:  "2g",
		TmpSize: "1g",
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "worker-1", nodes[0].Name)
}

func TestWorkerAdd_DefaultResources(t *testing.T) {
	// When CPUs, Memory, TmpSize are zero/empty, WorkerAdd defaults to
	// config.DefaultCPUs, config.DefaultMemory, config.DefaultTmpSize.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		// CPUs, Memory, TmpSize intentionally omitted → should use defaults
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
}

func TestWorkerAdd_Unmanaged_MultipleNodes(t *testing.T) {
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       2,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
		Unmanaged:   true,
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, "worker-2", nodes[1].Name)

	// Verify no slurm operations were performed.
	for _, call := range m.Calls {
		args := call.Args
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol" {
			assert.Fail(t, "should not call scontrol reconfigure for unmanaged workers")
		}
	}
}

func TestWorkerRemove_NoController(t *testing.T) {
	// When the controller is already gone (orphaned workers), removal should
	// skip slurm config update and still delete the containers.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{}
		}
		switch {
		case args[0] == "ps":
			// Only worker nodes, no controller.
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "c0", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		case args[0] == "cp" && len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: emptyCorefileTar()}
		case args[0] == "cp":
			return mock.Result{}
		case args[0] == "inspect" && len(args) >= 2 && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: dnsRunningInspectJSON}
		case args[0] == "kill" || args[0] == "start":
			return mock.Result{}
		case args[0] == "exec" && args[1] == "sind-ssh":
			return mock.Result{Stdout: "worker-0.dev.sind.sind ssh-ed25519 AAAA\n"}
		case args[0] == "exec" && args[1] == "-i":
			return mock.Result{}
		case args[0] == "rm":
			return mock.Result{}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-0"})

	require.NoError(t, err)

	// Verify no slurm config operations were attempted.
	for _, call := range m.Calls {
		args := call.Args
		if args[0] == "exec" && len(args) > 1 && args[1] == "sind-dev-controller" {
			assert.Fail(t, "should not interact with controller when it doesn't exist")
		}
	}

	// Verify container was still removed.
	var removed bool
	for _, call := range m.Calls {
		if call.Args[0] == "rm" && strings.Contains(strings.Join(call.Args, " "), "worker-0") {
			removed = true
		}
	}
	assert.True(t, removed, "container should be removed even without controller")
}

func TestNextComputeIndex_NonNumericSuffix(t *testing.T) {
	// Containers with non-numeric suffixes (e.g. worker-abc) are ignored.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				testutil.PsEntry{ID: "c0", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
				testutil.PsEntry{ID: "cx", Names: "sind-dev-worker-abc", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	idx, err := NextComputeIndex(t.Context(), client, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestWorkerRemove_RejectsController(t *testing.T) {
	// Removing the controller via worker remove must be rejected.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: workerContainers("worker-0")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"controller"})

	require.Error(t, err)
	assert.Equal(t, `node "controller" has role "controller": only worker nodes can be removed`, err.Error())
}

func TestWorkerRemove_RejectsSubmitter(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "abc", Names: "sind-dev-controller", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=controller"},
				testutil.PsEntry{ID: "sub", Names: "sind-dev-submitter", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=submitter"},
				testutil.PsEntry{ID: "c0", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
					Labels: "sind.cluster=dev,sind.role=worker"},
			)}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"submitter"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "only worker nodes")
}

func TestWorkerRemove_DuplicateNames(t *testing.T) {
	// Duplicate names should be deduplicated — container removed only once.
	var m mock.Executor
	m.OnCall = workerRemoveOnCall(t, "")
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-1", "worker-1"})

	require.NoError(t, err)

	var removeCount int
	for _, call := range m.Calls {
		if call.Args[0] == "rm" && strings.Contains(strings.Join(call.Args, " "), "worker-1") {
			removeCount++
		}
	}
	assert.Equal(t, 1, removeCount, "container should be removed exactly once")
}

func TestWorkerRemove_EmptyNames(t *testing.T) {
	// Empty shortNames is a no-op — no docker calls should be made.
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result {
		return mock.Result{Err: fmt.Errorf("should not be called")}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls should be made for empty shortNames")
}

func TestWorkerAdd_NegativeCount(t *testing.T) {
	// Negative count defaults to 1, same as zero.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       -5,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "worker-1", nodes[0].Name)
}

// --- WorkerAdd error path tests ---

func TestWorkerAdd_ListError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Err: fmt.Errorf("docker daemon not running")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")

	// The list call must be scoped by both realm and cluster labels.
	require.NotEmpty(t, m.Calls)
	psArgs := m.Calls[0].Args
	assert.Contains(t, psArgs, "label=sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, psArgs, "label=sind.cluster=dev")
}

func TestWorkerAdd_CreateNodeError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "create" {
			return mock.Result{Err: fmt.Errorf("image not found")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
}

func TestWorkerAdd_SetupNodesError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && len(args) > 2 && args[2] == "sh" &&
			strings.Contains(strings.Join(args, " "), "is-system-running") {
			return mock.Result{Err: fmt.Errorf("container not running")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, 50*time.Millisecond)

	require.Error(t, err)
}

func TestWorkerAdd_RegisterNodesError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		// Fail on DNS CopyFromContainer (cp ... sind-dns:... -)
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns") {
			return mock.Result{Err: fmt.Errorf("DNS container crashed")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
}

func TestWorkerAdd_EnableSlurmError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && len(args) > 3 &&
			strings.Contains(args[1], "worker") && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
}

func TestWorkerAdd_CleansUpOnFailure(t *testing.T) {
	// When enableSlurm fails (after containers exist), cleanup should run.
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && len(args) > 3 &&
			strings.Contains(args[1], "worker") && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: fmt.Errorf("systemctl failed")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	// Verify cleanup ran: look for "docker rm -f" on the new worker container.
	var removed bool
	for _, call := range m.Calls {
		if call.Args[0] == "rm" && len(call.Args) >= 2 && strings.HasPrefix(call.Args[len(call.Args)-1], "sind-dev-worker-") {
			removed = true
		}
	}
	assert.True(t, removed, "cleanup should remove the new worker container")
}

func TestWorkerAdd_CleanupErrors(t *testing.T) {
	// When cleanup itself encounters errors (RemoveKnownHost, RemoveContainer),
	// those errors are logged but do not mask the original failure.
	var m mock.Executor
	inner := workerAddOnCall(t)
	inCleanup := false
	m.OnCall = func(args []string, stdin string) mock.Result {
		// Make enableSlurm fail to trigger cleanup.
		if args[0] == "exec" && len(args) > 3 &&
			strings.Contains(args[1], "worker") && args[2] == "systemctl" && args[3] == "enable" {
			inCleanup = true
			return mock.Result{Err: fmt.Errorf("systemctl failed")}
		}
		if !inCleanup {
			return inner(args, stdin)
		}
		// During cleanup: make RemoveKnownHost and RemoveContainer fail.
		if args[0] == "exec" && args[1] == "-i" {
			return mock.Result{Err: fmt.Errorf("write failed")}
		}
		if args[0] == "rm" {
			return mock.Result{Err: fmt.Errorf("container locked")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemctl failed")
}

func TestWorkerAdd_NoCleanupOnValidationFailure(t *testing.T) {
	// When validation fails (before containers), cleanup should NOT run.
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dev-controller" {
			return mock.Result{Err: fmt.Errorf("container not running")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	// No "docker rm" calls on worker containers → cleanup did not run.
	for _, call := range m.Calls {
		if call.Args[0] == "rm" && len(call.Args) >= 2 && strings.HasPrefix(call.Args[len(call.Args)-1], "sind-dev-worker-") {
			require.Fail(t, "cleanup should not run when validation fails")
		}
	}
}

func TestWorkerAdd_ControllerInspectError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-dev-controller" {
			return mock.Result{Err: fmt.Errorf("container not running")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting controller")
}

func TestWorkerAdd_ReadsNodesConfOnce(t *testing.T) {
	// sind-nodes.conf is read once, before any container exists; the update
	// builds on that content.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, 1, countCalls(m.Calls, "exec", "sind-dev-controller", "cat", "/etc/slurm/sind-nodes.conf"))
	assert.Equal(t, 1, countCalls(m.Calls, "inspect", "sind-dev-controller"))
}

func TestWorkerAdd_ReadNodesConfError(t *testing.T) {
	// A read that fails for another reason than a missing file is
	// reported as it is, before any container exists.
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "cat" {
			return mock.Result{Err: fmt.Errorf("daemon gone")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

	require.Error(t, err)
	assert.Equal(t, "reading sind-nodes.conf: daemon gone", err.Error())
	assert.Zero(t, countCalls(m.Calls, "create"))
}

// statefulNodesConf serves sind-nodes.conf from memory on top of base, so
// that each read returns the last write.
func statefulNodesConf(base func([]string, string) mock.Result, content string) func([]string, string) mock.Result {
	var mu sync.Mutex
	return func(args []string, stdin string) mock.Result {
		joined := strings.Join(args, " ")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "cat":
			return mock.Result{Stdout: content}
		case args[0] == "exec" && args[1] == "-i" && args[2] == "sind-dev-controller" && strings.Contains(joined, "sind-nodes.conf"):
			content = stdin
			return mock.Result{}
		}
		return base(args, stdin)
	}
}

func TestWorkerAdd_RollbackRemovesNodesFromConf(t *testing.T) {
	// A failure after sind-nodes.conf got the new nodes takes them out
	// again, so a retry does not define them twice.
	original := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0 Default=YES MaxTime=INFINITE State=UP\n"
	for _, tt := range []struct {
		name string
		fail func(args []string) bool
	}{
		{"slurmd fails", func(args []string) bool {
			return args[0] == "exec" && len(args) > 3 && args[1] == "sind-dev-worker-1" && args[2] == "systemctl" && args[3] == "enable"
		}},
		{"reconfigure fails", func(args []string) bool {
			return args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "scontrol"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			failed := false
			base := statefulNodesConf(workerAddOnCall(t), original)
			m.OnCall = func(args []string, stdin string) mock.Result {
				if !failed && tt.fail(args) {
					failed = true
					return mock.Result{Err: fmt.Errorf("boom")}
				}
				return base(args, stdin)
			}
			client := docker.NewClient(&m)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

			require.Error(t, err)
			writes := nodesConfWrites(m.Calls)
			require.Len(t, writes, 2, "added, then removed")
			assert.Contains(t, writes[0], "NodeName=worker-1")
			assert.Equal(t, original, writes[1])
			assert.Equal(t, 2, countCalls(m.Calls, "exec", "sind-dev-controller", "scontrol", "reconfigure"))
			assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"))
		})
	}
}

func TestWorkerAdd_RollbackNodesConfErrors(t *testing.T) {
	// A failed revert is logged; the original error is returned.
	for _, tt := range []struct {
		name string
		fail func(args []string, reads int) bool
	}{
		{"read", func(args []string, reads int) bool { return args[0] == "exec" && args[2] == "cat" && reads > 1 }},
		{"write", func(args []string, _ int) bool {
			return args[0] == "exec" && args[1] == "-i" && args[2] == "sind-dev-controller" && strings.Contains(strings.Join(args, " "), "sind-nodes.conf")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			base := workerAddOnCall(t)
			reads, slurmdFailed := 0, false
			m.OnCall = func(args []string, stdin string) mock.Result {
				if args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "cat" {
					reads++
				}
				if args[0] == "exec" && len(args) > 3 && args[1] == "sind-dev-worker-1" && args[2] == "systemctl" && args[3] == "enable" {
					slurmdFailed = true
					return mock.Result{Err: fmt.Errorf("slurmd failed")}
				}
				if slurmdFailed && tt.fail(args, reads) {
					return mock.Result{Err: fmt.Errorf("revert failed")}
				}
				return base(args, stdin)
			}
			client := docker.NewClient(&m)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "slurmd failed")
			assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"), "containers removed anyway")
		})
	}
}

func TestWorkerAdd_RetryReplacesLeftoverNode(t *testing.T) {
	// A NodeName line an older sind left behind for the name the new worker
	// gets is replaced, not defined a second time.
	leftover := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=1 RealMemory=512 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"
	var m mock.Executor
	m.OnCall = statefulNodesConf(workerAddOnCall(t), leftover)
	client := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g"}, time.Millisecond)
	require.NoError(t, err)

	writes := nodesConfWrites(m.Calls)
	require.Len(t, writes, 1)
	assert.Equal(t, "# Generated by sind\n"+
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n"+
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n"+
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n", writes[0])
}

func TestWorkerAdd_WriteNodesConfError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "-i" &&
			strings.Contains(strings.Join(args, " "), "sind-dev-controller") {
			return mock.Result{Err: fmt.Errorf("disk full")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating sind-nodes.conf")
}

func TestWorkerAdd_ScontrolReconfigureError(t *testing.T) {
	var m mock.Executor
	inner := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Err: fmt.Errorf("slurmctld not responding")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconfiguring slurmctld")
}

// --- WorkerRemove error path tests ---

func TestWorkerRemove_RemoveNodesConfError(t *testing.T) {
	nodesConf := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"

	var m mock.Executor
	inner := workerRemoveOnCall(t, nodesConf)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "-i" &&
			strings.Contains(strings.Join(args, " "), "sind-dev-controller") {
			return mock.Result{Err: fmt.Errorf("disk full")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating sind-nodes.conf")
}

// TestWorkerRemove_DeregisterMeshContinuesOnDNSFailure verifies that a DNS
// update failure during worker removal is logged and swallowed — workers
// should still be removed.
func TestWorkerRemove_DeregisterMeshContinuesOnDNSFailure(t *testing.T) {
	var m mock.Executor
	inner := workerRemoveOnCall(t, "")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "cp" && len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns") {
			return mock.Result{Err: fmt.Errorf("DNS container crashed")}
		}
		return inner(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	err := WorkerRemove(t.Context(), client, mgr, "dev", []string{"worker-1"})
	require.NoError(t, err)
}

// --- Lifecycle ---

// workerLifecycleOnCall handles both add and remove flows in sequence.
// The cluster starts with controller + worker-0. WorkerAdd creates worker-1,
// then WorkerRemove deletes worker-1.
func workerLifecycleOnCall(t *testing.T) func([]string, string) mock.Result {
	t.Helper()

	nodesConf := "# Generated by sind\n" +
		"NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0 Default=YES MaxTime=INFINITE State=UP\n"

	// Track whether worker-1 has been added so remove can include it.
	added := false

	return func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{}
		}
		joined := strings.Join(args, " ")

		switch {
		case args[0] == "ps":
			if added {
				return mock.Result{Stdout: workerContainers("worker-0", "worker-1")}
			}
			return mock.Result{Stdout: workerContainers("worker-0")}

		case args[0] == "inspect" && args[1] == "sind-dns":
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{
				"sind-mesh": "10.0.0.2",
			})}

		case args[0] == "inspect" && args[1] == "sind-dev-controller":
			return mock.Result{Stdout: inspectJSONLabels(t, "sind-dev-controller", "running",
				map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"},
				docker.Labels{"sind.slurm.version": "25.11.0"})}

		case args[0] == "inspect" && strings.HasPrefix(args[1], "sind-dev-worker-"):
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{
				"sind-dev-net": "10.0.1.3",
			})}

		case args[0] == "exec" && args[1] == "sind-ssh" && len(args) > 2 && args[2] == "cat":
			// ReadFile: SSH pubkey
			return mock.Result{Stdout: "ssh-ed25519 AAAA-test-key\n"}

		case args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "cat":
			// ReadFile: sind-nodes.conf
			return mock.Result{Stdout: nodesConf}

		case args[0] == "exec" && args[1] == "-i" && strings.Contains(joined, "sind-dev-controller"):
			// WriteFile: updated sind-nodes.conf
			return mock.Result{}

		case args[0] == "exec" && args[1] == "sind-dev-controller" && len(args) > 2 && args[2] == "scontrol":
			return mock.Result{}

		case args[0] == "create":
			added = true
			return mock.Result{Stdout: "new-cid\n"}

		case args[0] == "network" && args[1] == "connect":
			return mock.Result{}

		case args[0] == "start":
			return mock.Result{}

		case args[0] == "exec":
			container := args[1]
			if container == "-i" {
				return mock.Result{}
			}
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
				case cmd == "systemctl" && len(args) > 3 && args[3] == "is-active":
					return mock.Result{Stdout: "active\n"}
				}
			}
			return mock.Result{}

		case args[0] == "cp":
			if len(args) == 3 && args[2] == "-" && strings.Contains(args[1], "sind-dns") {
				return mock.Result{Stdout: emptyCorefileTar()}
			}
			return mock.Result{}

		case args[0] == "kill":
			return mock.Result{}

		case args[0] == "rm":
			return mock.Result{}
		}

		t.Logf("unhandled lifecycle mock call: %v", args)
		return mock.Result{}
	}
}

func TestWorkerAddRemoveLifecycle(t *testing.T) {
	var m mock.Executor
	m.OnCall = workerLifecycleOnCall(t)
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Add a managed worker.
	nodes, err := WorkerAdd(ctx, client, mgr, WorkerAddOptions{
		ClusterName: "dev",
		Count:       1,
		CPUs:        2,
		Memory:      "2g",
		TmpSize:     "1g",
	}, time.Millisecond)

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "worker-1", nodes[0].Name)
	assert.Equal(t, config.RoleWorker, nodes[0].Role)
	assert.Equal(t, StateRunning, nodes[0].State)

	// Remove the worker.
	err = WorkerRemove(ctx, client, mgr, "dev", []string{"worker-1"})
	require.NoError(t, err)

	// Verify both add and remove operations happened.
	var created, removed bool
	for _, call := range m.Calls {
		args := call.Args
		joined := strings.Join(args, " ")
		if args[0] == "create" && strings.Contains(joined, "sind-dev-worker-1") {
			created = true
		}
		if args[0] == "rm" && strings.Contains(joined, "worker-1") {
			removed = true
		}
	}
	assert.True(t, created, "worker-1 should be created during add")
	assert.True(t, removed, "worker-1 should be removed during remove")
}

// --- findController ---

func TestFindController(t *testing.T) {
	entry := func(short string, state docker.ContainerState) docker.ContainerListEntry {
		return docker.ContainerListEntry{Name: ContainerName(mesh.DefaultRealm, "dev", short), State: state}
	}
	worker := entry("worker-0", docker.StateRunning)
	tests := []struct {
		name       string
		containers []docker.ContainerListEntry
		want       string
	}{
		{name: "single controller", containers: []docker.ContainerListEntry{entry("controller", docker.StateRunning), worker}, want: "sind-dev-controller"},
		{name: "stopped single controller", containers: []docker.ContainerListEntry{entry("controller", docker.StateExited), worker}, want: "sind-dev-controller"},
		{name: "pair prefers primary", containers: []docker.ContainerListEntry{entry("controller-backup", docker.StateRunning), entry("controller", docker.StateRunning)}, want: "sind-dev-controller"},
		{name: "primary stopped after failover", containers: []docker.ContainerListEntry{entry("controller", docker.StateExited), entry("controller-backup", docker.StateRunning)}, want: "sind-dev-controller-backup"},
		{name: "primary gone", containers: []docker.ContainerListEntry{entry("controller-backup", docker.StateRunning), worker}, want: "sind-dev-controller-backup"},
		{name: "both stopped", containers: []docker.ContainerListEntry{entry("controller", docker.StateExited), entry("controller-backup", docker.StateExited)}, want: "sind-dev-controller"},
		{name: "no controller", containers: []docker.ContainerListEntry{worker}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findController(tt.containers, mesh.DefaultRealm, "dev")
			assert.Equal(t, tt.want != "", ok)
			assert.Equal(t, docker.ContainerName(tt.want), got.Name)
		})
	}
}

func TestClusterManaged(t *testing.T) {
	entry := func(short string, state docker.ContainerState, managed string) docker.ContainerListEntry {
		return docker.ContainerListEntry{
			Name:   ContainerName(mesh.DefaultRealm, "dev", short),
			State:  state,
			Labels: docker.Labels{LabelManaged: managed},
		}
	}
	tests := []struct {
		name       string
		containers []docker.ContainerListEntry
		want       bool
	}{
		{name: "managed controller", containers: []docker.ContainerListEntry{entry("controller", docker.StateRunning, "true")}, want: true},
		{name: "unmanaged controller", containers: []docker.ContainerListEntry{entry("controller", docker.StateRunning, "false")}},
		{name: "label from the backup after failover", containers: []docker.ContainerListEntry{
			entry("controller", docker.StateExited, "true"), entry("controller-backup", docker.StateRunning, "false"),
		}},
		{name: "unmanaged worker of a managed cluster", containers: []docker.ContainerListEntry{
			entry("controller", docker.StateRunning, "true"), entry("worker-0", docker.StateRunning, "false"),
		}, want: true},
		{name: "no controller", containers: []docker.ContainerListEntry{entry("worker-0", docker.StateRunning, "false")}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clusterManaged(tt.containers, mesh.DefaultRealm, "dev"))
		})
	}
}

// --- worker shape ---

func TestNewestWorker(t *testing.T) {
	entry := func(short string, managed string) docker.ContainerListEntry {
		labels := docker.Labels{}
		if managed != "" {
			labels[LabelManaged] = managed
		}
		return docker.ContainerListEntry{Name: ContainerName(mesh.DefaultRealm, "dev", short), Labels: labels}
	}
	tests := []struct {
		name       string
		containers []docker.ContainerListEntry
		managed    bool
		want       string
	}{
		{"highest index", []docker.ContainerListEntry{entry("controller", ""), entry("worker-2", ""), entry("worker-10", ""), entry("worker-9", "")}, true, "sind-dev-worker-10"},
		{"managed like the new ones", []docker.ContainerListEntry{entry("worker-0", ""), entry("worker-1", "false")}, true, "sind-dev-worker-0"},
		{"unmanaged like the new ones", []docker.ContainerListEntry{entry("worker-0", "false"), entry("worker-1", "")}, false, "sind-dev-worker-0"},
		{"any worker without a match", []docker.ContainerListEntry{entry("worker-0", ""), entry("worker-1", "")}, false, "sind-dev-worker-1"},
		{"no worker", []docker.ContainerListEntry{entry("controller", ""), entry("worker-x", "")}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := newestWorker(tt.containers, mesh.DefaultRealm, "dev", tt.managed)
			assert.Equal(t, tt.want != "", ok)
			assert.Equal(t, docker.ContainerName(tt.want), got)
		})
	}
}

func TestExistingWorkerShape_Fallback(t *testing.T) {
	// A worker docker reports no limits for keeps the fallback's.
	fallback := defaultWorkerShape("sha256:ctrl")

	shape := existingWorkerShape(t.Context(), &docker.ContainerInfo{}, fallback)

	assert.Equal(t, fallback, shape)
}

func TestMemoryArg(t *testing.T) {
	for bytes, want := range map[int64]string{
		512 << 20:        "512m",
		2 << 30:          "2g",
		1536 << 20:       "1536m",
		1 << 40:          "1024g",
		1000 << 10:       "1000k",
		(1 << 30) + 1024: "1048577k",
		(64 << 20) + 1:   "67108865b",
	} {
		assert.Equal(t, want, memoryArg(bytes), bytes)
	}
}

func TestTmpfsSize(t *testing.T) {
	assert.Equal(t, "1g", tmpfsSize("rw,nosuid,nodev,size=1g"))
	assert.Equal(t, "256m", tmpfsSize("size=256m,mode=1777"))
	assert.Empty(t, tmpfsSize("rw,nosuid"))
	assert.Empty(t, tmpfsSize(""))
}

func TestCapabilityNames(t *testing.T) {
	assert.Equal(t, []string{"SYS_ADMIN", "NET_ADMIN", "ALL"}, capabilityNames([]string{"CAP_SYS_ADMIN", "CAP_SYS_NICE", "net_admin", "ALL"}, UserJobCapability))
	assert.Equal(t, []string{"SYS_NICE"}, capabilityNames([]string{"CAP_SYS_NICE"}))
	assert.Nil(t, capabilityNames(nil))
}

func TestDeviceArgs(t *testing.T) {
	assert.Equal(t, []string{"/dev/fuse", "/dev/sda:/dev/xvdc:rwm", "/dev/kvm:/dev/kvm:r"}, deviceArgs([]docker.DeviceMapping{
		{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"},
		{PathOnHost: "/dev/sda", PathInContainer: "/dev/xvdc", CgroupPermissions: "rwm"},
		{PathOnHost: "/dev/kvm", PathInContainer: "/dev/kvm", CgroupPermissions: "r"},
	}))
	assert.Nil(t, deviceArgs(nil))
}

func TestExtraSecurityOpts(t *testing.T) {
	var logs strings.Builder
	ctx := sindlog.With(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)))

	extra := extraSecurityOpts(ctx, "sind-dev-worker-0", []string{
		"writable-cgroups=true", "label=disable", "apparmor=unconfined", `seccomp={"defaultAction":"SCMP_ACT_ALLOW"}`, "seccomp=unconfined",
	})

	assert.Equal(t, []string{"apparmor=unconfined", "seccomp=unconfined"}, extra)
	assert.Contains(t, logs.String(), "not inheriting the seccomp profile of the newest worker")
	assert.Equal(t, []string{"writable-cgroups=true", "label=disable"}, nodeSecurityOpts())
}
