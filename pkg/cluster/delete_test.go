// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
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
	names := []string{m.Calls[0].Args[2], m.Calls[1].Args[2]}
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
	m.AddResult("", "", nil) // config
	m.AddResult("", "", nil) // munge
	m.AddResult("", "", nil) // data
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"volume", "rm", "sind-dev-config"}, m.Calls[0].Args)
	assert.Equal(t, []string{"volume", "rm", "sind-dev-munge"}, m.Calls[1].Args)
	assert.Equal(t, []string{"volume", "rm", "sind-dev-data"}, m.Calls[2].Args)
}

func TestDeleteVolumes_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)                         // config OK
	m.AddResult("", "", fmt.Errorf("volume in use")) // munge fails
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume sind-dev-munge")
}

// TestDeleteVolumes_AlreadyGone covers volumes that were manually removed.
func TestDeleteVolumes_AlreadyGone(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)                                          // config OK
	m.AddResult("", "Error: No such volume\n", testutil.ExitCode1(t)) // munge already gone
	m.AddResult("", "", nil)                                          // data OK
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)
	require.NoError(t, err)
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
	// Override: make CleanupMesh's first `rm -f` (for the SSH keygen container)
	// fail with a real (non-IsNotFound) error.
	inner := m.OnCall
	rmSeen := false
	m.OnCall = func(args []string, stdin string) mock.Result {
		if !rmSeen && len(args) >= 3 && args[0] == "rm" && args[1] == "-f" {
			rmSeen = true
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
	// Build set of existing volumes for quick lookup.
	existingVolumes := map[string]bool{}
	for _, v := range opts.volumes {
		existingVolumes["sind-"+clusterName+"-"+v] = true
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

		// docker volume inspect (VolumeExists check)
		case args[0] == "volume" && args[1] == "inspect":
			volName := args[2]
			if existingVolumes[volName] {
				return mock.Result{}
			}
			return mock.Result{Stderr: "Error: No such volume\n", Err: exitErr}

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
			return mock.Result{Stderr: "Error\n", Err: exitErr}
		}

		t.Logf("unhandled mock call: %v", args)
		return mock.Result{}
	}
}
