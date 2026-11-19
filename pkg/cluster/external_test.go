// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// externalFake is a docker stand-in for the external container tests: the
// realm's networks, and containers with their labels and the networks they
// join. Calls whose joined arguments start with a key of fail get that
// result.
type externalFake struct {
	t          *testing.T
	networks   []string
	containers map[string]externalContainer
	fail       map[string]mock.Result
	// onConnect, when set, is called as a docker network connect starts.
	onConnect func()
}

type externalContainer struct {
	labels   map[string]string
	networks []string
}

// meshLabels returns the labels of a container of realm's mesh project.
func meshLabels(realm, service string) map[string]string {
	return docker.ComposeLabels(realm+"-mesh", service, 1)
}

func newExternalFake(t *testing.T) (*externalFake, *docker.Client, *mock.Executor) {
	f := &externalFake{
		t:        t,
		networks: []string{"sind-mesh", "sind-dev-net", "sind-lock", "sind-ci-net"},
		containers: map[string]externalContainer{
			"kind-control-plane": {networks: []string{"kind"}},
			"sind-dev-controller": {labels: map[string]string{LabelRealm: "sind"},
				networks: []string{"sind-dev-net", "sind-mesh"}},
			"sind-prometheus": {labels: meshLabels("sind", "prometheus"), networks: []string{"sind-mesh"}},
		},
		fail: map[string]mock.Result{},
	}
	m := &mock.Executor{OnCall: f.onCall}
	return f, docker.NewClient(m), m
}

func (f *externalFake) onCall(args []string, _ string) mock.Result {
	joined := strings.Join(args, " ")
	if f.onConnect != nil && strings.HasPrefix(joined, "network connect") {
		f.onConnect()
	}
	for prefix, r := range f.fail {
		if strings.HasPrefix(joined, prefix) {
			return r
		}
	}
	switch {
	case args[0] == "network" && args[1] == "ls":
		var lines []string
		for _, n := range f.networks {
			lines = append(lines, `{"Name":"`+n+`","Driver":"bridge","Labels":"sind.realm=sind"}`)
		}
		return mock.Result{Stdout: strings.Join(lines, "\n") + "\n"}
	case args[0] == "ps":
		// Without a network filter, ps lists every container.
		net, filtered := strings.CutPrefix(args[len(args)-1], "network=")
		var entries []testutil.PsEntry
		for name, c := range f.containers {
			if filtered && !slices.Contains(c.networks, net) {
				continue
			}
			var labels []string
			for k, v := range c.labels {
				labels = append(labels, k+"="+v)
			}
			entries = append(entries, testutil.PsEntry{ID: name, Names: name, State: "running", Image: "img",
				Labels: strings.Join(labels, ","), Networks: strings.Join(c.networks, ",")})
		}
		return mock.Result{Stdout: testutil.NDJSON(entries...)}
	case args[0] == "inspect":
		var docs []map[string]any
		for _, name := range args[1 : len(args)-2] {
			c, ok := f.containers[name]
			if !ok {
				return mock.Result{Stderr: testutil.NoSuchContainer(name), Err: testutil.ExitCode1(f.t)}
			}
			nets := map[string]map[string]string{}
			for _, n := range c.networks {
				nets[n] = map[string]string{"IPAddress": "10.0.0.9"}
			}
			docs = append(docs, map[string]any{
				"Id": name, "Name": "/" + name,
				"State":           map[string]any{"Status": "running"},
				"Config":          map[string]any{"Labels": c.labels},
				"NetworkSettings": map[string]any{"Networks": nets},
			})
		}
		out, _ := json.Marshal(docs)
		return mock.Result{Stdout: string(out)}
	case args[0] == "network" && args[1] == "connect":
		c := f.containers[args[3]]
		c.networks = append(c.networks, args[2])
		f.containers[args[3]] = c
		return mock.Result{}
	case args[0] == "network" && args[1] == "disconnect":
		c := f.containers[args[3]]
		c.networks = slices.DeleteFunc(c.networks, func(n string) bool { return n == args[2] })
		f.containers[args[3]] = c
		return mock.Result{}
	}
	f.t.Errorf("unexpected docker call: %s", joined)
	return mock.Result{Err: fmt.Errorf("unexpected call: %s", joined)}
}

func TestExternalContainers(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind", "sind-mesh"}}
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind", "sind-mesh"}}

	names, err := ExternalContainers(t.Context(), c, mesh.NewManager(c, "sind"))

	require.NoError(t, err)
	assert.Equal(t, []docker.ContainerName{"kind-control-plane", "kind-worker"}, names,
		"the nodes and the monitoring stack are sind's")
}

func TestExternalContainers_Error(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.fail["ps"] = mock.Result{Err: fmt.Errorf("daemon unreachable")}

	_, err := ExternalContainers(t.Context(), c, mesh.NewManager(c, "sind"))

	require.ErrorContains(t, err, "listing containers on the mesh")
}

func TestConnectableContainers(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind", "sind-mesh"}}
	f.containers["db"] = externalContainer{networks: []string{"bridge"}}
	f.containers["ci-dev-controller"] = externalContainer{labels: map[string]string{LabelRealm: "ci"}, networks: []string{"ci-dev-net"}}
	for _, service := range []string{"dns", "ssh", "grafana", "prometheus", "loki", "alloy"} {
		f.containers["ci-"+service] = externalContainer{labels: meshLabels("ci", service), networks: []string{"ci-mesh"}}
	}
	f.containers["web"] = externalContainer{labels: meshLabels("service", "web"), networks: []string{"service-mesh"}}
	// A user's Compose project service-mesh with a prometheus service: its
	// container has Compose's name, not the one sind gives realm service's.
	f.containers["service-mesh-prometheus-1"] = externalContainer{labels: meshLabels("service", "prometheus"), networks: []string{"service-mesh"}}

	names, err := ConnectableContainers(t.Context(), c, mesh.NewManager(c, "sind"))

	require.NoError(t, err)
	assert.Equal(t, []docker.ContainerName{"db", "kind-control-plane", "service-mesh-prometheus-1", "web"}, names,
		"kind-worker joins the mesh already, and sind's containers of any realm are left out")
}

func TestConnectable(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["db"] = externalContainer{networks: []string{"bridge"}}

	entries, err := Connectable(t.Context(), c, mesh.NewManager(c, "sind"))

	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, docker.ContainerName("db"), entries[0].Name)
	assert.Equal(t, []docker.NetworkName{"bridge"}, entries[0].Networks)
	assert.Equal(t, "img", entries[0].Image)
	assert.Equal(t, docker.ContainerName("kind-control-plane"), entries[1].Name)
}

func TestConnectableContainers_Error(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.fail["ps"] = mock.Result{Err: fmt.Errorf("daemon unreachable")}

	_, err := ConnectableContainers(t.Context(), c, mesh.NewManager(c, "sind"))

	require.EqualError(t, err, "listing containers: daemon unreachable")
}

func TestConnect(t *testing.T) {
	f, c, m := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind", "sind-dev-net"}}

	err := Connect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-control-plane")

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network connect sind-mesh kind-control-plane",
		"network connect sind-ci-net kind-control-plane",
	}, callsWithPrefix(m, "network connect"), "the mesh first, the lock left out, joined networks skipped")
}

// TestConnect_OtherRealmsMesh checks that Connect refuses the mesh and
// monitoring stack containers of another realm, which carry no realm label.
func TestConnect_OtherRealmsMesh(t *testing.T) {
	f, c, m := newExternalFake(t)
	f.containers["ci-ssh"] = externalContainer{labels: meshLabels("ci", "ssh"), networks: []string{"ci-mesh"}}
	f.containers["ci-grafana"] = externalContainer{labels: meshLabels("ci", "grafana"), networks: []string{"ci-mesh"}}

	for _, name := range []docker.ContainerName{"ci-ssh", "ci-grafana"} {
		err := Connect(t.Context(), c, mesh.NewManager(c, "sind"), name)
		require.EqualError(t, err, string(name)+" is a sind container")
	}
	assert.Empty(t, callsWithPrefix(m, "network connect"))
}

func TestMeshContainer(t *testing.T) {
	for _, tt := range []struct {
		name   docker.ContainerName
		labels map[string]string
		want   bool
	}{
		{"sind-dns", meshLabels("sind", "dns"), true},
		{"ci-2-ssh", meshLabels("ci-2", "ssh"), true},
		{"ci-loki", meshLabels("ci", "loki"), true},
		{"ci-web", meshLabels("ci", "web"), false},                                // another service of a project of that name
		{"service-mesh-prometheus-1", meshLabels("service", "prometheus"), false}, // Compose's name, not sind's
		{"ci-ssh", meshLabels("ci", "dns"), false},                                // the name of another service
		{"ci-2-dns", meshLabels("ci", "dns"), false},                              // the name of another realm's
		{"CI-dns", meshLabels("CI", "dns"), false},                                // no realm's project
		{"-dns", meshLabels("", "dns"), false},                                    // a project named "-mesh"
		{"ci-dev-dns", docker.ComposeLabels("ci-dev", "dns", 1), false},           // a cluster's project
		{"dns", map[string]string{docker.ComposeServiceLabel: "dns"}, false},
		{"sind-dns", nil, false},
	} {
		assert.Equal(t, tt.want, meshContainer(tt.name, tt.labels), "%s %v", tt.name, tt.labels)
	}
}

func TestConnect_NoMesh(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.networks = []string{"sind-dev-net"}

	err := Connect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-control-plane")

	require.ErrorContains(t, err, `no mesh found for realm "sind"`)
}

func TestConnect_NoNetworks(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.networks = nil

	err := Connect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-control-plane")

	require.ErrorContains(t, err, `no mesh found for realm "sind"`)
}

func TestConnect_Errors(t *testing.T) {
	for _, tt := range []struct {
		name, container, fail, match string
	}{
		{"list networks", "kind-control-plane", "network ls", "listing networks"},
		{"inspect", "kind-control-plane", "inspect", "inspecting containers"},
		{"missing container", "kind-gone", "", `container "kind-gone" not found`},
		{"sind node", "sind-dev-controller", "", "sind-dev-controller is a sind container"},
		{"monitoring container", "sind-prometheus", "", "sind-prometheus is a sind container"},
		{"connect", "kind-control-plane", "network connect", "connecting kind-control-plane to sind-mesh"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, c, _ := newExternalFake(t)
			if tt.fail != "" {
				f.fail[tt.fail] = mock.Result{Err: fmt.Errorf("daemon unreachable")}
			}

			err := Connect(t.Context(), c, mesh.NewManager(c, "sind"), docker.ContainerName(tt.container))

			require.ErrorContains(t, err, tt.match)
		})
	}
}

// TestDisconnect_MissingContainer checks that the error names the container
// that is missing among several, in sind's words rather than docker's.
func TestDisconnect_MissingContainer(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind"}}

	err := Disconnect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-worker", "kind-gone")

	require.EqualError(t, err, `container "kind-gone" not found`)
}

// TestDisconnect_MissingContainerGoneMeanwhile checks that the inspect error
// stands when every container exists on a second look.
func TestDisconnect_MissingContainerGoneMeanwhile(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind"}}
	f.fail["inspect kind-worker kind-control-plane"] = mock.Result{Stderr: testutil.NoSuchContainer("kind-worker"), Err: testutil.ExitCode1(t)}

	err := Disconnect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-worker", "kind-control-plane")

	require.ErrorContains(t, err, "inspecting containers")
}

func TestDisconnect(t *testing.T) {
	f, c, m := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind", "sind-mesh", "sind-dev-net"}}
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind"}}

	err := Disconnect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-control-plane", "kind-worker")

	require.NoError(t, err)
	assert.Equal(t, []string{
		"network disconnect sind-dev-net kind-control-plane",
		"network disconnect sind-mesh kind-control-plane",
	}, callsWithPrefix(m, "network disconnect"), "the mesh last, other networks left alone")
	assert.Equal(t, []string{"kind"}, f.containers["kind-control-plane"].networks)
}

func TestDisconnect_AlreadyGone(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"sind-mesh"}}
	f.fail["network disconnect"] = mock.Result{Stderr: testutil.NoSuchNetwork("sind-mesh"), Err: testutil.ExitCode1(t)}

	err := Disconnect(t.Context(), c, mesh.NewManager(c, "sind"), "kind-control-plane")

	require.NoError(t, err)
}

func TestDisconnect_Errors(t *testing.T) {
	for _, tt := range []struct {
		name, container, fail, match string
	}{
		{"list networks", "kind-control-plane", "network ls", "listing networks"},
		{"sind node", "sind-dev-controller", "", "sind-dev-controller is a sind container"},
		{"disconnect", "kind-control-plane", "network disconnect", "disconnecting kind-control-plane from sind-mesh"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, c, _ := newExternalFake(t)
			f.containers["kind-control-plane"] = externalContainer{networks: []string{"sind-mesh"}}
			if tt.fail != "" {
				f.fail[tt.fail] = mock.Result{Err: fmt.Errorf("daemon unreachable")}
			}

			err := Disconnect(t.Context(), c, mesh.NewManager(c, "sind"), docker.ContainerName(tt.container))

			require.ErrorContains(t, err, tt.match)
		})
	}
}

func TestConnectExternal(t *testing.T) {
	f, c, m := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind", "sind-mesh"}}

	ConnectExternal(t.Context(), c, mesh.NewManager(c, "sind"), "sind-new-net")

	assert.Equal(t, []string{"network connect sind-new-net kind-control-plane"}, callsWithPrefix(m, "network connect"))
}

func TestConnectExternal_Warnings(t *testing.T) {
	for _, tt := range []struct{ fail, want string }{
		{"ps", "not connecting external containers to sind-new-net: listing containers on the mesh: daemon unreachable"},
		{"network connect", "not connecting external container kind-control-plane to sind-new-net: daemon unreachable"},
	} {
		t.Run(tt.fail, func(t *testing.T) {
			f, c, _ := newExternalFake(t)
			f.containers["kind-control-plane"] = externalContainer{networks: []string{"sind-mesh"}}
			f.fail[tt.fail] = mock.Result{Err: fmt.Errorf("daemon unreachable")}
			var warned []string
			ctx := sindlog.WithWarn(t.Context(), func(msg string) { warned = append(warned, msg) })

			ConnectExternal(ctx, c, mesh.NewManager(c, "sind"), "sind-new-net")

			require.Len(t, warned, 1)
			assert.Contains(t, warned[0], tt.want)
		})
	}
}

// callsWithPrefix returns the joined arguments of m's calls that start with
// prefix.
func callsWithPrefix(m *mock.Executor, prefix string) []string {
	var out []string
	for _, c := range m.Calls {
		if joined := strings.Join(c.Args, " "); strings.HasPrefix(joined, prefix) {
			out = append(out, joined)
		}
	}
	return out
}

// Connecting is the step "connecting", with a target for each container
// announced queued before the first runs; one that joins the mesh and every
// cluster network already is skipped, as the step "disconnecting" skips one
// that joins none.
func TestConnectAndDisconnect_ReportTheContainers(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind"}}
	f.containers["kind-worker"] = externalContainer{networks: []string{"sind-mesh", "sind-dev-net", "sind-ci-net"}}
	meshMgr := mesh.NewManager(c, "sind")
	names := []docker.ContainerName{"kind-control-plane", "kind-worker"}

	ctx, w := progresstest.Watch(t.Context(), t)
	require.NoError(t, Connect(ctx, c, meshMgr, names...))
	w.Finish()
	assert.Equal(t, `step connecting total=2 [fold]: ok
  target kind-control-plane: ok
  target kind-worker: skipped: connected
`, testutil.WithoutCalls(w.Events()))

	ctx, w = progresstest.Watch(t.Context(), t)
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind"}}
	require.NoError(t, Disconnect(ctx, c, meshMgr, names...))
	w.Finish()
	assert.Equal(t, `step disconnecting total=2 [fold]: ok
  target kind-control-plane: ok
  target kind-worker: skipped: not connected
`, testutil.WithoutCalls(w.Events()))
}

// The first container that fails ends the step with its error; the
// targets of the containers not tried end canceled with it, and the one
// that failed fails.
func TestConnectAndDisconnect_ReportAFailure(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind", "sind-mesh"}}
	f.containers["kind-worker"] = externalContainer{networks: []string{"kind", "sind-mesh"}}
	f.fail["network connect"] = mock.Result{Err: fmt.Errorf("daemon unreachable")}
	f.fail["network disconnect"] = mock.Result{Err: fmt.Errorf("daemon unreachable")}
	meshMgr := mesh.NewManager(c, "sind")
	names := []docker.ContainerName{"kind-control-plane", "kind-worker"}

	ctx, w := progresstest.Watch(t.Context(), t)
	require.Error(t, Connect(ctx, c, meshMgr, names...))
	w.Finish()
	assert.Equal(t, `step connecting total=2 [fold]: failed (target): connecting kind-control-plane to sind-ci-net: daemon unreachable
  target kind-control-plane: failed (target): connecting {} to sind-ci-net: daemon unreachable
  target kind-worker: canceled (canceled): not finished
`, testutil.WithoutCalls(w.Events()))

	ctx, w = progresstest.Watch(t.Context(), t)
	require.Error(t, Disconnect(ctx, c, meshMgr, names...))
	w.Finish()
	assert.Equal(t, `step disconnecting total=2 [fold]: failed (target): disconnecting kind-control-plane from sind-mesh: daemon unreachable
  target kind-control-plane: failed (target): disconnecting {} from sind-mesh: daemon unreachable
  target kind-worker: canceled (canceled): not finished
`, testutil.WithoutCalls(w.Events()))
}

// A container whose work the end of the context stopped ends canceled, as
// the other steps' targets do.
func TestConnect_Interrupted(t *testing.T) {
	f, c, _ := newExternalFake(t)
	f.containers["kind-control-plane"] = externalContainer{networks: []string{"kind"}}
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, cancel := context.WithCancel(ctx)
	f.fail["network connect"] = mock.Result{Err: fmt.Errorf("signal: killed")}
	f.onConnect = cancel

	require.Error(t, Connect(ctx, c, mesh.NewManager(c, "sind"), "kind-control-plane"))
	w.Finish()
	assert.Equal(t, `step connecting total=1 [fold]: canceled (canceled): connecting kind-control-plane to sind-mesh: signal: killed
  target kind-control-plane: canceled (canceled): connecting {} to sind-mesh: signal: killed
`, testutil.WithoutCalls(w.Events()))
}
