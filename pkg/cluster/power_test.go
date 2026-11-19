// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
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

func TestPower_Shutdown_ClusterNotFound(t *testing.T) {
	client := docker.NewClient(notFoundExecutor(t, mock.Result{}))

	err := PowerShutdown(t.Context(), client, "ci-42", "dev", []string{"worker-0"})

	require.ErrorIs(t, err, ErrClusterNotFound)
	assert.EqualError(t, err, `cluster "dev" not found in realm "ci-42"`)
}

func TestCheckNode(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: powerContainers()}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)

	require.NoError(t, CheckNode(t.Context(), client, mesh.DefaultRealm, "dev", "worker-0"))

	err := CheckNode(t.Context(), client, mesh.DefaultRealm, "dev", "worker-99")
	require.ErrorIs(t, err, ErrNodeNotFound)
	assert.EqualError(t, err, `node "worker-99" not found in cluster "dev"`)

	client = docker.NewClient(notFoundExecutor(t, mock.Result{}))
	err = CheckNode(t.Context(), client, "ci-42", "dev", "worker-0")
	require.ErrorIs(t, err, ErrClusterNotFound)
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

// TestPower_ContainerOps covers the power commands that run one docker
// command on each node.
func TestPower_ContainerOps(t *testing.T) {
	for _, op := range []struct {
		name, docker, verb string
		run                func(context.Context, *docker.Client, string, string, []string) error
	}{
		{"shutdown", "stop", "stopping", PowerShutdown},
		{"cut", "kill", "killing", PowerCut},
		{"freeze", "pause", "pausing", PowerFreeze},
		{"unfreeze", "unpause", "unpausing", PowerUnfreeze},
	} {
		t.Run(op.name, func(t *testing.T) {
			run := func(onCall func([]string, string) mock.Result, nodes ...string) (*mock.Executor, error) {
				m := &mock.Executor{OnCall: onCall}
				return m, op.run(t.Context(), docker.NewClient(m), mesh.DefaultRealm, "dev", nodes)
			}
			opResult := func(r mock.Result) func([]string, string) mock.Result {
				return func(args []string, _ string) mock.Result {
					switch args[0] {
					case "ps":
						return mock.Result{Stdout: powerContainers()}
					case op.docker:
						return r
					}
					return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
				}
			}

			m, err := run(opResult(mock.Result{}), "worker-0", "controller")
			require.NoError(t, err)
			var done []string
			for _, c := range m.Calls {
				if c.Args[0] == op.docker {
					done = append(done, c.Args[1])
				}
			}
			assert.ElementsMatch(t, []string{"sind-dev-worker-0", "sind-dev-controller"}, done)

			m, err = run(opResult(mock.Result{}), "worker-0", "worker-0")
			require.NoError(t, err)
			var twice int
			for _, c := range m.Calls {
				if c.Args[0] == op.docker {
					twice++
				}
			}
			assert.Equal(t, 1, twice, "a node named twice is acted on once")

			_, err = run(opResult(mock.Result{Err: fmt.Errorf("docker failed")}), "worker-0")
			assert.EqualError(t, err, op.verb+" sind-dev-worker-0: docker failed")

			_, err = run(listErrorMock().OnCall, "worker-0")
			assert.ErrorContains(t, err, "listing")

			m, err = run(nil)
			require.NoError(t, err)
			assert.Empty(t, m.Calls)
		})
	}
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
		case args[0] == "inspect" && (args[1] == "sind-dns" || args[1] == "sind-ssh" || isStackContainer(args[1])):
			return mock.Result{Stderr: testutil.NoSuchContainer(args[1]), Err: testutil.ExitCode1(t)}
		case args[0] == "stop", args[0] == "kill", args[0] == "start":
			return mock.Result{}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

// powerKnownHosts is the relay's known_hosts in the power tests, with the
// keys of every node of cluster dev.
const powerKnownHosts = "controller.dev.sind.sind ssh-ed25519 A\nworker-0.dev.sind.sind ssh-ed25519 B\nworker-1.dev.sind.sind ssh-ed25519 C\n"

// meshCall answers the mesh's calls for a running mesh whose DNS container
// has 10.0.9.2, and whose Corefile has corefile: the inspects, the Corefile
// read and write, the reload, and the read of known_hosts, which has
// powerKnownHosts. The written Corefile goes to written.
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
		case strings.Join(args, " ") == "exec sind-ssh cat /root/.ssh/known_hosts":
			return mock.Result{Stdout: powerKnownHosts}, true
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

// TestPower_On_MissingHostKeys covers started nodes whose host keys the
// relay's known_hosts lacks, as a write of an earlier sind cut short leaves
// it: their keys are read from the nodes and added. A node whose key
// cannot be read is a warning, and does not keep the other out.
func TestPower_On_MissingHostKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		worker1 mock.Result
		want    string
		warning string
	}{
		"both": {
			worker1: mock.Result{Stdout: "ssh-ed25519 KEY1 root@worker-1\n"},
			want:    "controller.dev.sind.sind ssh-ed25519 A\nworker-0.dev.sind.sind ssh-ed25519 KEY0\nworker-1.dev.sind.sind ssh-ed25519 KEY1\n",
		},
		"one unreadable": {
			worker1: mock.Result{Stderr: "cat: no such file\n", Err: testutil.ExitCode1(t)},
			want:    "controller.dev.sind.sind ssh-ed25519 A\nworker-0.dev.sind.sind ssh-ed25519 KEY0\n",
			warning: "not adding the host keys that known_hosts lacks: reading the host key of sind-dev-worker-1: ",
		},
		"one not ed25519": {
			worker1: mock.Result{Stdout: "ssh-rsa AAAA root@worker-1\n"},
			want:    "controller.dev.sind.sind ssh-ed25519 A\nworker-0.dev.sind.sind ssh-ed25519 KEY0\n",
			warning: "reading the host key of sind-dev-worker-1: no ed25519 host key found",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var written, hosts string
			var m mock.Executor
			m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
				switch strings.Join(args, " ") {
				case "inspect sind-dev-worker-0 sind-dev-worker-1 --type container":
					return mock.Result{Stdout: startedNodesJSON("10.0.9.2")}, true
				case "exec sind-ssh cat /root/.ssh/known_hosts":
					return mock.Result{Stdout: "controller.dev.sind.sind ssh-ed25519 A\n"}, true
				case "exec sind-dev-worker-0 cat /etc/ssh/ssh_host_ed25519_key.pub":
					return mock.Result{Stdout: "ssh-ed25519 KEY0 root@worker-0\n"}, true
				case "exec sind-dev-worker-1 cat /etc/ssh/ssh_host_ed25519_key.pub":
					return tc.worker1, true
				}
				if args[0] == "exec" && args[1] == "-i" && args[2] == "sind-ssh" {
					hosts = stdin
					return mock.Result{}, true
				}
				return meshCall(t, nil, &written)(args, stdin)
			})
			client := docker.NewClient(&m)
			mgr := mesh.NewManager(client, mesh.DefaultRealm)
			var warned []string
			mgr.OnWarning = func(msg string) { warned = append(warned, msg) }

			require.NoError(t, PowerOn(t.Context(), client, mgr, "dev", []string{"worker-0", "worker-1"}))
			assert.Equal(t, tc.want, hosts)
			if tc.warning == "" {
				assert.Empty(t, warned)
			} else {
				require.Len(t, warned, 1)
				assert.Contains(t, warned[0], tc.warning)
			}
		})
	}
}

// TestPower_On_KnownHostsErrors covers a known_hosts that cannot be read or
// written: a warning, and the command succeeds.
func TestPower_On_KnownHostsErrors(t *testing.T) {
	for name, fail := range map[string]func(args []string) bool{
		"read":  func(a []string) bool { return strings.Join(a, " ") == "exec sind-ssh cat /root/.ssh/known_hosts" },
		"write": func(a []string) bool { return a[0] == "exec" && a[1] == "-i" && a[2] == "sind-ssh" },
	} {
		t.Run(name, func(t *testing.T) {
			var written string
			var m mock.Executor
			m.OnCall = powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
				switch {
				case fail(args):
					return mock.Result{Err: fmt.Errorf("relay gone")}, true
				case args[0] == "inspect" && args[1] == "sind-dev-worker-0":
					return mock.Result{Stdout: startedNodesJSON("10.0.9.2")}, true
				case strings.Join(args, " ") == "exec sind-ssh cat /root/.ssh/known_hosts":
					return mock.Result{}, true
				case strings.HasSuffix(strings.Join(args, " "), "cat /etc/ssh/ssh_host_ed25519_key.pub"):
					return mock.Result{Stdout: "ssh-ed25519 KEY root@node\n"}, true
				}
				return meshCall(t, nil, &written)(args, stdin)
			})
			client := docker.NewClient(&m)
			mgr := mesh.NewManager(client, mesh.DefaultRealm)
			var warned []string
			mgr.OnWarning = func(msg string) { warned = append(warned, msg) }

			require.NoError(t, PowerOn(t.Context(), client, mgr, "dev", []string{"worker-0"}))
			require.Len(t, warned, 1)
			assert.Contains(t, warned[0], "not adding the host keys that known_hosts lacks: ")
			assert.Contains(t, warned[0], "relay gone")
		})
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
			require.ErrorContains(t, err, tt.match)
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

	require.ErrorContains(t, err, "listing")
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

	require.ErrorContains(t, err, "starting sind-dev-worker-0")
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

	require.ErrorContains(t, err, "starting sind-dev-worker-0")
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

// TestPower_Restart covers PowerReboot and PowerCycle, which stop or kill
// their nodes and then start them.
func TestPower_Restart(t *testing.T) {
	for _, op := range []struct {
		name, docker, verb string
		run                func(context.Context, *docker.Client, *mesh.Manager, string, []string) error
	}{
		{"reboot", "stop", "stopping", PowerReboot},
		{"cycle", "kill", "killing", PowerCycle},
	} {
		t.Run(op.name, func(t *testing.T) {
			// run restarts nodes, with the calls for which fail is true failing.
			run := func(fail func(args []string) bool, nodes ...string) (*mock.Executor, error) {
				m := &mock.Executor{OnCall: powerOnCall(t, func(args []string, _ string) (mock.Result, bool) {
					return mock.Result{Err: errors.New(args[0] + " failed")}, fail(args)
				})}
				client := docker.NewClient(m)
				return m, op.run(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", nodes)
			}

			// Every node goes down before any comes back up.
			m, err := run(func([]string) bool { return false }, "worker-0", "worker-1")
			require.NoError(t, err)
			ops := powerOps(m)
			require.Len(t, ops, 4)
			assert.ElementsMatch(t, []string{op.docker + "=sind-dev-worker-0", op.docker + "=sind-dev-worker-1"}, ops[:2])
			assert.ElementsMatch(t, []string{"start=sind-dev-worker-0", "start=sind-dev-worker-1"}, ops[2:])

			client := docker.NewClient(listErrorMock())
			err = op.run(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})
			assert.ErrorContains(t, err, "listing")

			// A node that does not go down is not started, the other node
			// restarts, and the error names the failing node.
			m, err = run(func(args []string) bool { return args[0] == op.docker && args[1] == "sind-dev-worker-0" }, "worker-0", "worker-1")
			assert.ErrorContains(t, err, op.verb+" sind-dev-worker-0: "+op.docker+" failed")
			ops = powerOps(m)
			assert.Contains(t, ops, "start=sind-dev-worker-1")
			assert.NotContains(t, ops, "start=sind-dev-worker-0")

			// Nodes that all fail to go down: no start, and no mesh call.
			m, err = run(func(args []string) bool { return args[0] == op.docker }, "worker-0")
			require.Error(t, err)
			assert.Len(t, m.Calls, 2, "ps and "+op.docker)

			_, err = run(func(args []string) bool { return args[0] == "start" }, "worker-0")
			assert.ErrorContains(t, err, "starting")
		})
	}
}

// TestPower_KillOffNode checks that cut and cycle count a node that does
// not run as killed, as shutdown and reboot count it stopped: cycle starts
// it, and neither fails.
func TestPower_KillOffNode(t *testing.T) {
	notRunning := func(args []string, _ string) (mock.Result, bool) {
		return mock.Result{Stderr: "Error response from daemon: cannot kill container: " + args[1] + ": container c2 is not running\n",
			Err: testutil.ExitCode1(t)}, args[0] == "kill" && args[1] == "sind-dev-worker-0"
	}

	m := &mock.Executor{OnCall: powerOnCall(t, notRunning)}
	client := docker.NewClient(m)
	require.NoError(t, PowerCycle(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"}))
	assert.ElementsMatch(t, []string{"start=sind-dev-worker-0", "start=sind-dev-worker-1"}, powerOps(m)[2:])

	m = &mock.Executor{OnCall: powerOnCall(t, notRunning)}
	require.NoError(t, PowerCut(t.Context(), docker.NewClient(m), mesh.DefaultRealm, "dev", []string{"worker-0"}))
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
