// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DeleteContainers ---

func TestDeleteContainers(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result { return mock.Result{} }
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
		{Name: "sind-dev-worker-0"},
	}
	err := DeleteContainers(t.Context(), c, containers)

	require.NoError(t, err)
	require.Len(t, m.Calls, 2)
	// Order is nondeterministic (parallel removal).
	names := []string{m.Calls[0].Args[3], m.Calls[1].Args[3]}
	assert.ElementsMatch(t, []string{"sind-dev-controller", "sind-dev-worker-0"}, names)
}

func TestDeleteContainers_RemoveError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("container in use")) // rm -f fails
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeleteContainers(t.Context(), c, containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container sind-dev-controller")
}

// TestDeleteContainers_AlreadyGone covers the manual-cleanup case: the user
// `docker rm`'d a cluster container, then ran `sind delete cluster`. The
// removal must succeed with a warning instead of aborting.
func TestDeleteContainers_AlreadyGone(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container\n", testutil.ExitCode1(t))
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
		{Name: "sind-dev-worker-0"},
	}
	err := DeleteContainers(t.Context(), c, containers)
	require.NoError(t, err)
}

func TestDeleteContainers_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)

	err := DeleteContainers(t.Context(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- DeleteNetwork ---

func TestDeleteNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil) // network rm
	c := docker.NewClient(&m)

	err := DeleteNetwork(t.Context(), c, docker.NetworkName("sind-dev-net"))

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "rm", "sind-dev-net"}, m.Calls[0].Args)
}

func TestDeleteNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("network has active endpoints"))
	c := docker.NewClient(&m)

	err := DeleteNetwork(t.Context(), c, docker.NetworkName("sind-dev-net"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing network sind-dev-net")
}

// TestDeleteNetwork_AlreadyGone covers a network that was manually removed.
func TestDeleteNetwork_AlreadyGone(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such network\n", testutil.ExitCode1(t))
	c := docker.NewClient(&m)

	err := DeleteNetwork(t.Context(), c, docker.NetworkName("sind-dev-net"))
	require.NoError(t, err)
}

// --- DeleteVolumes ---

func TestDeleteVolumes(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result { return mock.Result{} }
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)

	require.NoError(t, err)
	// Order is nondeterministic (parallel removal).
	args := make([][]string, 0, len(m.Calls))
	for _, call := range m.Calls {
		args = append(args, call.Args)
	}
	assert.ElementsMatch(t, [][]string{
		{"volume", "rm", "sind-dev-config"},
		{"volume", "rm", "sind-dev-munge"},
		{"volume", "rm", "sind-dev-data"},
	}, args)
}

// volumeRmOnCall serves docker volume rm with a result per volume name;
// volumes without one are removed.
func volumeRmOnCall(results map[string]mock.Result) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		return results[args[2]]
	}
}

// TestDeleteVolumes_Error checks that a failed removal does not stop the
// others and that every failure is reported.
func TestDeleteVolumes_Error(t *testing.T) {
	var m mock.Executor
	m.OnCall = volumeRmOnCall(map[string]mock.Result{
		"sind-dev-munge": {Err: fmt.Errorf("permission denied")},
		"sind-dev-data":  {Err: fmt.Errorf("device busy")},
	})
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)

	require.Error(t, err)
	assert.Equal(t, "removing volume sind-dev-munge: permission denied\nremoving volume sind-dev-data: device busy", err.Error())
	assert.Len(t, m.Calls, 3, "every volume is attempted")
}

// TestDeleteVolumes_AlreadyGone covers volumes that were manually removed.
func TestDeleteVolumes_AlreadyGone(t *testing.T) {
	var m mock.Executor
	m.OnCall = volumeRmOnCall(map[string]mock.Result{
		"sind-dev-munge": {Stderr: testutil.NoSuchVolume("sind-dev-munge"), Err: testutil.ExitCode1(t)},
	})
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 3)
}

// volumeInUse is what docker writes to stderr, with exit code 1, when a
// volume is still mounted by a container.
const volumeInUse = "Error response from daemon: remove sind-dev-munge: volume is in use - [0123456789ab]\n"

// TestDeleteVolumes_InUseRetried checks that a volume still held by a
// container that is going away is retried until the removal succeeds.
func TestDeleteVolumes_InUseRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var m mock.Executor
		m.AddResult("", volumeInUse, testutil.ExitCode1(t)) // munge still in use
		m.AddResult("", "", nil)                            // munge removed on retry
		c := docker.NewClient(&m)

		err := DeleteVolumes(t.Context(), c, []docker.VolumeName{"sind-dev-munge"})
		require.NoError(t, err)
		assert.Len(t, m.Calls, 2)
	})
}

// TestDeleteVolumes_StillInUse checks that a volume that stays in use is
// reported, not mistaken for one that is already gone and leaked. docker
// exits 1 for both, so only its stderr tells them apart.
func TestDeleteVolumes_StillInUse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var m mock.Executor
		for range 6 {
			m.AddResult("", volumeInUse, testutil.ExitCode1(t))
		}
		c := docker.NewClient(&m)

		err := DeleteVolumes(t.Context(), c, []docker.VolumeName{"sind-dev-munge"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "removing volume sind-dev-munge: exit status 1: Error response from daemon: remove sind-dev-munge: volume is in use")
		assert.Len(t, m.Calls, 6)
	})
}

func TestDeleteVolumes_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)

	err := DeleteVolumes(t.Context(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- DeregisterMesh ---

func TestDeregisterMesh(t *testing.T) {
	var m mock.Executor
	m.OnCall = meshDeregisterOnCall(
		"controller.dev.sind.sind ssh-ed25519 AAAA1\nworker-0.dev.sind.sind ssh-ed25519 AAAA2\n",
	)
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
		{Name: "sind-dev-worker-0"},
	}
	err := DeregisterMesh(t.Context(), mgr, "dev", containers)

	require.NoError(t, err)
}

func TestDeregisterMesh_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := DeregisterMesh(t.Context(), mgr, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// TestDeregisterMesh_DNSError verifies that a DNS-side failure during
// deregistration is logged and swallowed — the cluster is being torn down,
// stale records are harmless, and aborting would prevent the rest of the
// teardown from running.
func TestDeregisterMesh_DNSError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		// CopyFromContainer (read Corefile) fails
		if len(args) > 0 && args[0] == "cp" {
			return mock.Result{Err: fmt.Errorf("DNS container not running")}
		}
		return mock.Result{}
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeregisterMesh(t.Context(), mgr, "dev", containers)
	require.NoError(t, err)
}

func TestDeregisterMesh_KnownHostError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		// CopyFromContainer (read Corefile) → return valid Corefile
		if len(args) >= 2 && args[0] == "cp" && strings.Contains(args[1], "sind-dns") {
			return mock.Result{Stdout: testutil.TarArchive("Corefile", emptyCorefileContent())}
		}
		// CopyToContainer (write Corefile) → success
		if len(args) >= 2 && args[0] == "cp" && args[1] == "-" {
			return mock.Result{}
		}
		// InspectContainer (state check) → running
		if len(args) >= 2 && args[0] == "inspect" && strings.Contains(args[1], "sind-dns") {
			return mock.Result{Stdout: dnsRunningInspectJSON}
		}
		// Signal DNS → success
		if len(args) >= 2 && args[0] == "kill" {
			return mock.Result{}
		}
		// Start DNS → success
		if len(args) >= 2 && args[0] == "start" {
			return mock.Result{}
		}
		// ReadFile (known_hosts via exec cat) → fail
		if len(args) >= 2 && args[0] == "exec" {
			return mock.Result{Err: fmt.Errorf("SSH container not running")}
		}
		return mock.Result{}
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeregisterMesh(t.Context(), mgr, "dev", containers)
	require.NoError(t, err)
}

// TestDeregisterMesh_StartsStoppedMesh covers a delete after a host
// reboot: the stopped DNS container and relay start, DNS first, so the
// nodes leave known_hosts, which docker exec reads and writes in the
// relay, as well as the Corefile.
func TestDeregisterMesh_StartsStoppedMesh(t *testing.T) {
	started := map[string]bool{}
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect" && (args[1] == "sind-dns" || args[1] == "sind-ssh"):
			state := "exited"
			if started[args[1]] {
				state = "running"
			}
			return mock.Result{Stdout: inspectJSON(t, args[1], state, nil)}
		case args[0] == "start":
			if args[1] == "sind-ssh" {
				assert.True(t, started["sind-dns"], "DNS starts before the relay")
			}
			started[args[1]] = true
			return mock.Result{}
		case args[0] == "exec" && !started["sind-ssh"]:
			t.Errorf("docker exec in the stopped relay: %v", args)
			return mock.Result{Stderr: "Error response from daemon: container is not running\n", Err: fmt.Errorf("exit status 1")}
		case args[0] == "exec" && args[1] == "sind-ssh" && args[2] == "cat":
			return mock.Result{Stdout: "controller.dev.sind.sind ssh-ed25519 K1\ncontroller.other.sind.sind ssh-ed25519 K2\n"}
		case args[0] == "exec" && args[1] == "-i" && args[2] == "sind-ssh":
			return mock.Result{}
		case args[0] == "cp" && args[1] == "sind-dns:/Corefile":
			return mock.Result{Stdout: testutil.TarArchive("Corefile", emptyCorefileContent())}
		case args[0] == "cp", args[0] == "kill":
			return mock.Result{}
		}
		t.Errorf("unexpected docker call: %v", args)
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	c := docker.NewClient(&m)

	err := DeregisterMesh(t.Context(), mesh.NewManager(c, mesh.DefaultRealm), "dev", []docker.ContainerListEntry{{Name: "sind-dev-controller"}})

	require.NoError(t, err)
	assert.True(t, started["sind-ssh"], "relay started")
	var written []string
	for _, call := range m.Calls {
		if call.Args[0] == "exec" && call.Args[1] == "-i" && call.Args[2] == "sind-ssh" {
			written = append(written, call.Stdin)
		}
	}
	assert.Equal(t, []string{"controller.other.sind.sind ssh-ed25519 K2\n"}, written)
}

// TestDeregisterMesh_StartMeshError covers a mesh that cannot be inspected:
// the failure is logged, and the removals are still tried.
func TestDeregisterMesh_StartMeshError(t *testing.T) {
	var m mock.Executor
	base := meshDeregisterOnCall("controller.dev.sind.sind ssh-ed25519 K1\n")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "inspect" && args[1] == "sind-ssh" {
			return mock.Result{Err: fmt.Errorf("daemon gone")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)
	var logs strings.Builder
	ctx := sindlog.With(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)))

	err := DeregisterMesh(ctx, mesh.NewManager(c, mesh.DefaultRealm), "dev", []docker.ContainerListEntry{{Name: "sind-dev-controller"}})

	require.NoError(t, err)
	assert.Contains(t, logs.String(), "starting the mesh failed, continuing")
	assert.Contains(t, logs.String(), "inspecting SSH container: daemon gone")
	assert.Equal(t, 1, countCalls(m.Calls, "exec", "-i", "sind-ssh"), "known_hosts written")
	assert.Equal(t, 1, countCalls(m.Calls, "kill", "-s", "USR1", "sind-dns"), "CoreDNS reloaded")
}

// --- Delete Orchestrator ---

func TestDelete_FullCluster(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []testutil.PsEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
			{ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img"},
		},
		networkExists: true,
		volumes:       []string{"config", "munge", "data"},
		otherClusters: false,
		knownHosts:    "controller.dev.sind.sind ssh-ed25519 K1\nworker-0.dev.sind.sind ssh-ed25519 K2\n",
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.NoError(t, err)
}

func TestDelete_NonExistent(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "gone", deleteOnCallOpts{
		containers:    nil,
		networkExists: false,
		volumes:       nil,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "gone")

	require.NoError(t, err)
}

func TestDelete_Partial(t *testing.T) {
	// Partial cluster: containers gone, but network and some volumes remain
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:    nil,
		networkExists: true,
		volumes:       []string{"config", "data"}, // munge already gone
		otherClusters: true,                       // other clusters exist
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.NoError(t, err)
}

func TestDelete_PreserveMesh(t *testing.T) {
	// Other clusters exist, mesh should be preserved
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []testutil.PsEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists: true,
		volumes:       []string{"config", "munge", "data"},
		otherClusters: true,
		knownHosts:    "controller.dev.sind.sind ssh-ed25519 K1\n",
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.NoError(t, err)
}

func TestDelete_ListResourcesError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result {
		return mock.Result{Err: fmt.Errorf("docker ps failed")}
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

// TestDelete_DeregisterMeshFailureContinues verifies that a failure during
// DNS record removal does not abort the whole delete — the cluster is being
// torn down and the orchestrator should continue removing containers,
// network and volumes.
func TestDelete_DeregisterMeshFailureContinues(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []testutil.PsEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists:  true,
		volumes:        []string{"config", "munge", "data"},
		deregisterFail: true,
		otherClusters:  true, // skip CleanupMesh to keep the test focused
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")
	require.NoError(t, err)

	// Cluster container and network removal must have happened despite the
	// failed DNS update.
	var rmCount, netRm int
	for _, call := range m.Calls {
		switch {
		case len(call.Args) >= 3 && call.Args[0] == "rm" && call.Args[1] == "-f":
			rmCount++
		case len(call.Args) >= 3 && call.Args[0] == "network" && call.Args[1] == "rm":
			netRm++
		}
	}
	assert.Equal(t, 1, rmCount, "container removed")
	assert.Equal(t, 1, netRm, "network removed")
}

func TestDelete_ContainerRemoveError(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []testutil.PsEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists:       true,
		volumes:             []string{"config", "munge", "data"},
		containerRemoveFail: true,
		knownHosts:          "controller.dev.sind.sind ssh-ed25519 K1\n",
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container")
}

func TestDelete_NetworkRemoveError(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:        nil,
		networkExists:     true,
		volumes:           nil,
		networkRemoveFail: true,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing network")
}

func TestDelete_HasOtherClustersError(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:    nil,
		networkExists: false,
		volumes:       []string{"config"},
	})
	// Override OnCall to make HasOtherClusters fail.
	inner := m.OnCall
	callCount := 0
	m.OnCall = func(args []string, stdin string) mock.Result {
		// The second "ps" call is HasOtherClusters (the first is ListClusterResources).
		if args[0] == "ps" {
			callCount++
			if callCount == 2 {
				return mock.Result{Err: fmt.Errorf("docker daemon unreachable")}
			}
		}
		return inner(args, stdin)
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestDelete_VolumeRemoveError(t *testing.T) {
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:       nil,
		networkExists:    false,
		volumes:          []string{"config"},
		volumeRemoveFail: true,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume")
}

func TestDelete_CleanupMeshError(t *testing.T) {
	// No other clusters → CleanupMesh runs and fails.
	var m mock.Executor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:    nil,
		networkExists: false,
		volumes:       []string{"config"},
		otherClusters: false,
	})
	// Override: make CleanupMesh's `rm -f` of the SSH keygen container
	// fail with a real (non-IsNotFound) error.
	inner := m.OnCall
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 4 && args[0] == "rm" && args[3] == "sind-ssh-keygen" {
			return mock.Result{Err: fmt.Errorf("docker daemon unreachable")}
		}
		return inner(args, stdin)
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH keygen container")
}

// meshUpdates returns the recorded calls that update the realm's Corefile
// or known_hosts.
func meshUpdates(m *mock.Executor) []string {
	var out []string
	for _, c := range m.Calls {
		joined := strings.Join(c.Args, " ")
		if strings.HasPrefix(joined, "cp ") || strings.HasPrefix(joined, "exec sind-ssh") || strings.HasPrefix(joined, "exec -i sind-ssh") {
			out = append(out, joined)
		}
	}
	return out
}

// TestDelete_LastClusterSkipsDeregistration covers the last cluster of a
// realm: the mesh goes with it, so its nodes are not deregistered first.
func TestDelete_LastClusterSkipsDeregistration(t *testing.T) {
	var m mock.Executor
	m.OnCall = deleteOnCall(t, notFoundErr(t), "dev", deleteOnCallOpts{
		containers: []testutil.PsEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists: true,
		volumes:       []string{"config"},
		otherClusters: false,
	})
	c := docker.NewClient(&m)

	err := Delete(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm), "dev")

	require.NoError(t, err)
	assert.Empty(t, meshUpdates(&m))
	var removed []string
	for _, call := range m.Calls {
		if call.Args[0] == "rm" {
			removed = append(removed, call.Args[3])
		}
	}
	assert.Contains(t, removed, "sind-dns", "mesh removed")
}

// TestDelete_OtherClusterDeregisters covers a realm that keeps its mesh: the
// nodes leave the Corefile and known_hosts.
func TestDelete_OtherClusterDeregisters(t *testing.T) {
	var m mock.Executor
	m.OnCall = deleteOnCall(t, notFoundErr(t), "dev", deleteOnCallOpts{
		containers: []testutil.PsEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists: true,
		volumes:       []string{"config"},
		otherClusters: true,
		knownHosts:    "controller.dev.sind.sind ssh-ed25519 K1\n",
	})
	c := docker.NewClient(&m)

	err := Delete(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm), "dev")

	require.NoError(t, err)
	updates := meshUpdates(&m)
	assert.Contains(t, updates, "cp sind-dns:/Corefile -")
	assert.Contains(t, updates, "exec sind-ssh cat /root/.ssh/known_hosts")
	for _, call := range m.Calls {
		if call.Args[0] == "rm" {
			assert.NotEqual(t, "sind-dns", call.Args[3], "mesh kept")
		}
	}
}

// --- DeleteAll ---

// realmFake answers the calls of DeleteAll for a realm whose clusters have
// the given nodes. Clusters in containersOnly have labelled containers but
// unlabelled networks and volumes (sind before v0.9.0). fail answers a call
// first when it returns true.
type realmFake struct {
	t              *testing.T
	clusters       map[string][]string
	containersOnly map[string]bool
	fail           func(args []string) bool
}

func (f *realmFake) onCall(args []string, _ string) mock.Result {
	if f.fail != nil && f.fail(args) {
		return mock.Result{Err: fmt.Errorf("docker daemon unavailable")}
	}
	joined := strings.Join(args, " ")
	notFound := mock.Result{Stderr: "Error: No such object\n", Err: notFoundErr(f.t)}
	lsEntries := func() string {
		var lines []string
		for name := range f.clusters {
			if !f.containersOnly[name] {
				lines = append(lines, fmt.Sprintf(`{"Name":"sind-%s-net","Labels":"sind.cluster=%s,sind.realm=sind"}`, name, name))
			}
		}
		return strings.Join(lines, "\n")
	}
	switch {
	case strings.HasPrefix(joined, "network ls"), strings.HasPrefix(joined, "volume ls"):
		return mock.Result{Stdout: lsEntries()}
	case args[0] == "ps":
		filter := args[len(args)-1]
		var entries []testutil.PsEntry
		for name, nodes := range f.clusters {
			if filter != "label=sind.cluster" && filter != "label=sind.cluster="+name {
				continue
			}
			for _, n := range nodes {
				entries = append(entries, testutil.PsEntry{ID: n, Names: "sind-" + name + "-" + n, State: "running", Image: "img",
					Labels: "sind.cluster=" + name + ",sind.realm=sind"})
			}
		}
		if len(entries) == 0 {
			return mock.Result{}
		}
		return mock.Result{Stdout: testutil.NDJSON(entries...)}
	case args[0] == "network" && args[1] == "inspect":
		for name := range f.clusters {
			if args[2] == "sind-"+name+"-net" {
				return mock.Result{Stdout: "{}\n"}
			}
		}
		return notFound
	case args[0] == "volume" && args[1] == "inspect":
		for name := range f.clusters {
			if args[2] == "sind-"+name+"-config" {
				return mock.Result{Stdout: "{}\n"}
			}
		}
		return notFound
	case args[0] == "rm", args[0] == "network" && (args[1] == "rm" || args[1] == "disconnect"), args[0] == "volume" && args[1] == "rm":
		return mock.Result{}
	case args[0] == "cp" && args[1] == "sind-dns:/Corefile":
		return mock.Result{Stdout: testutil.TarArchive("Corefile", emptyCorefileContent())}
	case args[0] == "cp", args[0] == "kill":
		return mock.Result{}
	case joined == "inspect sind-dns":
		return mock.Result{Stdout: dnsRunningInspectJSON}
	case joined == "inspect sind-ssh":
		return mock.Result{Stdout: sshRunningInspectJSON}
	case args[0] == "exec":
		return mock.Result{}
	}
	f.t.Errorf("unexpected docker call: %s", joined)
	return mock.Result{Err: fmt.Errorf("unexpected call: %s", joined)}
}

// removed returns the containers, networks and volumes m removed.
func removed(m *mock.Executor) []string {
	var out []string
	for _, c := range m.Calls {
		switch {
		case c.Args[0] == "rm":
			out = append(out, c.Args[3])
		case c.Args[0] == "network" && c.Args[1] == "rm", c.Args[0] == "volume" && c.Args[1] == "rm":
			out = append(out, c.Args[2])
		}
	}
	return out
}

func TestDeleteAll(t *testing.T) {
	f := &realmFake{t: t,
		clusters: map[string][]string{
			"dev":    {"controller", "worker-0"},
			"test":   {"controller"},
			"legacy": {"controller"}, // found by its containers only
		},
		containersOnly: map[string]bool{"legacy": true},
	}
	var m mock.Executor
	m.OnCall = f.onCall
	c := docker.NewClient(&m)

	err := DeleteAll(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm))

	require.NoError(t, err)
	got := removed(&m)
	assert.Subset(t, got, []string{
		"sind-dev-controller", "sind-dev-worker-0", "sind-test-controller", "sind-legacy-controller",
		"sind-dev-net", "sind-test-net", "sind-legacy-net",
		"sind-ssh", "sind-dns", "sind-mesh", "sind-ssh-config",
	})
	assert.Empty(t, meshUpdates(&m), "no deregistration from a mesh that goes")
	assert.Equal(t, "volume rm sind-ssh-config", strings.Join(m.Calls[len(m.Calls)-1].Args, " "), "mesh removed last")
}

// TestDeleteAll_MeshOnly covers a realm left with a mesh and no cluster, as
// after a killed create: the mesh is removed.
func TestDeleteAll_MeshOnly(t *testing.T) {
	f := &realmFake{t: t, clusters: map[string][]string{}}
	var m mock.Executor
	m.OnCall = f.onCall
	c := docker.NewClient(&m)

	err := DeleteAll(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm))

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"sind-ssh-keygen", "sind-ssh", "sind-dns", "sind-mesh", "sind-ssh-config"}, removed(&m))
}

// TestDeleteAll_ClusterFails covers one cluster that fails to delete: the
// others are deleted and deregistered, the mesh stays, and the error names
// the cluster.
func TestDeleteAll_ClusterFails(t *testing.T) {
	f := &realmFake{t: t,
		clusters: map[string][]string{"dev": {"controller"}, "test": {"controller"}},
		fail: func(args []string) bool {
			return args[0] == "rm" && args[3] == "sind-test-controller"
		},
	}
	var m mock.Executor
	m.OnCall = f.onCall
	c := docker.NewClient(&m)

	err := DeleteAll(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting cluster test")
	got := removed(&m)
	assert.Contains(t, got, "sind-dev-controller")
	assert.NotContains(t, got, "sind-dns", "mesh kept for test")

	// Only dev's node left the mesh.
	var corefile string
	for _, call := range m.Calls {
		if call.Args[0] == "cp" && call.Args[1] == "-" {
			corefile = call.Stdin
		}
	}
	require.NotEmpty(t, corefile, "Corefile rewritten")
	assert.Contains(t, meshUpdates(&m), "exec sind-ssh cat /root/.ssh/known_hosts")
}

func TestDeleteAll_ListError(t *testing.T) {
	f := &realmFake{t: t,
		clusters: map[string][]string{"dev": {"controller"}},
		fail: func(args []string) bool {
			return args[0] == "ps" && args[len(args)-1] == "label=sind.cluster=dev"
		},
	}
	var m mock.Executor
	m.OnCall = f.onCall
	c := docker.NewClient(&m)

	err := DeleteAll(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting cluster dev: listing containers")
}

func TestDeleteAll_DiscoveryErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		fail  func(args []string) bool
		match string
	}{
		{"networks", func(a []string) bool { return a[0] == "network" && a[1] == "ls" }, "listing networks"},
		{"containers", func(a []string) bool { return a[0] == "ps" }, "listing containers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &realmFake{t: t, clusters: map[string][]string{"dev": {"controller"}}, fail: tt.fail}
			var m mock.Executor
			m.OnCall = f.onCall
			c := docker.NewClient(&m)

			err := DeleteAll(t.Context(), c, mesh.NewManager(c, mesh.DefaultRealm))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
			assert.Empty(t, removed(&m))
		})
	}
}

// --- helpers ---

// indexOf returns the index of s in slice, or -1 if not found.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}

// dnsRunningInspectJSON is a mock docker inspect result reporting the
// sind-dns container as running. Used by DeregisterMesh tests to satisfy the
// container-state check that gates the DNS reload.
const dnsRunningInspectJSON = `[{"Id":"dns123","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`

// sshRunningInspectJSON is a mock docker inspect result reporting the
// sind-ssh relay as running, for the mesh start that precedes
// deregistration.
const sshRunningInspectJSON = `[{"Id":"ssh123","Name":"/sind-ssh","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`

// meshDeregisterOnCall returns a mock OnCall that handles RemoveDNSRecord
// and RemoveKnownHost operations for DeregisterMesh tests.
func meshDeregisterOnCall(knownHostsContent string) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{}
		}
		switch {
		// CopyFromContainer: docker cp sind-dns:/Corefile -
		case args[0] == "cp" && len(args) >= 2 && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: testutil.TarArchive("Corefile", emptyCorefileContent())}
		// CopyToContainer: docker cp - sind-dns:/
		case args[0] == "cp" && len(args) >= 2 && args[1] == "-":
			return mock.Result{}
		// InspectContainer: docker inspect sind-dns (state check before reload)
		case args[0] == "inspect" && len(args) >= 2 && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: dnsRunningInspectJSON}
		// InspectContainer: docker inspect sind-ssh (mesh start)
		case args[0] == "inspect" && len(args) >= 2 && args[1] == "sind-ssh":
			return mock.Result{Stdout: sshRunningInspectJSON}
		// Signal: docker kill -s HUP sind-dns
		case args[0] == "kill":
			return mock.Result{}
		// Start: docker start sind-dns (DNS reload)
		case args[0] == "start":
			return mock.Result{}
		// ReadFile: docker exec sind-ssh cat /root/.ssh/known_hosts
		case args[0] == "exec" && len(args) >= 3 && args[2] == "cat":
			return mock.Result{Stdout: knownHostsContent}
		// WriteFile: docker exec -i sind-ssh sh -c 'cat > ...'
		case args[0] == "exec" && len(args) >= 2 && args[1] == "-i":
			return mock.Result{}
		}
		return mock.Result{}
	}
}

// deleteOnCallOpts configures the behavior of deleteOnCall.
type deleteOnCallOpts struct {
	containers          []testutil.PsEntry
	networkExists       bool
	volumes             []string // volume type suffixes that exist, e.g. ["config", "munge", "data"]
	otherClusters       bool
	knownHosts          string
	deregisterFail      bool
	containerRemoveFail bool
	networkRemoveFail   bool
	volumeRemoveFail    bool
}

// deleteOnCall returns a mock OnCall handler for the Delete orchestrator tests.
// It dispatches based on docker command arguments to simulate the full delete flow.
func deleteOnCall(t *testing.T, exitErr *exec.ExitError, clusterName string, opts deleteOnCallOpts) func([]string, string) mock.Result {
	t.Helper()
	containerJSON := ""
	if len(opts.containers) > 0 {
		containerJSON = testutil.NDJSON(opts.containers...)
	}
	// Track which phase we're in based on calls.
	listClusterDone := false

	return func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{}
		}

		switch {
		// docker ps -a ... (ListContainers or HasOtherClusters)
		case args[0] == "ps":
			filterVal := ""
			for i, a := range args {
				if a == "--filter" && i+1 < len(args) {
					filterVal = args[i+1]
				}
			}

			if !listClusterDone && strings.Contains(filterVal, "="+clusterName) {
				// ListClusterResources: return cluster containers
				listClusterDone = true
				return mock.Result{Stdout: containerJSON}
			}
			// HasOtherClusters: return other cluster containers
			if opts.otherClusters {
				return mock.Result{Stdout: testutil.NDJSON(
					testutil.PsEntry{ID: "x", Names: "sind-other-controller", State: "running", Image: "img"},
				)}
			}
			return mock.Result{Stdout: ""}

		// docker network inspect (NetworkExists check)
		case args[0] == "network" && args[1] == "inspect":
			if opts.networkExists {
				return mock.Result{}
			}
			return mock.Result{Stderr: "Error: No such network\n", Err: exitErr}

		// docker network rm (DeleteNetwork)
		case args[0] == "network" && args[1] == "rm":
			if opts.networkRemoveFail {
				return mock.Result{Err: fmt.Errorf("network has active endpoints")}
			}
			return mock.Result{}

		// docker volume ls --filter name=sind-<cluster>- (ListClusterResources)
		case args[0] == "volume" && args[1] == "ls":
			var entries []string
			for _, v := range opts.volumes {
				entries = append(entries, `{"Name":"sind-`+clusterName+`-`+v+`","Driver":"local","Labels":""}`)
			}
			return mock.Result{Stdout: strings.Join(entries, "\n")}

		// docker volume rm (DeleteVolumes)
		case args[0] == "volume" && args[1] == "rm":
			if opts.volumeRemoveFail {
				return mock.Result{Err: fmt.Errorf("volume in use")}
			}
			return mock.Result{}

		// docker kill (DeleteContainers - best effort)
		case args[0] == "kill":
			return mock.Result{}

		// docker rm (DeleteContainers or CleanupMesh)
		case args[0] == "rm":
			if opts.containerRemoveFail {
				return mock.Result{Err: fmt.Errorf("removal failed")}
			}
			return mock.Result{}

		// docker cp (DNS Corefile read/write for DeregisterMesh)
		case args[0] == "cp":
			if opts.deregisterFail && strings.Contains(args[1], "sind-dns") {
				return mock.Result{Err: fmt.Errorf("DNS container not running")}
			}
			if len(args) >= 2 && strings.Contains(args[1], "sind-dns") {
				return mock.Result{Stdout: testutil.TarArchive("Corefile", emptyCorefileContent())}
			}
			return mock.Result{}

		// docker inspect sind-dns (state check before DNS reload)
		case args[0] == "inspect" && len(args) >= 2 && strings.Contains(args[1], "sind-dns"):
			return mock.Result{Stdout: dnsRunningInspectJSON}

		// docker inspect sind-ssh (mesh start before deregistration)
		case args[0] == "inspect" && len(args) >= 2 && args[1] == "sind-ssh":
			return mock.Result{Stdout: sshRunningInspectJSON}

		// docker kill -s HUP (DNS reload)
		case args[0] == "kill":
			return mock.Result{}

		// docker start sind-dns (DNS reload)
		case args[0] == "start":
			return mock.Result{}

		// docker exec (known_hosts read/write for DeregisterMesh or CleanupMesh)
		case args[0] == "exec":
			if len(args) >= 3 && args[2] == "cat" {
				return mock.Result{Stdout: opts.knownHosts}
			}
			return mock.Result{}

		// docker container inspect (ContainerExists for CleanupMesh)
		case args[0] == "container" && args[1] == "inspect":
			if !opts.otherClusters {
				// Mesh containers exist during cleanup
				return mock.Result{}
			}
			return mock.Result{Stderr: testutil.NoSuchContainer(args[2]), Err: exitErr}
		}

		t.Logf("unhandled mock call: %v", args)
		return mock.Result{}
	}
}
