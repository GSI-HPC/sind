// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/require"
)

// fake is a Docker daemon that answers the commands the collectors run, with
// what the test sets. The mock executor serializes the calls; the test
// changes the fields with the methods below.
type fake struct {
	t  *testing.T
	mu sync.Mutex
	// down is set while the daemon does not answer.
	down bool
	// nodes are the containers of the realms, by realm.
	nodes map[string][]testutil.PsEntry
	// external are the containers outside sind, by realm: those on its mesh.
	external map[string][]string
	// locks are the realms whose lock is held, with the command.
	locks map[string]string
	// meshes are the realms that have a mesh.
	meshes map[string]bool
	// monitoring are the realms that have a monitoring stack.
	monitoring map[string]bool
	// clusters are the clusters that exist, as "realm/cluster".
	clusters map[string]bool
	// calls counts the commands by their first two arguments.
	calls map[string]int
	// failing makes the commands whose arguments contain it fail.
	failing string

	// gate, when set, holds the command `docker info` until it closes.
	gate chan struct{}

	exec  *mock.Executor
	pipes *mock.Pipes
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		t: t, nodes: map[string][]testutil.PsEntry{}, external: map[string][]string{},
		locks: map[string]string{}, meshes: map[string]bool{}, monitoring: map[string]bool{},
		clusters: map[string]bool{}, calls: map[string]int{}, pipes: &mock.Pipes{},
	}
	f.exec = &mock.Executor{OnCall: f.call, OnStart: f.pipes.OnStart}
	return f
}

func (f *fake) client() *docker.Client { return docker.NewClient(f.exec) }

// set changes the fake under its lock.
func (f *fake) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// count returns how many commands starting with key ran.
func (f *fake) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func node(realm, cluster, name, role string) testutil.PsEntry {
	return testutil.PsEntry{
		ID: name, Names: realm + "-" + cluster + "-" + name, State: "running", Image: "img",
		Labels: fmt.Sprintf("sind.realm=%s,sind.cluster=%s,sind.role=%s", realm, cluster, role),
	}
}

// monitoringServices are the containers of a monitoring stack, after the realm.
var monitoringServices = []string{"grafana", "prometheus", "loki", "alloy"}

type lsRow struct {
	Name, Driver, Labels string
}

func (f *fake) call(args []string, _ string) mock.Result {
	f.mu.Lock()
	gate := f.gate
	f.mu.Unlock()
	if gate != nil && args[0] == "info" {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := args[0]
	if len(args) > 1 {
		key += " " + args[1]
	}
	f.calls[key]++
	f.calls[args[0]]++
	if f.down {
		return mock.Result{Stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\nsecond line\n", Err: testutil.ExitCode1(f.t)}
	}
	if f.failing != "" && strings.Contains(strings.Join(args, " "), f.failing) {
		return mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(f.t)}
	}
	filters := testutil.ArgValues(args, "--filter")
	realmOf := func() (string, bool) {
		for _, fl := range filters {
			if r, ok := strings.CutPrefix(fl, "label=sind.realm="); ok {
				return r, true
			}
		}
		return "", false
	}
	switch args[0] {
	case "ps":
		if i := slices.IndexFunc(filters, func(s string) bool { return strings.HasPrefix(s, "label=com.docker.compose.project=") }); i >= 0 {
			// The containers of a monitoring stack.
			realm := strings.TrimSuffix(strings.TrimPrefix(filters[i], "label=com.docker.compose.project="), "-mesh")
			var rows []testutil.PsEntry
			if f.monitoring[realm] {
				for _, svc := range monitoringServices {
					rows = append(rows, testutil.PsEntry{ID: svc, Names: realm + "-" + svc, State: "exited"})
				}
			}
			return mock.Result{Stdout: ndjson(rows)}
		}
		if i := slices.IndexFunc(filters, func(s string) bool { return strings.HasPrefix(s, "network=") }); i >= 0 {
			realm := strings.TrimSuffix(strings.TrimPrefix(filters[i], "network="), "-mesh")
			var rows []testutil.PsEntry
			for _, n := range f.external[realm] {
				rows = append(rows, testutil.PsEntry{ID: n, Names: n, State: "running"})
			}
			return mock.Result{Stdout: ndjson(rows)}
		}
		if realm, ok := realmOf(); ok {
			return mock.Result{Stdout: ndjson(f.nodes[realm])}
		}
		var all []testutil.PsEntry
		for _, realm := range slices.Sorted(maps.Keys(f.nodes)) {
			all = append(all, f.nodes[realm]...)
		}
		return mock.Result{Stdout: ndjson(all)}
	case "network":
		switch args[1] {
		case "ls":
			realm, _ := realmOf()
			var rows []lsRow
			for c := range f.clusters {
				if r, name, _ := strings.Cut(c, "/"); r == realm {
					rows = append(rows, lsRow{Name: realm + "-" + name + "-net", Driver: "bridge"})
				}
			}
			if _, held := f.locks[realm]; held {
				rows = append(rows, lsRow{Name: realm + "-lock", Driver: "bridge"})
			}
			return mock.Result{Stdout: ndjson(rows)}
		case "inspect":
			name := args[2]
			if realm, ok := strings.CutSuffix(name, "-lock"); ok && len(args) > 3 {
				cmd, held := f.locks[realm]
				if !held {
					return mock.Result{Stderr: testutil.NoSuchNetwork(name), Err: testutil.ExitCode1(f.t)}
				}
				return mock.Result{Stdout: fmt.Sprintf(`{"Id":"n1","Name":%q,"Created":"2026-01-02T03:04:05Z","ConfigOnly":true,"Labels":{"sind.realm":%q,"sind.lock.command":%q,"sind.lock.pid":"42"}}`, name, realm, cmd)}
			}
			if len(args) == 3 {
				// One network: the existence check of a cluster's status.
				for c := range f.clusters {
					if r, name, _ := strings.Cut(c, "/"); r+"-"+name+"-net" == args[2] {
						return mock.Result{Stdout: "[{}]\n"}
					}
				}
				return mock.Result{Stderr: testutil.NoSuchNetwork(args[2]), Err: testutil.ExitCode1(f.t)}
			}
			return mock.Result{Stdout: "[]"}
		}
	case "volume":
		return mock.Result{Stdout: ""}
	case "inspect":
		name := args[1]
		if realm, svc, ok := strings.Cut(name, "-"); ok && f.monitoring[realm] && slices.Contains(monitoringServices, svc) {
			var infos []string
			for _, n := range args[1 : len(args)-2] {
				infos = append(infos, fmt.Sprintf(`{"Id":%q,"Name":"/%s","State":{"Status":"exited"},"Config":{"Image":"img"}}`, n, n))
			}
			return mock.Result{Stdout: "[" + strings.Join(infos, ",") + "]"}
		}
		if realm, ok := strings.CutSuffix(name, "-dns"); ok && f.meshes[realm] {
			return mock.Result{Stdout: fmt.Sprintf(`[{"Id":"d1","Name":"/%s","State":{"Status":"running"},"Config":{"Image":"coredns"},"NetworkSettings":{"Networks":{"%s-mesh":{"IPAddress":"10.0.0.2"}}}}]`, name, realm)}
		}
		if realm, ok := strings.CutSuffix(name, "-ssh"); ok && f.meshes[realm] {
			return mock.Result{Stdout: fmt.Sprintf(`[{"Id":"s1","Name":"/%s","State":{"Status":"running"},"Config":{"Image":"sshd"}}]`, name)}
		}
		if len(args) == 2 {
			// The DNS container of the network health of a status.
			return mock.Result{Stdout: "[{}]\n"}
		}
		return mock.Result{Stderr: testutil.NoSuchContainer(name), Err: testutil.ExitCode1(f.t)}
	case "container":
		// container inspect: the existence check of the mesh containers.
		name := args[2]
		if realm, ok := strings.CutSuffix(name, "-dns"); ok && f.meshes[realm] {
			return mock.Result{Stdout: "[{}]"}
		}
		return mock.Result{Stderr: testutil.NoSuchContainer(name), Err: testutil.ExitCode1(f.t)}
	case "cp":
		// The mesh DNS records: the Corefile of the DNS container.
		return mock.Result{Stdout: testutil.TarArchive("Corefile", "hosts {\n  10.0.0.5 controller.dev.sind\n  10.0.0.6 worker-0.dev.sind\n}\n")}
	}
	return mock.Result{Stderr: fmt.Sprintf("fake: unexpected %v\n", args), Err: testutil.ExitCode1(f.t)}
}

func ndjson[T any](rows []T) string {
	if len(rows) == 0 {
		return ""
	}
	return testutil.NDJSON(rows...)
}

// quietLog discards logs.
func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// debugLog returns a logger that writes everything it is given to the
// buffer.
func debugLog() (*slog.Logger, *syncBuffer) {
	b := &syncBuffer{}
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})), b
}

// syncBuffer is a bytes.Buffer that goroutines may share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

// waitFor fails the test unless cond holds within a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, 5*time.Millisecond, what)
}

// never fails the test when cond holds within d.
func never(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	require.Never(t, cond, d, 5*time.Millisecond, what)
}

// start runs run in a goroutine and returns the function that ends it: it
// cancels the context and fails the test when run does not return, which is
// a leak. The test's cleanup calls it too.
func start(t *testing.T, run func(context.Context)) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Run did not return after its context ended")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// cancelled returns a context that has ended.
func cancelled(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	cancel()
	return ctx, cancel
}

// settle waits until the daemon has had no command for a moment, so that a
// count taken then is not changed by a read still going on.
func (f *fake) settle() {
	f.t.Helper()
	last, quietSince := -1, time.Now()
	for time.Since(quietSince) < 60*time.Millisecond {
		n := f.total()
		if n != last {
			last, quietSince = n, time.Now()
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.calls {
		if !strings.Contains(k, " ") {
			n += v
		}
	}
	return n
}
