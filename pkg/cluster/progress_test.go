// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// withoutCalls draws the spans of events as progresstest.Capture.Tree
// does, leaving out the calls and what is under them: the docker commands,
// which the tests of the docker package cover, and of which a readiness
// wait runs as many as its rounds take.
func withoutCalls(events []progress.Event) string {
	var c progresstest.Capture
	calls := map[progress.SpanID]bool{}
	for _, e := range events {
		if e.Kind == progress.KindCall || calls[e.Parent] {
			calls[e.Span] = true
			continue
		}
		c.Handle(e)
	}
	return c.Tree()
}

// endSpan ends a span failed with the work's error, unless the context
// had been canceled for another reason first: then the work was stopped,
// and the span ends canceled with the work's error as its text.
func TestEndSpan(t *testing.T) {
	failure := errors.New("node worker-0: image not found")
	sibling := errors.New("node worker-1: image not found")
	canceled := func(cause error) context.Context {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)
		return ctx
	}
	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want string
	}{
		{"ok", canceled(sibling), nil, "step s: ok\n"},
		{"failed first", t.Context(), failure, "step s: failed (target): node worker-0: image not found\n"},
		{"failed, and so canceled the group", canceled(failure), failure, "step s: failed (target): node worker-0: image not found\n"},
		{"stopped by a sibling", canceled(sibling), failure, "step s: canceled (canceled): node worker-0: image not found\n"},
		{"interrupted", canceled(nil), failure, "step s: canceled (canceled): node worker-0: image not found\n"},
		{"timed out", expired, fmt.Errorf("waiting: %w", context.DeadlineExceeded), "step s: failed (timeout): waiting: context deadline exceeded\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w := progresstest.Watch(t.Context(), t)
			_, span := progress.Start(ctx, progress.KindStep, "s")
			endSpan(tc.ctx, span, tc.err)
			assert.Equal(t, tc.want, w.Finish())
		})
	}
	assert.ErrorIs(t, stopped{failure}, failure)
}

// nodesOnCall answers the docker commands of setupNodes for nodes that get
// ready at once, except as override says.
func nodesOnCall(t *testing.T, override func(args []string) (mock.Result, bool)) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		if r, ok := override(args); ok {
			return r
		}
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "inspect":
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"})}
		case strings.Contains(joined, "is-system-running"):
			return mock.Result{Stdout: "running\n"}
		case strings.Contains(joined, "/dev/tcp"):
			return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
		case strings.Contains(joined, "is-active"):
			return mock.Result{Stdout: "active\n"}
		case strings.Contains(joined, "ssh-keyscan"):
			return mock.Result{Stdout: "localhost ssh-ed25519 AAAA-hostkey\n"}
		}
		return mock.Result{Stdout: "cid\n"}
	}
}

// nodeConfigs returns the run configs of workers named names.
func workerConfigs(names ...string) []RunConfig {
	configs := make([]RunConfig, len(names))
	for i, name := range names {
		configs[i] = RunConfig{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: name, Role: config.RoleWorker, Image: "img:1"}
	}
	return configs
}

// The nodes are the step "nodes", with a target for each node, all queued
// before the first runs, each waiting for its node to get ready.
func TestSetupNodes_ReportsTheNodes(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	m := &mock.Executor{OnCall: nodesOnCall(t, func([]string) (mock.Result, bool) { return mock.Result{}, false })}
	client := docker.NewClient(m)
	configs := append([]RunConfig{{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController}},
		workerConfigs("worker-0", "worker-1")...)

	_, err := setupNodes(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), mesh.DefaultRealm, "dev", "ssh-key", configs, &readiness{interval: time.Millisecond}, nil)

	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `step nodes total=3 [fold]: ok
  target controller role=controller: ok
    wait ready: ok
  target worker-[0-1] role=worker: ok
    wait ready: ok
`, withoutCalls(w.Events()))
}

// A node's setup is the call "setup", with its docker exec under it,
// ended with the setup's error; stopped by a sibling's failure, it ends
// canceled.
func TestSetupNode_IsTheCallSetup(t *testing.T) {
	stoppedBy := func(cause error) context.Context {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)
		return ctx
	}
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		stdout string
		want   string
	}{
		{"ok", t.Context(), "localhost ssh-ed25519 AAAA-hostkey\n", "call setup: ok\n  call docker exec [hidden]: ok\n"},
		{"failed", t.Context(), "", "call setup: failed (target): setting up SSH: no ed25519 host key found\n  call docker exec [hidden]: ok\n"},
		{"stopped by a sibling", stoppedBy(errors.New("node worker-1: image not found")), "",
			"call setup: canceled (canceled): setting up SSH: no ed25519 host key found\n  call docker exec [hidden]: ok\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w := progresstest.Watch(tc.ctx, t)
			var m mock.Executor
			m.AddResult(tc.stdout, "", nil)

			_, _ = setupNode(ctx, docker.NewClient(&m), "sind-dev-worker-0", RunConfig{}, "ssh-ed25519 AAAA-test-key\n")

			assert.Equal(t, tc.want, w.Finish())
		})
	}
}

// The first node that fails cancels the others: their targets end
// canceled, not failed, and the error is that of the node that failed.
func TestSetupNodes_AFailedNodeCancelsTheOthers(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	m := &mock.Executor{OnCall: nodesOnCall(t, func(args []string) (mock.Result, bool) {
		switch {
		case args[0] == "create" && strings.Contains(strings.Join(args, " "), "sind-dev-worker-0"):
			return mock.Result{Err: errors.New("image not found")}, true
		case args[0] == "inspect":
			// The other nodes never get ready.
			return mock.Result{Stdout: inspectJSON(t, args[1], "created", nil)}, true
		}
		return mock.Result{}, false
	})}
	client := docker.NewClient(m)

	_, err := setupNodes(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), mesh.DefaultRealm, "dev", "ssh-key", workerConfigs("worker-0", "worker-1", "worker-2"), &readiness{interval: time.Millisecond}, nil)

	require.EqualError(t, err, "node worker-0: creating container worker-0: image not found")
	w.Finish()
	assert.Equal(t, `step nodes total=3 [fold]: failed (target): node worker-0: creating container worker-0: image not found
  target worker-0 role=worker: failed (target): node {}: creating container {}: image not found
  target worker-[1-2] role=worker: canceled (canceled): waiting for {}: node sind-dev-{} not ready: context canceled; last probe error: probe container: container sind-dev-{} is created, expected running
    wait ready message=container: canceled (canceled): node sind-dev-{} not ready: context canceled; last probe error: probe container: container sind-dev-{} is created, expected running
`, withoutCalls(w.Events()))
}

// The pull is the step "pull images", with a target for each image it
// pulls, only when it pulls one.
func TestStartPull_ReportsThePulls(t *testing.T) {
	exitErr := notFoundErr(t)
	for _, tc := range []struct {
		name    string
		present []string
		want    string
	}{
		{"nothing to pull", []string{"img:1", "other:2"}, ""},
		{"a missing image", []string{"img:1"}, "step pull images total=1 [fold,show-lines]: ok\n  target other:2 [show-lines]: ok\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w := progresstest.Watch(t.Context(), t)
			m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
				if isImageExists(args) {
					for _, image := range tc.present {
						if args[4] == image {
							return mock.Result{Stdout: "sha256:local\n"}
						}
					}
					return mock.Result{Stderr: "Error: No such image: " + args[4] + "\n", Err: exitErr}
				}
				return mock.Result{}
			}}
			g, gctx := errgroup.WithContext(ctx)
			p := startPull(gctx, g, docker.NewClient(m), []string{"img:1", "other:2"}, false)
			require.NoError(t, g.Wait())
			require.NoError(t, p.err)
			w.Finish()
			assert.Equal(t, tc.want, withoutCalls(w.Events()))
		})
	}
}

// A pull that fails fails its target and the step; the checks that waited
// for it are stopped, not failed. The rollback removes what the checks
// created.
func TestCreate_AFailedPullStopsThePreflight(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), pullOnCall(mock.Result{Err: errors.New("manifest unknown")}))
	client := docker.NewClient(&m)
	cfg := createCfg()
	cfg.Pull = true

	_, err := Create(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), cfg, time.Millisecond)

	require.EqualError(t, err, "pulling img:1: manifest unknown")
	w.Finish()
	assert.Equal(t, `step preflight [hidden]: canceled (canceled): pulling img:1: manifest unknown
step pull images total=1 [fold,show-lines]: failed (target): pulling img:1: manifest unknown
  target img:1 [show-lines]: failed (target): pulling {}: manifest unknown
step rollback: ok
`, withoutCalls(w.Events()))
}

// An inspection of the images that fails before any pull fails the
// preflight, which shows it: there is no step "pull images" to show it, so
// the preflight is not stopped by a pull.
func TestCreate_AFailedImageInspectionFailsThePreflight(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if isImageExists(args) {
			return mock.Result{Err: errors.New("docker daemon unavailable")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)

	_, err := Create(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), createCfg(), time.Millisecond)

	require.EqualError(t, err, "inspecting image img:1: docker daemon unavailable")
	w.Finish()
	assert.Equal(t, `step preflight [hidden]: failed (target): inspecting image img:1: docker daemon unavailable
step rollback: ok
`, withoutCalls(w.Events()))
}

// A Create reports its steps: the hidden preflight, the nodes, the mesh
// registration, Slurm, the accounts and the home directories; each node is
// a target of the nodes and of Slurm.
func TestCreate_ReportsItsSteps(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()
	var registered atomic.Int32
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), accountsOnCall(&registered, mock.Result{}))
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)

	_, err := Create(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), accountsCfg(), time.Millisecond)

	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `step accounts: ok
  wait ready: ok
step home directories: ok
step mesh registration: ok
step nodes total=3 [fold]: ok
  target controller role=controller: ok
    wait ready: ok
  target db role=db: ok
    wait ready: ok
  target worker-0 role=worker: ok
    wait ready: ok
step preflight [hidden]: ok
step slurm total=3 [fold]: ok
  target controller role=controller: ok
    wait ready: ok
  target db role=db: ok
    wait ready: ok
  target worker-0 role=worker: ok
    wait ready: ok
`, withoutCalls(w.Events()))
}

// A db node that fails ends the step: the nodes that did not start end
// canceled with it.
func TestEnableSlurm_ReportsTheNodes(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[1] == "sind-dev-db" && args[2] == "systemctl" && args[3] == "enable" {
			return mock.Result{Err: errors.New("mariadb failed")}
		}
		return mock.Result{}
	}
	ctx, w := progresstest.Watch(t.Context(), t)
	configs := []RunConfig{
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "controller", Role: config.RoleController, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "db", Role: config.RoleDB, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-0", Role: config.RoleWorker, Managed: true},
		{Realm: mesh.DefaultRealm, ClusterName: "dev", ShortName: "worker-1", Role: config.RoleWorker},
	}

	err := enableSlurm(ctx, docker.NewClient(&m), mesh.DefaultRealm, "dev", configs, time.Millisecond, nil)

	require.Error(t, err)
	w.Finish()
	assert.Equal(t, `step slurm total=3 [fold]: failed (target): enabling mariadb on db: mariadb failed\nmariadb journal:\n
  target controller role=controller: canceled (canceled): not finished
  target db role=db: failed (target): enabling mariadb on {}: mariadb failed\nmariadb journal:\n
  target worker-0 role=worker: canceled (canceled): not finished
`, withoutCalls(w.Events()))
}

// WorkerAdd reports the same steps as Create for the nodes it adds.
func TestWorkerAdd_ReportsItsSteps(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)

	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 2}, time.Millisecond)

	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `step mesh registration: ok
step nodes total=2 [fold]: ok
  target worker-[1-2] role=worker: ok
    wait ready: ok
step slurm total=2 [fold]: ok
  target worker-[1-2] role=worker: ok
    wait ready: ok
`, withoutCalls(w.Events()))
}
