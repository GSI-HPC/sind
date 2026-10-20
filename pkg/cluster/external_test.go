// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

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
}

type externalContainer struct {
	labels   map[string]string
	networks []string
}

func newExternalFake(t *testing.T) (*externalFake, *docker.Client, *mock.Executor) {
	f := &externalFake{
		t:        t,
		networks: []string{"sind-mesh", "sind-dev-net", "sind-lock", "sind-ci-net"},
		containers: map[string]externalContainer{
			"kind-control-plane": {networks: []string{"kind"}},
			"sind-dev-controller": {labels: map[string]string{LabelRealm: "sind"},
				networks: []string{"sind-dev-net", "sind-mesh"}},
			"sind-prometheus": {labels: map[string]string{docker.ComposeProjectLabel: "sind-mesh"},
				networks: []string{"sind-mesh"}},
		},
		fail: map[string]mock.Result{},
	}
	m := &mock.Executor{OnCall: f.onCall}
	return f, docker.NewClient(m), m
}

func (f *externalFake) onCall(args []string, _ string) mock.Result {
	joined := strings.Join(args, " ")
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
		net := strings.TrimPrefix(args[len(args)-1], "network=")
		var entries []testutil.PsEntry
		for name, c := range f.containers {
			if !slices.Contains(c.networks, net) {
				continue
			}
			var labels []string
			for k, v := range c.labels {
				labels = append(labels, k+"="+v)
			}
			entries = append(entries, testutil.PsEntry{ID: name, Names: name, State: "running", Image: "img", Labels: strings.Join(labels, ",")})
		}
		return mock.Result{Stdout: testutil.NDJSON(entries...)}
	case args[0] == "inspect":
		var docs []map[string]any
		for _, name := range args[1:] {
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
		var kept []string
		for _, n := range c.networks {
			if n != args[2] {
				kept = append(kept, n)
			}
		}
		c.networks = kept
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
	for _, tt := range []struct{ fail, match string }{
		{"ps", "not connecting external containers"},
		{"network connect", "connecting an external container"},
	} {
		t.Run(tt.fail, func(t *testing.T) {
			f, c, _ := newExternalFake(t)
			f.containers["kind-control-plane"] = externalContainer{networks: []string{"sind-mesh"}}
			f.fail[tt.fail] = mock.Result{Err: fmt.Errorf("daemon unreachable")}
			var logs bytes.Buffer
			ctx := sindlog.With(t.Context(), slog.New(slog.NewTextHandler(&logs, nil)))

			ConnectExternal(ctx, c, mesh.NewManager(c, "sind"), "sind-new-net")

			assert.Contains(t, logs.String(), "level=WARN")
			assert.Contains(t, logs.String(), tt.match)
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
