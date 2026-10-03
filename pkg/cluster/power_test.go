// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listErrorMock returns a mock that fails on any call, simulating docker daemon unavailability.
func listErrorMock() *mock.Executor {
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result {
		return mock.Result{Err: fmt.Errorf("docker daemon unavailable")}
	}
	return &m
}

// powerContainers returns a standard set of cluster containers for power tests.
func powerContainers() string {
	return testutil.NDJSON(
		testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=controller"},
		testutil.PsEntry{ID: "c2", Names: "sind-dev-worker-0", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
		testutil.PsEntry{ID: "c3", Names: "sind-dev-worker-1", State: "running", Image: "img:1",
			Labels: "sind.cluster=dev,sind.role=worker"},
	)
}

// --- PowerShutdown ---

func TestPower_Shutdown(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return mock.Result{}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	// Verify docker stop was called for each node.
	var stops []string
	for _, c := range m.Calls {
		if c.Args[0] == "stop" {
			stops = append(stops, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, stops)
}

func TestPower_Shutdown_NodeNotFound(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-99"})

	require.ErrorIs(t, err, ErrNodeNotFound)
	assert.Contains(t, err.Error(), "worker-99")
	assert.Contains(t, err.Error(), "not found")
}

func TestPower_Shutdown_StopError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "stop" {
			return mock.Result{Err: fmt.Errorf("container already stopped")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")
}

func TestPower_Shutdown_EmptyNodes(t *testing.T) {
	var m mock.Executor
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_Shutdown_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

// TestPower_LabelFilter asserts resolveTargets scopes the container list by
// both realm and cluster labels so parallel realms with identically-named
// clusters do not see each other's nodes.
func TestPower_LabelFilter(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := PowerShutdown(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})
	require.NoError(t, err)

	require.NotEmpty(t, m.Calls)
	psArgs := m.Calls[0].Args
	assert.Contains(t, psArgs, "label=sind.realm="+mesh.DefaultRealm)
	assert.Contains(t, psArgs, "label=sind.cluster=dev")
}

// --- PowerCut ---

func TestPower_Cut(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return mock.Result{}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "controller"})

	require.NoError(t, err)

	var kills []string
	for _, c := range m.Calls {
		if c.Args[0] == "kill" {
			kills = append(kills, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-controller"}, kills)
}

func TestPower_Cut_EmptyNodes(t *testing.T) {
	var m mock.Executor
	client := docker.NewClient(&m)

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_Cut_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Cut_KillError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "kill" {
			return mock.Result{Err: fmt.Errorf("container not running")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := PowerCut(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "killing")
}

// --- PowerOn ---

// powerOnCall answers the calls of PowerOn, PowerReboot and PowerCycle for
// the containers of powerContainers in a realm without mesh. handle can
// answer a call first.
func powerOnCall(t *testing.T, handle func(args []string, stdin string) (mock.Result, bool)) func([]string, string) mock.Result {
	t.Helper()
	return func(args []string, stdin string) mock.Result {
		if handle != nil {
			if r, ok := handle(args, stdin); ok {
				return r
			}
		}
		switch {
		case args[0] == "ps":
			return mock.Result{Stdout: powerContainers()}
		case args[0] == "inspect" && (args[1] == "sind-dns" || args[1] == "sind-ssh"):
			return mock.Result{Stderr: testutil.NoSuchContainer(args[1]), Err: testutil.ExitCode1(t)}
		case args[0] == "stop", args[0] == "kill", args[0] == "start":
			return mock.Result{}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

// meshCall answers the mesh's calls for a running mesh whose DNS container
// has 10.0.9.2, and whose Corefile has corefile: the inspects, the Corefile
// read and write, and the reload. The written Corefile goes to written.
func meshCall(t *testing.T, corefile []string, written *string) func(args []string, stdin string) (mock.Result, bool) {
	t.Helper()
	return func(args []string, stdin string) (mock.Result, bool) {
		switch {
		case args[0] == "inspect" && args[1] == "sind-dns":
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "running", map[docker.NetworkName]string{"sind-mesh": "10.0.9.2"})}, true
		case args[0] == "inspect" && args[1] == "sind-ssh":
			return mock.Result{Stdout: inspectJSON(t, "sind-ssh", "running", nil)}, true
		case args[0] == "cp" && args[1] == "sind-dns:/Corefile":
			return mock.Result{Stdout: testutil.TarArchive("Corefile", corefileWith(corefile))}, true
		case args[0] == "cp" && args[1] == "-":
			*written = stdin
			return mock.Result{}, true
		case args[0] == "kill" && args[1] == "-s":
			return mock.Result{}, true
		}
		return mock.Result{}, false
	}
}

// corefileWith returns a Corefile of realm sind with the given host entries.
func corefileWith(entries []string) string {
	var b strings.Builder
	b.WriteString("sind.sind:53 {\n    hosts {\n")
	for _, e := range entries {
		b.WriteString("        " + e + "\n")
	}
	b.WriteString("        fallthrough\n    }\n    reload\n}\n")
	return b.String()
}

// startedNodesJSON is the inspect output of worker-0 and worker-1 after a
// start, on the cluster network, created with the DNS address dns.
func startedNodesJSON(dns string) string {
	return `[{"Id":"c2","Name":"/sind-dev-worker-0","State":{"Status":"running"},"HostConfig":{"Dns":["` + dns + `"]},"NetworkSettings":{"Networks":{"sind-dev-net":{"IPAddress":"172.20.0.7"}}}},` +
		`{"Id":"c3","Name":"/sind-dev-worker-1","State":{"Status":"running"},"HostConfig":{"Dns":["` + dns + `"]},"NetworkSettings":{"Networks":{"sind-dev-net":{"IPAddress":"172.20.0.3"}}}}]`
}

func TestPower_On(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, nil)
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	var starts []string
	for _, c := range m.Calls {
		if c.Args[0] == "start" {
			starts = append(starts, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, starts)
}

// TestPower_On_Mesh covers power on after a host reboot: the mesh is
// stopped, starts before the nodes, and the nodes' DNS records follow their
// new addresses.
func TestPower_On_Mesh(t *testing.T) {
	var written string
	meshRunning := false
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
		switch {
		case args[0] == "inspect" && args[1] == "sind-dns" && !meshRunning:
			return mock.Result{Stdout: inspectJSON(t, "sind-dns", "exited", nil)}, true
		case args[0] == "inspect" && args[1] == "sind-ssh" && !meshRunning:
			return mock.Result{Stdout: inspectJSON(t, "sind-ssh", "exited", nil)}, true
		case args[0] == "start" && args[1] == "sind-ssh":
			meshRunning = true
			return mock.Result{}, true
		case args[0] == "inspect" && args[1] == "sind-dev-worker-0":
			return mock.Result{Stdout: startedNodesJSON("10.0.9.2")}, true
		}
		return meshCall(t, []string{"172.20.0.3 worker-0.dev.sind.sind", "172.20.0.4 controller.dev.sind.sind"}, &written)(args, stdin)
	})
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	var warned []string
	mgr.OnWarning = func(msg string) { warned = append(warned, msg) }

	err := PowerOn(t.Context(), client, mgr, "dev", []string{"worker-0", "worker-1"})
	require.NoError(t, err)

	var starts []string
	for _, c := range m.Calls {
		if c.Args[0] == "start" {
			starts = append(starts, c.Args[1])
		}
	}
	require.Len(t, starts, 4)
	assert.Equal(t, []string{"sind-dns", "sind-ssh"}, starts[:2], "mesh first, DNS before the relay")
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, starts[2:])

	corefile := written // the tar holds the Corefile as plain text
	assert.Contains(t, corefile, "172.20.0.7 worker-0.dev.sind.sind", "new address")
	assert.Contains(t, corefile, "172.20.0.3 worker-1.dev.sind.sind")
	assert.Contains(t, corefile, "172.20.0.4 controller.dev.sind.sind", "other nodes kept")
	assert.NotContains(t, corefile, "172.20.0.3 worker-0")
	assert.Empty(t, warned)
}

// TestPower_On_StaleDNS covers nodes created with a mesh DNS address the DNS
// container no longer has.
func TestPower_On_StaleDNS(t *testing.T) {
	var written string
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
		if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Stdout: startedNodesJSON("10.0.9.5")}, true
		}
		return meshCall(t, nil, &written)(args, stdin)
	})
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	var warned []string
	mgr.OnWarning = func(msg string) { warned = append(warned, msg) }

	err := PowerOn(t.Context(), client, mgr, "dev", []string{"worker-0", "worker-1"})
	require.NoError(t, err)

	require.Len(t, warned, 1)
	assert.Contains(t, warned[0], "still use 10.0.9.5 (sind-dev-worker-0, sind-dev-worker-1)")
	assert.NotContains(t, warned[0], "SSH relay")
}

// TestPower_On_RecordsUnchanged covers nodes back at their old addresses:
// the Corefile is left alone.
func TestPower_On_RecordsUnchanged(t *testing.T) {
	var written string
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
		if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Stdout: startedNodesJSON("10.0.9.2")}, true
		}
		return meshCall(t, []string{"172.20.0.7 worker-0.dev.sind.sind", "172.20.0.3 worker-1.dev.sind.sind"}, &written)(args, stdin)
	})
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})
	require.NoError(t, err)

	for _, c := range m.Calls {
		assert.NotEqual(t, "-", c.Args[1], "no Corefile write: %v", c.Args)
	}
}

// TestPower_On_NodeWithoutClusterNetwork covers a node without an address
// on the cluster network: it gets no record.
func TestPower_On_NodeWithoutClusterNetwork(t *testing.T) {
	var written string
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
		if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Stdout: inspectJSON(t, "sind-dev-worker-0", "running", nil)}, true
		}
		return meshCall(t, nil, &written)(args, stdin)
	})
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})
	require.NoError(t, err)
	assert.Empty(t, written)
}

func TestPower_On_MeshErrors(t *testing.T) {
	tests := []struct {
		name  string
		call  func(args []string) bool
		match string
	}{
		{"mesh inspect", func(a []string) bool { return a[0] == "inspect" && a[1] == "sind-dns" }, "starting mesh"},
		{"node inspect", func(a []string) bool { return a[0] == "inspect" && a[1] == "sind-dev-worker-0" }, "inspecting started nodes"},
		{"Corefile", func(a []string) bool { return a[0] == "cp" }, "updating DNS records"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written string
			var m mock.Executor
			m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
				if tt.call(args) {
					return mock.Result{Err: fmt.Errorf("docker daemon unavailable")}, true
				}
				if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
					return mock.Result{Stdout: startedNodesJSON("10.0.9.2")}, true
				}
				return meshCall(t, nil, &written)(args, stdin)
			})
			client := docker.NewClient(&m)

			err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func TestPower_On_EmptyNodes(t *testing.T) {
	var m mock.Executor
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestPower_On_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

// TestPower_On_StartError covers one node that does not start: the other
// starts anyway, and the error names the failing node.
func TestPower_On_StartError(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "start" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Err: fmt.Errorf("container already running")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting sind-dev-worker-0")
	assert.NotContains(t, err.Error(), "worker-1")
	assert.Len(t, m.Calls, 1+2+2, "ps, two mesh inspects, both starts")
}

// TestPower_On_NothingStarted covers a realm with mesh where no node
// starts: no DNS record changes.
func TestPower_On_NothingStarted(t *testing.T) {
	var written string
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
		if args[0] == "start" {
			return mock.Result{Err: fmt.Errorf("start failed")}, true
		}
		return meshCall(t, nil, &written)(args, stdin)
	})
	client := docker.NewClient(&m)

	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting sind-dev-worker-0")
	assert.Empty(t, written)
	for _, c := range m.Calls {
		assert.NotEqual(t, "cp", c.Args[0], "no Corefile read")
	}
}

// --- PowerReboot ---

// powerOps returns the stop, kill and start calls of m in order.
func powerOps(m *mock.Executor) []string {
	var ops []string
	for _, c := range m.Calls {
		switch c.Args[0] {
		case "stop", "kill", "start":
			ops = append(ops, c.Args[0]+"="+c.Args[1])
		}
	}
	return ops
}

func TestPower_Reboot(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, nil)
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	// Every node goes down before any comes back up.
	ops := powerOps(&m)
	require.Len(t, ops, 4)
	assert.ElementsMatch(t, []string{"stop=sind-dev-worker-0", "stop=sind-dev-worker-1"}, ops[:2])
	assert.ElementsMatch(t, []string{"start=sind-dev-worker-0", "start=sind-dev-worker-1"}, ops[2:])
}

func TestPower_Reboot_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerReboot(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

// TestPower_Reboot_StopError covers a node that does not stop: it is not
// started, the other node reboots, and the error names the failing node.
func TestPower_Reboot_StopError(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "stop" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Err: fmt.Errorf("stop failed")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping sind-dev-worker-0")
	ops := powerOps(&m)
	assert.Contains(t, ops, "start=sind-dev-worker-1")
	assert.NotContains(t, ops, "start=sind-dev-worker-0")
}

func TestPower_Reboot_StartError(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "start" {
			return mock.Result{Err: fmt.Errorf("start failed")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// TestPower_Reboot_NothingStopped covers nodes that all fail to stop: no
// start, and no mesh call.
func TestPower_Reboot_NothingStopped(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "stop" {
			return mock.Result{Err: fmt.Errorf("stop failed")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	err := PowerReboot(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Len(t, m.Calls, 2, "ps and stop")
}

// --- PowerCycle ---

func TestPower_Cycle(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, nil)
	client := docker.NewClient(&m)

	err := PowerCycle(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	ops := powerOps(&m)
	require.Len(t, ops, 4)
	assert.ElementsMatch(t, []string{"kill=sind-dev-worker-0", "kill=sind-dev-worker-1"}, ops[:2])
	assert.ElementsMatch(t, []string{"start=sind-dev-worker-0", "start=sind-dev-worker-1"}, ops[2:])
}

func TestPower_Cycle_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerCycle(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Cycle_KillError(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "kill" {
			return mock.Result{Err: fmt.Errorf("kill failed")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	err := PowerCycle(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "killing")
}

func TestPower_Cycle_StartError(t *testing.T) {
	var m mock.Executor
	m.OnCall = powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "start" {
			return mock.Result{Err: fmt.Errorf("start failed")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	err := PowerCycle(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting")
}

// --- PowerFreeze ---

func TestPower_Freeze(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "pause" {
			return mock.Result{}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerFreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0", "worker-1"})

	require.NoError(t, err)

	var pauses []string
	for _, c := range m.Calls {
		if c.Args[0] == "pause" {
			pauses = append(pauses, c.Args[1])
		}
	}
	assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-worker-1"}, pauses)
}

func TestPower_Freeze_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerFreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Freeze_PauseError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "pause" {
			return mock.Result{Err: fmt.Errorf("container is not running")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := PowerFreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pausing")
}

// --- PowerUnfreeze ---

func TestPower_Unfreeze(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "unpause" {
			return mock.Result{}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	client := docker.NewClient(&m)

	err := PowerUnfreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.NoError(t, err)

	var unpauses []string
	for _, c := range m.Calls {
		if c.Args[0] == "unpause" {
			unpauses = append(unpauses, c.Args[1])
		}
	}
	assert.Equal(t, []string{"sind-dev-worker-0"}, unpauses)
}

func TestPower_Unfreeze_ListError(t *testing.T) {
	client := docker.NewClient(listErrorMock())

	err := PowerUnfreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing")
}

func TestPower_Unfreeze_UnpauseError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		if args[0] == "unpause" {
			return mock.Result{Err: fmt.Errorf("container is not paused")}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	err := PowerUnfreeze(t.Context(), client, mesh.DefaultRealm, "dev", []string{"worker-0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unpausing")
}

// --- Lifecycle ---

func TestPowerLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	realm := testutil.Realm("it-pwr")
	meshMgr := mesh.NewManager(c, realm)
	cluster := "it-pwr"
	ctrName := ContainerName(realm, cluster, "worker-0")

	if !rec.IsIntegration() {
		// Create container with labels
		rec.AddResult("ctr-id\n", "", nil)

		// Each power op: ListContainers (resolve targets) + the operation
		// itself. Starting ops first look for the realm's mesh, which
		// this test has none of.
		psLine := `{"ID":"ctr-id","Names":"` + string(ctrName) + `","State":"running","Image":"busybox:latest","Labels":"sind.cluster=` + cluster + `,sind.role=worker"}` + "\n"
		noMesh := func() {
			rec.AddResult("", testutil.NoSuchContainer("sind-dns"), testutil.ExitCode1(t))
			rec.AddResult("", testutil.NoSuchContainer("sind-ssh"), testutil.ExitCode1(t))
		}

		// PowerShutdown: ps + stop
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerOn: ps + mesh + start
		rec.AddResult(psLine, "", nil)
		noMesh()
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerFreeze: ps + pause
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerUnfreeze: ps + unpause
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerReboot: ps + stop + mesh + start
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)
		noMesh()
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// PowerCycle: ps + kill + mesh + start
		rec.AddResult(psLine, "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)
		noMesh()
		rec.AddResult(string(ctrName)+"\n", "", nil)

		// Cleanup: kill + rm
		rec.AddResult(string(ctrName)+"\n", "", nil)
		rec.AddResult(string(ctrName)+"\n", "", nil)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
	})

	// Create a labeled container.
	_, err := c.CreateContainer(ctx,
		"--name", string(ctrName),
		"--label", LabelRealm+"="+realm,
		"--label", LabelCluster+"="+cluster,
		"--label", LabelRole+"=worker",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)

	nodes := []string{"worker-0"}

	// Shutdown (stop) + On (start).
	err = PowerShutdown(ctx, c, realm, cluster, nodes)
	require.NoError(t, err)

	err = PowerOn(ctx, c, meshMgr, cluster, nodes)
	require.NoError(t, err)

	// Freeze (pause) + Unfreeze (unpause).
	err = PowerFreeze(ctx, c, realm, cluster, nodes)
	require.NoError(t, err)

	err = PowerUnfreeze(ctx, c, realm, cluster, nodes)
	require.NoError(t, err)

	// Reboot (stop + start).
	err = PowerReboot(ctx, c, meshMgr, cluster, nodes)
	require.NoError(t, err)

	// Cycle (kill + start).
	err = PowerCycle(ctx, c, meshMgr, cluster, nodes)
	require.NoError(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}
