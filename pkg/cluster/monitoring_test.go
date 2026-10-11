// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isStackContainer reports whether name is a container of realm sind's
// monitoring stack.
func isStackContainer(name string) bool {
	return slices.ContainsFunc(observability.Services, func(s observability.Service) bool {
		return string(observability.ContainerName("sind", s)) == name
	})
}

// probePlugins is what the image probe prints after the daemons for an
// image that can serve monitoring.
const probePlugins = `plugindir /usr/lib64/slurm
plugin http_parser_llhttp_parser.so
plugin url_parser_internal.so
plugin tls_none.so
plugin http_auth_local.so
plugin data_parser_v0_0_44.so
plugin metrics_openmetrics.so
`

// stackContainer is a container of stackFake.
type stackContainer struct {
	labels map[string]string
	state  docker.ContainerState
	ip     string
}

// stackFake stands in for the docker calls of realm sind's monitoring
// stack: its containers and volumes, the journal volume, the mesh
// network's labels, the daemon's cgroup driver, the image probe, the
// journal helper and slurmctld's HTTP endpoints. nodes are the monitored
// node containers docker ps lists for the journal volume. Calls whose
// joined arguments start with a key of fail get that result.
type stackFake struct {
	t          *testing.T
	mu         sync.Mutex
	containers map[string]*stackContainer
	volumes    map[string]bool
	nodes      []docker.ContainerListEntry
	dnsIP      string
	version    string // of the probed images' Slurm
	logError   string // the probed images' MariaDB log-error option
	fail       map[string]mock.Result
	calls      [][]string
}

// newStackFake returns a stackFake without a stack, on a mesh with a
// pinned DNS address, whose images serve monitoring.
func newStackFake(t *testing.T) *stackFake {
	return &stackFake{
		t:          t,
		containers: map[string]*stackContainer{},
		volumes:    map[string]bool{},
		dnsIP:      "172.30.255.254",
		version:    "26.05.4",
		logError:   "--skip-log-error",
		fail:       map[string]mock.Result{},
	}
}

// client returns a client whose calls stackFake must handle all of.
func (f *stackFake) client() (*docker.Client, *mock.Executor) {
	m := &mock.Executor{OnCall: func(args []string, stdin string) mock.Result {
		r, ok := f.handle(args, stdin)
		if !ok {
			r, ok = f.dns(args)
		}
		if !ok {
			f.t.Errorf("stack fake: unexpected call %v", args)
			return mock.Result{Err: testutil.ExitCode1(f.t)}
		}
		return r
	}}
	return docker.NewClient(m), m
}

// dns answers the mesh DNS container's calls that change the Corefile:
// it starts out empty and is running.
func (f *stackFake) dns(args []string) (mock.Result, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(args, " ")
	for prefix, r := range f.fail {
		if strings.HasPrefix(joined, prefix) && strings.Contains(joined, "sind-dns") {
			f.calls = append(f.calls, args)
			return r, true
		}
	}
	switch {
	case args[0] == "cp" && args[1] == "sind-dns:/Corefile":
		return mock.Result{Stdout: emptyCorefileTar()}, true
	case joined == "cp - sind-dns:/", joined == "kill -s USR1 sind-dns":
		f.calls = append(f.calls, args)
		return mock.Result{}, true
	case joined == "inspect sind-dns":
		return mock.Result{Stdout: inspectJSON(f.t, "sind-dns", "running", nil)}, true
	}
	return mock.Result{}, false
}

// withStack gives the fake a running stack of the current fingerprint.
func (f *stackFake) withStack() *stackFake {
	fp := observability.NewManager(nil, "sind").Fingerprint()
	for i, s := range observability.Services {
		f.containers[string(observability.ContainerName("sind", s))] = &stackContainer{
			labels: map[string]string{
				docker.ComposeProjectLabel: "sind-mesh",
				observability.LabelVersion: fp,
			},
			state: docker.StateRunning,
			ip:    "172.30.255." + string(rune('0'+i)),
		}
	}
	for _, s := range observability.Services {
		f.volumes[string(observability.VolumeName("sind", s))] = true
	}
	f.volumes[string(observability.JournalVolume("sind"))] = true
	return f
}

// stopped stops the fake's stack containers.
func (f *stackFake) stopped() *stackFake {
	for _, c := range f.containers {
		c.state = docker.StateExited
	}
	return f
}

// called reports whether a call starting with prefix was made.
func (f *stackFake) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.ContainsFunc(f.calls, func(c []string) bool {
		return strings.HasPrefix(strings.Join(c, " "), prefix)
	})
}

// isStackVolume reports whether name is a volume of the stack or the
// journal volume.
func isStackVolume(name string) bool {
	if name == string(observability.JournalVolume("sind")) {
		return true
	}
	return slices.ContainsFunc(observability.Services, func(s observability.Service) bool {
		return string(observability.VolumeName("sind", s)) == name
	})
}

// handle answers a call of the stack, or reports that it is none.
func (f *stackFake) handle(args []string, _ string) (mock.Result, bool) { //nolint:gocyclo // a dispatcher over docker calls
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(args, " ")
	notFound := func(stderr string) mock.Result { return mock.Result{Stderr: stderr, Err: testutil.ExitCode1(f.t)} }
	mine := func() bool {
		switch args[0] {
		case "inspect":
			return slices.ContainsFunc(args[1:], isStackContainer)
		case "create":
			name, _ := testutil.ArgValue(args, "--name")
			return isStackContainer(name)
		case "start", "rm", "exec":
			return isStackContainer(args[len(args)-1]) || len(args) > 1 && isStackContainer(args[1]) ||
				args[0] == "exec" && len(args) > 2 && args[len(args)-2] == probe.SlurmctldPort
		case "cp":
			name, _, _ := strings.Cut(args[len(args)-1], ":")
			return isStackContainer(name)
		case "container":
			return isStackContainer(args[len(args)-1])
		case "network":
			return joined == "network inspect sind-mesh --format {{json .Labels}}"
		case "info":
			return true
		case "run":
			name, _ := testutil.ArgValue(args, "--name")
			return strings.HasSuffix(name, "-journal-helper") || strings.Contains(joined, "plugindir")
		case "ps":
			return slices.ContainsFunc(testutil.ArgValues(args, "--filter"), func(v string) bool {
				return v == "label="+observability.LabelVersion || strings.HasPrefix(v, "volume=")
			})
		case "volume":
			if args[1] == "ls" {
				return slices.Contains(testutil.ArgValues(args, "--filter"), "label="+docker.ComposeProjectLabel+"=sind-mesh")
			}
			return isStackVolume(args[len(args)-1])
		}
		return false
	}
	if !mine() {
		return mock.Result{}, false
	}
	f.calls = append(f.calls, args)
	for prefix, r := range f.fail {
		if strings.HasPrefix(joined, prefix) {
			return r, true
		}
	}
	switch args[0] {
	case "inspect":
		var out []map[string]any
		for _, name := range args[1:] {
			c, ok := f.containers[name]
			if !ok {
				return notFound(testutil.NoSuchContainer(name)), true
			}
			networks := map[string]any{}
			if c.state == docker.StateRunning {
				networks["sind-mesh"] = map[string]string{"IPAddress": c.ip}
			}
			out = append(out, map[string]any{
				"Id": "id-" + name, "Name": "/" + name,
				"State":           map[string]any{"Status": c.state},
				"Config":          map[string]any{"Labels": c.labels},
				"NetworkSettings": map[string]any{"Networks": networks},
			})
		}
		b, _ := json.Marshal(out)
		return mock.Result{Stdout: string(b)}, true
	case "container":
		if _, ok := f.containers[args[len(args)-1]]; !ok {
			return notFound(testutil.NoSuchContainer(args[len(args)-1])), true
		}
		return mock.Result{Stdout: "[{}]"}, true
	case "create":
		name, _ := testutil.ArgValue(args, "--name")
		labels := map[string]string{}
		for _, l := range testutil.ArgValues(args, "--label") {
			k, v, _ := strings.Cut(l, "=")
			labels[k] = v
		}
		ip, _ := testutil.ArgValue(args, "--ip")
		if ip == "" {
			ip = "172.30.0.9"
		}
		f.containers[name] = &stackContainer{labels: labels, state: docker.StateCreated, ip: ip}
		return mock.Result{Stdout: "id-" + name + "\n"}, true
	case "start":
		f.containers[args[1]].state = docker.StateRunning
		return mock.Result{}, true
	case "rm":
		name := args[len(args)-1]
		if _, ok := f.containers[name]; !ok {
			return notFound(testutil.NoSuchContainer(name)), true
		}
		delete(f.containers, name)
		return mock.Result{}, true
	case "exec":
		if args[len(args)-2] == probe.SlurmctldPort {
			return mock.Result{Stdout: "HTTP/1.1 200 OK\r\n"}, true
		}
		if c := f.containers[args[1]]; c == nil || c.state != docker.StateRunning {
			return notFound("Error response from daemon: container " + args[1] + " is not running\n"), true
		}
		return mock.Result{}, true
	case "network":
		return mock.Result{Stdout: `{"` + mesh.LabelDNSIP + `":"` + f.dnsIP + `"}`}, true
	case "info":
		return mock.Result{Stdout: `{"ServerVersion":"28.1.1","CgroupDriver":"systemd"}`}, true
	case "run":
		if name, _ := testutil.ArgValue(args, "--name"); name != "" {
			return mock.Result{}, true
		}
		return mock.Result{Stdout: f.probeOutput(args)}, true
	case "ps":
		var entries []map[string]string
		if slices.Contains(testutil.ArgValues(args, "--filter"), "label="+observability.LabelVersion) {
			for name, c := range f.containers {
				entries = append(entries, map[string]string{"ID": "id-" + name, "Names": name, "State": string(c.state), "Labels": joinTestLabels(c.labels)})
			}
		} else {
			for _, n := range f.nodes {
				if matchesTestFilters(args, n.Labels) {
					entries = append(entries, map[string]string{"ID": string(n.ID), "Names": string(n.Name), "State": "running", "Labels": joinTestLabels(n.Labels)})
				}
			}
		}
		return mock.Result{Stdout: testutil.NDJSON(entries...)}, true
	case "cp":
		return mock.Result{}, true
	}
	// volume
	name := args[len(args)-1]
	switch args[1] {
	case "ls":
		var entries []map[string]string
		for v := range f.volumes {
			entries = append(entries, map[string]string{"Name": v})
		}
		return mock.Result{Stdout: testutil.NDJSON(entries...)}, true
	case "create":
		f.volumes[name] = true
		return mock.Result{}, true
	case "inspect":
		if !f.volumes[name] {
			return notFound(testutil.NoSuchVolume(name)), true
		}
		return mock.Result{Stdout: "[{}]"}, true
	case "rm":
		if !f.volumes[name] {
			return notFound(testutil.NoSuchVolume(name)), true
		}
		delete(f.volumes, name)
	}
	return mock.Result{}, true
}

// probeOutput is what the image probe run with args prints: the requested
// daemons of the fake's version, the plugins, and MariaDB's option when
// asked for.
func (f *stackFake) probeOutput(args []string) string {
	i := slices.Index(args, "-c")
	var b strings.Builder
	for _, d := range args[i+5:] {
		b.WriteString("daemon " + d + " slurm " + f.version + "\n")
	}
	b.WriteString(probePlugins)
	if args[i+4] == "1" {
		b.WriteString("mariadb " + f.logError + "\n")
	}
	return b.String()
}

// monitoredNode returns the docker ps entry of a monitored node of realm
// sind.
func monitoredNode(cluster, shortName string, role string) docker.ContainerListEntry {
	return docker.ContainerListEntry{
		ID:   docker.ContainerID("id-" + cluster + "-" + shortName),
		Name: ContainerName("sind", cluster, shortName),
		Labels: docker.Labels{
			LabelRealm: "sind", LabelCluster: cluster, LabelRole: role,
		},
	}
}

// matchesTestFilters reports whether labels match every label filter of a
// docker ps call: "label=key" or "label=key=value".
func matchesTestFilters(args []string, labels docker.Labels) bool {
	for _, filter := range testutil.ArgValues(args, "--filter") {
		spec, ok := strings.CutPrefix(filter, "label=")
		if !ok {
			continue
		}
		key, value, hasValue := strings.Cut(spec, "=")
		got, present := labels[key]
		if !present || hasValue && got != value {
			return false
		}
	}
	return true
}

// joinTestLabels joins labels as docker ps prints them.
func joinTestLabels(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// stackMesh returns a mesh manager of realm sind on client that collects
// its warnings.
func stackMesh(client *docker.Client) (*mesh.Manager, *[]string) {
	var warnings []string
	var mu sync.Mutex
	m := mesh.NewManager(client, "sind")
	m.OnWarning = func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, msg)
	}
	return m, &warnings
}

func TestMonitored(t *testing.T) {
	journal := &docker.ContainerInfo{Mounts: []docker.Mount{
		{Type: docker.MountVolume, Name: "sind-dev-config"},
		{Type: docker.MountVolume, Name: "sind-journal"},
	}}
	assert.True(t, Monitored("sind", journal))
	assert.False(t, Monitored("other", journal))
	bind := &docker.ContainerInfo{Mounts: []docker.Mount{{Type: docker.MountBind, Name: "sind-journal"}}}
	assert.False(t, Monitored("sind", bind))
}

func TestAlloyConfig(t *testing.T) {
	f := newStackFake(t)
	f.nodes = []docker.ContainerListEntry{monitoredNode("dev", "controller", "controller"), monitoredNode("dev", "worker-0", "worker")}
	client, m := f.client()

	conf, err := alloyConfig(t.Context(), client, "sind")
	require.NoError(t, err)
	assert.Contains(t, conf, "sind-dev-controller:6817")
	assert.Contains(t, conf, "sind-dev-worker-0:6818")
	assert.Contains(t, conf, "/system.slice/docker-id-dev-worker-0.scope")
	assert.True(t, slices.ContainsFunc(m.Calls, func(c mock.Call) bool {
		return slices.Equal(c.Args, []string{"ps", "-a", "--no-trunc", "--format", "json",
			"--filter", "label=sind.realm=sind", "--filter", "label=sind.role", "--filter", "volume=sind-journal"})
	}))
}

func TestAlloyConfig_Errors(t *testing.T) {
	for _, prefix := range []string{"info", "ps"} {
		t.Run(prefix, func(t *testing.T) {
			f := newStackFake(t)
			f.fail[prefix] = mock.Result{Err: testutil.ExitCode1(t)}
			client, _ := f.client()
			_, err := alloyConfig(t.Context(), client, "sind")
			require.Error(t, err)
		})
	}
}

func TestEnsureMonitoring_Creates(t *testing.T) {
	f := newStackFake(t)
	client, _ := f.client()
	meshMgr, _ := stackMesh(client)

	created, err := ensureMonitoring(t.Context(), client, meshMgr)
	require.NoError(t, err)
	assert.True(t, created)
	for _, s := range observability.Services {
		c := f.containers[string(observability.ContainerName("sind", s))]
		require.NotNil(t, c, s)
		assert.Equal(t, docker.StateRunning, c.state)
	}
	assert.Equal(t, mesh.ServiceIP(f.dnsIP, 1), f.containers["sind-grafana"].ip)
	assert.True(t, f.called("cp - sind-alloy:"))
	assert.True(t, f.called("exec sind-grafana curl -fsS --max-time 5 -X POST -o /dev/null http://sind-alloy:12345/-/reload"))
}

func TestEnsureMonitoring_Existing(t *testing.T) {
	f := newStackFake(t).withStack()
	client, _ := f.client()
	meshMgr, _ := stackMesh(client)

	created, err := ensureMonitoring(t.Context(), client, meshMgr)
	require.NoError(t, err)
	assert.False(t, created)
	assert.False(t, f.called("create"))
	assert.True(t, f.called("cp - sind-alloy:"))
}

func TestEnsureMonitoring_Errors(t *testing.T) {
	tests := []struct {
		name, prefix string
		stack        bool
		created      bool
	}{
		{"exists", "container inspect sind-alloy", false, false},
		{"pinned address", "network inspect sind-mesh", false, false},
		{"alloy config", "info", false, false},
		{"stack", "volume create", false, true},
		{"sync", "cp - sind-alloy:", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newStackFake(t)
			if tt.stack {
				f.withStack()
			}
			f.fail[tt.prefix] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
			client, _ := f.client()
			meshMgr, _ := stackMesh(client)
			created, err := ensureMonitoring(t.Context(), client, meshMgr)
			require.Error(t, err)
			assert.Equal(t, tt.created, created)
		})
	}
}

func TestRegisterStackDNS(t *testing.T) {
	f := newStackFake(t).withStack()
	f.containers["sind-loki"].state = docker.StateExited
	client, m := f.client()
	meshMgr, _ := stackMesh(client)

	require.NoError(t, registerStackDNS(t.Context(), client, meshMgr))
	var corefile string
	for _, c := range m.Calls {
		if strings.Join(c.Args, " ") == "cp - sind-dns:/" {
			corefile = c.Stdin
		}
	}
	assert.Contains(t, corefile, "grafana.sind.sind")
	assert.Contains(t, corefile, "prometheus.sind.sind")
	assert.NotContains(t, corefile, "loki.sind.sind")
}

func TestRegisterStackDNS_Errors(t *testing.T) {
	for _, prefix := range []string{"inspect sind-grafana", "cp - sind-dns:/"} {
		t.Run(prefix, func(t *testing.T) {
			f := newStackFake(t).withStack()
			f.fail[prefix] = mock.Result{Err: testutil.ExitCode1(t)}
			client, _ := f.client()
			meshMgr, _ := stackMesh(client)
			require.Error(t, registerStackDNS(t.Context(), client, meshMgr))
		})
	}
}

func TestStartMonitoring(t *testing.T) {
	t.Run("no stack", func(t *testing.T) {
		f := newStackFake(t)
		client, _ := f.client()
		meshMgr, _ := stackMesh(client)
		found, err := startMonitoring(t.Context(), client, meshMgr)
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("stopped", func(t *testing.T) {
		f := newStackFake(t).withStack().stopped()
		client, _ := f.client()
		meshMgr, _ := stackMesh(client)
		found, err := startMonitoring(t.Context(), client, meshMgr)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, docker.StateRunning, f.containers["sind-grafana"].state)
		assert.True(t, f.called("cp - sind-dns:/"))
	})
	t.Run("start fails", func(t *testing.T) {
		f := newStackFake(t).withStack().stopped()
		f.fail["start sind-loki"] = mock.Result{Err: testutil.ExitCode1(t)}
		client, _ := f.client()
		meshMgr, _ := stackMesh(client)
		_, err := startMonitoring(t.Context(), client, meshMgr)
		require.Error(t, err)
	})
}

func TestForgetMonitoredNodes(t *testing.T) {
	t.Run("no stack", func(t *testing.T) {
		f := newStackFake(t)
		client, _ := f.client()
		meshMgr, warnings := stackMesh(client)
		forgetMonitoredNodes(t.Context(), client, meshMgr, "dev")
		assert.Empty(t, *warnings)
		assert.False(t, f.called("cp - sind-alloy:"))
	})
	t.Run("cluster", func(t *testing.T) {
		f := newStackFake(t).withStack()
		client, _ := f.client()
		meshMgr, warnings := stackMesh(client)
		forgetMonitoredNodes(t.Context(), client, meshMgr, "dev")
		assert.Empty(t, *warnings)
		assert.True(t, f.called("cp - sind-alloy:"))
		assert.True(t, f.called("exec sind-alloy sh -c"))
	})
	t.Run("nodes", func(t *testing.T) {
		f := newStackFake(t).withStack()
		client, m := f.client()
		meshMgr, warnings := stackMesh(client)
		forgetMonitoredNodes(t.Context(), client, meshMgr, "dev", "worker-1")
		assert.Empty(t, *warnings)
		c := m.Calls[len(m.Calls)-1].Args
		assert.Equal(t, []string{"sh", "remove", "/var/log/sind/dev/worker-1"}, c[len(c)-3:])
	})
	t.Run("realm", func(t *testing.T) {
		f := newStackFake(t).withStack()
		client, _ := f.client()
		meshMgr, warnings := stackMesh(client)
		forgetMonitoredNodes(t.Context(), client, meshMgr, "")
		assert.Empty(t, *warnings)
		assert.True(t, f.called("cp - sind-alloy:"))
		assert.False(t, f.called("exec sind-alloy sh -c"))
	})
	for _, prefix := range []string{"start sind-loki", "cp - sind-alloy:", "exec sind-alloy sh -c"} {
		t.Run(prefix, func(t *testing.T) {
			f := newStackFake(t).withStack().stopped()
			f.fail[prefix] = mock.Result{Err: testutil.ExitCode1(t)}
			client, _ := f.client()
			meshMgr, warnings := stackMesh(client)
			forgetMonitoredNodes(t.Context(), client, meshMgr, "dev")
			require.Len(t, *warnings, 1)
			assert.Contains(t, (*warnings)[0], `updating the monitoring stack of realm "sind"`)
		})
	}
}

func TestClusterMonitored(t *testing.T) {
	f := newStackFake(t)
	f.nodes = []docker.ContainerListEntry{monitoredNode("dev", "controller", "controller")}
	client, _ := f.client()

	monitored, err := clusterMonitored(t.Context(), client, "sind", "dev")
	require.NoError(t, err)
	assert.True(t, monitored)
	monitored, err = clusterMonitored(t.Context(), client, "sind", "prod")
	require.NoError(t, err)
	assert.False(t, monitored)

	other, err := otherMonitoredClusters(t.Context(), client, "sind", "dev")
	require.NoError(t, err)
	assert.False(t, other)
	other, err = otherMonitoredClusters(t.Context(), client, "sind", "prod")
	require.NoError(t, err)
	assert.True(t, other)

	f.fail["ps"] = mock.Result{Err: testutil.ExitCode1(t)}
	_, err = clusterMonitored(t.Context(), client, "sind", "dev")
	require.ErrorContains(t, err, "listing monitored nodes")
	_, err = otherMonitoredClusters(t.Context(), client, "sind", "dev")
	require.Error(t, err)
}

func TestRemoveMonitoring(t *testing.T) {
	f := newStackFake(t).withStack()
	client, _ := f.client()
	require.NoError(t, removeMonitoringContainers(t.Context(), client, "sind"))
	assert.Empty(t, f.containers)
	require.NoError(t, removeMonitoringVolumes(t.Context(), client, "sind"))
	assert.Empty(t, f.volumes)

	f.fail["ps"] = mock.Result{Err: testutil.ExitCode1(t)}
	f.fail["volume ls"] = mock.Result{Err: testutil.ExitCode1(t)}
	require.ErrorContains(t, removeMonitoringContainers(t.Context(), client, "sind"), "removing the monitoring stack")
	require.ErrorContains(t, removeMonitoringVolumes(t.Context(), client, "sind"), "removing the monitoring stack's volumes")
}

func TestCreateJournalVolume(t *testing.T) {
	f := newStackFake(t)
	client, m := f.client()
	require.NoError(t, createJournalVolume(t.Context(), client, "sind"))
	assert.True(t, f.volumes["sind-journal"])
	assert.Equal(t, []string{"volume", "create",
		"--label", "com.docker.compose.project=sind-mesh",
		"--label", "com.docker.compose.volume=journal",
		"--label", "sind.realm=sind",
		"sind-journal"}, m.Calls[0].Args)

	f.fail["volume create"] = mock.Result{Err: testutil.ExitCode1(t)}
	require.ErrorContains(t, createJournalVolume(t.Context(), client, "sind"), "creating journal volume")
}

func TestResetClusterJournals(t *testing.T) {
	f := newStackFake(t)
	client, m := f.client()
	require.NoError(t, resetClusterJournals(t.Context(), client, "sind", "dev", "img:1", []string{"controller", "worker-0"}))
	assert.Equal(t, []string{"run", "--rm",
		"--name", "sind-dev-journal-helper",
		"--label", "sind.realm=sind",
		"--label", "sind.cluster=dev",
		"--network", "none",
		"--mount", "type=volume,source=sind-journal,target=/journal,volume-nocopy",
		"--entrypoint", "/bin/sh",
		"img:1", "-c", journalHelperScript, "sh", "dev", "controller", "worker-0"}, m.Calls[0].Args)

	f.fail["run"] = mock.Result{Err: testutil.ExitCode1(t)}
	require.ErrorContains(t, resetClusterJournals(t.Context(), client, "sind", "dev", "img:1", nil), "making journal directories")
}

func TestMonitoringProbes(t *testing.T) {
	unmanaged := false
	cfg := &config.Cluster{
		Nodes: []config.Node{
			{Role: config.RoleController, Image: "ctl"},
			{Role: config.RoleDB, Image: "db"},
			{Role: config.RoleWorker, Image: "ctl"},
			{Role: config.RoleWorker, Image: "w"},
			{Role: config.RoleWorker, Image: "w"},
			{Role: config.RoleWorker, Image: "plain", Managed: &unmanaged},
			{Role: config.RoleSubmitter, Image: "sub"},
		},
	}
	cfg.Slurm.Main = config.Section{Content: "PluginDir=/opt/slurm/lib\n"}
	assert.Equal(t, []imageProbe{
		{image: "ctl", req: slurm.ProbeRequest{Daemons: []string{slurm.Slurmctld, slurm.Slurmd}, PluginDir: "/opt/slurm/lib"}},
		{image: "db", req: slurm.ProbeRequest{PluginDir: "/opt/slurm/lib", MariaDB: true}},
		{image: "w", req: slurm.ProbeRequest{Daemons: []string{slurm.Slurmd}, PluginDir: "/opt/slurm/lib"}},
	}, monitoringProbes(cfg))
}

func TestProbeMonitoringImages(t *testing.T) {
	cfg := createCfg()
	cfg.Nodes = append(cfg.Nodes, config.Node{Role: config.RoleDB, Image: "db:1"})

	t.Run("ok", func(t *testing.T) {
		f := newStackFake(t)
		f.logError = "--log-error=/var/log/mariadb.log"
		client, m := f.client()
		meshMgr, warnings := stackMesh(client)
		version, err := probeMonitoringImages(t.Context(), client, meshMgr, cfg)
		require.NoError(t, err)
		assert.Equal(t, "26.05.4", version)
		assert.Len(t, m.Calls, 2)
		require.Len(t, *warnings, 1)
		assert.Contains(t, (*warnings)[0], "image db:1: ")
	})
	t.Run("unsupported", func(t *testing.T) {
		f := newStackFake(t)
		f.version = "25.11.2"
		client, _ := f.client()
		meshMgr, _ := stackMesh(client)
		_, err := probeMonitoringImages(t.Context(), client, meshMgr, createCfg())
		require.ErrorIs(t, err, ErrImageUnsupported)
		assert.ErrorContains(t, err, "image img:1 cannot serve monitoring: ")
	})
	t.Run("probe fails", func(t *testing.T) {
		f := newStackFake(t)
		f.fail["run"] = mock.Result{Err: testutil.ExitCode1(t)}
		client, _ := f.client()
		meshMgr, _ := stackMesh(client)
		_, err := probeMonitoringImages(t.Context(), client, meshMgr, createCfg())
		require.ErrorContains(t, err, "probing image img:1")
	})
}

func TestGetMonitoring(t *testing.T) {
	f := newStackFake(t)
	client, _ := f.client()

	_, err := GetMonitoring(t.Context(), client, "sind")
	require.ErrorIs(t, err, ErrNoMonitoring)
	require.EqualError(t, err, `no monitoring stack in realm "sind"`)

	f.withStack()
	f.containers["sind-alloy"].state = docker.StateExited
	ctl := monitoredNode("prod", "controller", "controller")
	ctl.Labels[LabelSlurmVersion] = "26.05.4"
	f.nodes = []docker.ContainerListEntry{monitoredNode("prod", "worker-0", "worker"), ctl, monitoredNode("dev", "controller", "controller")}

	m, err := GetMonitoring(t.Context(), client, "sind")
	require.NoError(t, err)
	assert.Equal(t, "sind", m.Realm)
	require.Len(t, m.Services, 4)
	assert.Equal(t, MonitoringService{
		Name: observability.ServiceGrafana, Container: "sind-grafana", IP: f.containers["sind-grafana"].ip,
		URL: "http://grafana.sind.sind:3000", State: docker.StateRunning, Ready: true, Checked: true,
	}, m.Services[0])
	assert.Equal(t, "", m.Services[3].URL, "a stopped Alloy has no address")
	assert.Equal(t, []MonitoredCluster{{Name: "dev", Nodes: 1}, {Name: "prod", Nodes: 2, SlurmVersion: "26.05.4"}}, m.Clusters)

	f.containers["sind-alloy"].state = docker.StateRunning
	m, err = GetMonitoring(t.Context(), client, "sind")
	require.NoError(t, err)
	assert.Equal(t, "http://"+f.containers["sind-alloy"].ip+":12345", m.Services[3].URL)

	f.fail["ps -a --no-trunc --format json --filter label=sind.realm"] = mock.Result{Err: testutil.ExitCode1(t)}
	_, err = GetMonitoring(t.Context(), client, "sind")
	require.ErrorContains(t, err, "listing monitored nodes")
	f.fail["ps"] = mock.Result{Err: testutil.ExitCode1(t)}
	_, err = GetMonitoring(t.Context(), client, "sind")
	require.ErrorContains(t, err, "listing the monitoring containers")
}
