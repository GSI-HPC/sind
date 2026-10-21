// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// colWatcher records what a Watcher is told.
type colWatcher struct {
	mu       sync.Mutex
	topics   [][]store.Topic
	released int
}

func (w *colWatcher) watch(topics []store.Topic) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.topics = append(w.topics, topics)
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.released++
	}
}

func (w *colWatcher) state() (topics [][]store.Topic, released int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]store.Topic(nil), w.topics...), w.released
}

func TestColWatcherHearsTheTopicsOfAStreamAndItsEnd(t *testing.T) {
	cfg, st := testConfig(t)
	w := &colWatcher{}
	cfg.Env.AddWatcher(w.watch)
	cfg.Env.AddWatcher(func([]store.Topic) func() { return nil }) // a watcher with nothing to release
	ts := newTestServerFrom(t, cfg, st).login()

	c := ts.connect("f=one-run&f=count&id=x")
	got, released := w.state()
	require.Len(t, got, 1)
	assert.Equal(t, []store.Topic{store.TopicTasks, store.RunTopic("x")}, got[0], "exact topics and those of Watch, in fragment order")
	assert.Zero(t, released)

	c.cancel()
	c.waitServed()
	require.Eventually(t, func() bool { _, r := w.state(); return r == 1 }, 2*time.Second, 5*time.Millisecond)
}

func TestColWatcherIsReleasedWhenTheServerEnds(t *testing.T) {
	cfg, st := testConfig(t)
	w := &colWatcher{}
	cfg.Env.AddWatcher(w.watch)
	ts := newTestServerFrom(t, cfg, st).login()

	c := ts.connect("f=count")
	ts.cancel()
	c.waitServed()
	_, released := w.state()
	assert.Equal(t, 1, released)
}

func TestColWatcherIsNotToldOfARefusedStream(t *testing.T) {
	cfg, st := testConfig(t)
	w := &colWatcher{}
	cfg.Env.AddWatcher(w.watch)
	ts := newTestServerFrom(t, cfg, st).login()

	assert.Equal(t, 400, ts.get("/events?f=nope").Code)
	got, _ := w.state()
	assert.Empty(t, got)
}

func TestColEnvWithoutWatchers(*testing.T) {
	var e Env
	e.watch([]store.Topic{store.TopicRuns})()
}

func TestColFragmentTopics(t *testing.T) {
	frags := []Fragment{
		{Name: "a", Topics: []store.Topic{"x", "y"}},
		{Name: "b", Topics: []store.Topic{"y"}, Watch: func(p Params) []store.Topic { return []store.Topic{"z", "x", store.Topic(p.Get("id"))} }},
		{Name: "c", Prefixes: []string{"run/"}},
	}
	assert.Equal(t, []store.Topic{"x", "y", "z", "7"}, fragmentTopics(frags, Params{"id": {"7"}}))
	assert.Empty(t, fragmentTopics(nil, nil))
}

// colDocker is a Docker daemon for the collect service: it knows one node
// and refuses what else it is asked, and notes the commands it ran.
type colDocker struct {
	pipes    mock.Pipes
	clusterQ atomic.Int32
	downIn   atomic.Bool
}

func (d *colDocker) executor(t *testing.T) *mock.Executor {
	return &mock.Executor{
		OnStart: d.pipes.OnStart,
		OnCall: func(args []string, _ string) mock.Result {
			if d.downIn.Load() {
				return mock.Result{Stderr: "Cannot connect to the Docker daemon\n", Err: testutil.ExitCode1(t)}
			}
			if strings.Contains(strings.Join(args, " "), "label=sind.cluster=dev") {
				d.clusterQ.Add(1)
			}
			if args[0] == "ps" {
				return mock.Result{Stdout: testutil.NDJSON(testutil.PsEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller",
				})}
			}
			return mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
		},
	}
}

func colEnv(t *testing.T, exec *mock.Executor) *Env {
	t.Helper()
	return &Env{Store: store.New(), Exec: exec, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Addr: "127.0.0.1:7878"}
}

// colStart sets the service up on env and runs it, and returns the function
// that ends it and waits for Run to return.
func colStart(t *testing.T, env *Env) (stop func()) {
	t.Helper()
	svc := colService()
	require.Equal(t, "collect", svc.Name)
	require.NoError(t, svc.Setup(env))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx, env)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("the collect service did not stop with its context")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestColServiceFillsTheStoreAndProvidesEnvKeys(t *testing.T) {
	var d colDocker
	env := colEnv(t, d.executor(t))
	stop := colStart(t, env)

	require.Eventually(t, func() bool { return len(env.Store.Snapshot().Realms) == 1 }, 5*time.Second, 5*time.Millisecond)
	assert.True(t, env.Store.Snapshot().Docker.Reachable())
	require.Eventually(t, func() bool { return env.Store.Snapshot().Doctor != nil && !env.Store.Snapshot().Doctor.Running }, 5*time.Second, 5*time.Millisecond)

	// The host checks run again on request.
	before := env.Store.Snapshot().Doctor.Time
	doctor, ok := env.Get(ColDoctorKey).(func())
	require.True(t, ok)
	doctor()
	require.Eventually(t, func() bool { return env.Store.Snapshot().Doctor.Time.After(before) }, 5*time.Second, 5*time.Millisecond)

	// A refresh reads the realm now: a node appears.
	refresh, ok := env.Get(ColRefreshKey).(func(string))
	require.True(t, ok)
	refresh("sind")
	require.Eventually(t, func() bool { return len(env.Store.Snapshot().NodesOf("sind")) == 1 }, 5*time.Second, 5*time.Millisecond)

	// A stream that watches the status of a cluster makes it be read.
	release := env.watch([]store.Topic{store.StatusTopic("sind", "dev")})
	require.Eventually(t, func() bool { return d.clusterQ.Load() > 0 }, 5*time.Second, 5*time.Millisecond)
	release()
	stop()
}

func TestColServiceReportsDockerDown(t *testing.T) {
	var d colDocker
	d.downIn.Store(true)
	env := colEnv(t, d.executor(t))
	colStart(t, env)
	require.Eventually(t, func() bool { return env.Store.Snapshot().Docker.Err != "" }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, "Cannot connect to the Docker daemon", env.Store.Snapshot().Docker.Err)
}

func TestColRunWithoutSetupDoesNothing(t *testing.T) {
	env := colEnv(t, &mock.Executor{})
	colService().Run(t.Context(), env)
}

func TestColServicesAreRegistered(t *testing.T) {
	var names []string
	for _, s := range defaultRegistry.services {
		names = append(names, s.Name)
	}
	assert.Contains(t, names, "collect")
	assert.Contains(t, names, "collect-activity")
}

func TestColActivityServiceFollowsTheDefaultLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	data, err := os.ReadFile(filepath.Join("activity", "testdata", "create-ok.jsonl"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sind"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sind", "activity.jsonl"), data, 0o600))

	env := colEnv(t, &mock.Executor{})
	svc := colActivityService()
	require.Equal(t, "collect-activity", svc.Name)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Run(ctx, env) }()
	require.Eventually(t, func() bool { return len(env.Store.Snapshot().Runs) == 1 }, 5*time.Second, 5*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the activity service did not stop with its context")
	}
}

func TestColActivityServiceWithoutStateDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	env := colEnv(t, &mock.Executor{})
	// It returns at once: there is no log to follow.
	colActivityService().Run(t.Context(), env)
	assert.Empty(t, env.Store.Snapshot().Runs)
}
