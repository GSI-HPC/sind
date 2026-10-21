// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deletedNodes are the containers of cluster dev that the delete tests
// remove, with the labels sind gives them.
func deletedNodes() []testutil.PsEntry {
	labels := func(role string) string { return "sind.realm=sind,sind.cluster=dev,sind.role=" + role }
	return []testutil.PsEntry{
		{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img", Labels: labels("controller")},
		{ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img", Labels: labels("worker")},
		{ID: "c", Names: "sind-dev-worker-1", State: "running", Image: "img", Labels: labels("worker")},
	}
}

// stepOrder returns the names of the steps under the command in the events,
// in the order they started, which a tree sorted by name does not show.
func stepOrder(events []progress.Event) []string {
	var command progress.SpanID
	var order []string
	for _, e := range events {
		switch {
		case e.Type != progress.TypeStart:
		case e.Kind == progress.KindCommand:
			command = e.Span
		case e.Kind == progress.KindStep && e.Parent == command:
			order = append(order, e.Name)
		}
	}
	return order
}

// deleteDev runs Delete of cluster dev under a command span, with nodes
// removed and the realm's other clusters as otherClusters says, and
// returns the error and the tree of the spans that are not docker calls.
func deleteDev(t *testing.T, otherClusters bool, stack *stackFake, opts deleteOnCallOpts) (string, []string, error) {
	t.Helper()
	opts.containers = deletedNodes()
	opts.networkExists = true
	opts.volumes = []string{"config", "munge", "data"}
	opts.otherClusters = otherClusters
	opts.knownHosts = "controller.dev.sind.sind ssh-ed25519 K1\n"
	m := &mock.Executor{OnCall: stackFirst(stack, deleteOnCall(t, testutil.ExitCode1(t), "dev", opts))}
	c := docker.NewClient(m)
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, command := progress.Start(ctx, progress.KindCommand, "delete cluster")

	err := Delete(ctx, c, mesh.NewManager(c, mesh.DefaultRealm), "dev")

	command.End(err)
	w.Finish()
	return testutil.WithoutCalls(w.Events()), stepOrder(w.Events()), err
}

// A cluster that leaves other clusters in the realm is deleted in the
// steps "mesh deregistration", the nodes' DNS records and known_hosts
// entries, "nodes", with a target for each node and its role, "network" and
// "volumes", with a target for each volume. The mesh stays.
func TestDelete_ReportsItsSteps(t *testing.T) {
	tree, order, err := deleteDev(t, true, newStackFake(t), deleteOnCallOpts{})

	require.NoError(t, err)
	assert.Equal(t, `command delete cluster: ok
  step mesh deregistration: ok
  step network: ok
  step nodes total=3 [fold]: ok
    target controller role=controller: ok
    target worker-[0-1] role=worker: ok
  step volumes total=3 [fold]: ok
    target sind-dev-config,sind-dev-data,sind-dev-munge: ok
`, tree)
	assert.Equal(t, []string{"mesh deregistration", "nodes", "network", "volumes"}, order)
}

// The last cluster takes the realm's shared resources with it: its
// monitoring stack, which is hidden as a realm may have none, the mesh, and
// then the stack's volumes.
func TestDelete_LastClusterReportsTheRealm(t *testing.T) {
	tree, order, err := deleteDev(t, false, newStackFake(t), deleteOnCallOpts{})

	require.NoError(t, err)
	assert.Equal(t, `command delete cluster: ok
  step mesh: ok
  step monitoring stack [hidden]: ok
  step monitoring volumes [hidden]: ok
  step network: ok
  step nodes total=3 [fold]: ok
    target controller role=controller: ok
    target worker-[0-1] role=worker: ok
  step volumes total=3 [fold]: ok
    target sind-dev-config,sind-dev-data,sind-dev-munge: ok
`, tree)
	assert.Equal(t, []string{"nodes", "network", "volumes", "monitoring stack", "mesh", "monitoring volumes"}, order)
}

// A node that cannot be removed fails its target and the step, with the
// error Delete returns; the other nodes are removed, and the steps after
// the failed one do not run.
func TestDelete_ReportsAFailedNode(t *testing.T) {
	m := &mock.Executor{}
	base := stackFirst(newStackFake(t), deleteOnCall(t, testutil.ExitCode1(t), "dev", deleteOnCallOpts{
		containers: deletedNodes(), networkExists: true, otherClusters: true,
		knownHosts: "controller.dev.sind.sind ssh-ed25519 K1\n",
	}))
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "rm" && args[len(args)-1] == "sind-dev-worker-1" {
			return mock.Result{Err: errors.New("device busy")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(m)
	ctx, w := progresstest.Watch(t.Context(), t)

	err := Delete(ctx, c, mesh.NewManager(c, mesh.DefaultRealm), "dev")

	require.EqualError(t, err, "removing container sind-dev-worker-1: device busy")
	w.Finish()
	assert.Equal(t, `step mesh deregistration: ok
step nodes total=3 [fold]: failed (target): removing container sind-dev-worker-1: device busy
  target controller role=controller: ok
  target worker-0 role=worker: ok
  target worker-1 role=worker: failed (target): removing container sind-dev-{}: device busy
`, testutil.WithoutCalls(w.Events()))
}

// A removal that the end of its context stops ends canceled, and so does
// its step when every removal that failed was stopped; one that fails for
// its own reason fails the step. A container that is gone is no failure.
func TestRemoveEach_Outcomes(t *testing.T) {
	gone := errors.New("no such thing")
	removals := func(errs map[string]error) []removal {
		var rs []removal
		for _, n := range []string{"a", "b", "c"} {
			rs = append(rs, removal{name: "sind-" + n, node: n, role: "worker",
				run: func(context.Context) error { return errs[n] }})
		}
		return rs
	}
	for _, tc := range []struct {
		name   string
		cancel bool
		errs   map[string]error
		want   string
		class  progress.Class
	}{
		{"ok", false, nil, "step s total=3 [fold]: ok\n  target a,b,c role=worker: ok\n", progress.ClassNone},
		{"failed", false, map[string]error{"b": gone},
			"step s total=3 [fold]: failed (target): removing thing sind-b: no such thing\n  target a,c role=worker: ok\n  target b role=worker: failed (target): removing thing sind-{}: no such thing\n",
			progress.ClassTarget},
		{"stopped", true, map[string]error{"a": gone, "b": gone},
			"step s total=3 [fold]: canceled (canceled): removing thing sind-a: no such thing\\nremoving thing sind-b: no such thing\n  target a,b role=worker: canceled (canceled): removing thing sind-{}: no such thing\n  target c role=worker: ok\n",
			progress.ClassCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w := progresstest.Watch(t.Context(), t)
			if tc.cancel {
				var cancel context.CancelCauseFunc
				ctx, cancel = context.WithCancelCause(ctx)
				cancel(errors.New("interrupted"))
			}

			err := removeEach(ctx, "s", "thing", removals(tc.errs))

			assert.Equal(t, tc.want == "" || strings.Contains(tc.want, "failed") || strings.Contains(tc.want, "canceled"), err != nil)
			if err != nil {
				assert.Equal(t, tc.class, progress.Classify(err, nil))
			}
			assert.Equal(t, tc.want, w.Finish())
		})
	}
}

// failures joins what several items' work failed with, in order, and none
// when nothing failed.
func TestFailures(t *testing.T) {
	assert.NoError(t, failures(nil))
	assert.NoError(t, failures([]error{nil, nil}))
	err := failures([]error{errors.New("a"), nil, stopped{errors.New("b")}})
	assert.EqualError(t, err, "a\nb")
	assert.Equal(t, progress.ClassTarget, progress.Classify(err, nil))
	err = failures([]error{nil, stopped{errors.New("b")}, stopped{fmt.Errorf("c: %w", context.Canceled)}})
	assert.EqualError(t, err, "b\nc: context canceled")
	assert.Equal(t, progress.ClassCanceled, progress.Classify(err, nil))
}

// Below a target of a pool's step a step announces no targets of its own,
// which the pool's counts would take in.
func TestRemoveEach_BelowATargetHasNoTargets(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	assert.True(t, hasTargets(ctx))
	rs := []removal{{name: "sind-a", node: "a", run: func(context.Context) error { return nil }}}

	require.NoError(t, removeEach(withoutTargets(ctx), "nodes", "container", rs))

	assert.False(t, hasTargets(withoutTargets(ctx)))
	assert.Equal(t, "step nodes: ok\n", w.Finish())
}

// Nothing to remove reports nothing, as without a Bus.
func TestRemoveEach_NothingToRemove(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)

	require.NoError(t, removeEach(ctx, "nodes", "container", nil))

	assert.Empty(t, w.Finish())
}

// The node of a container is named by its short name, and by its whole
// name when it does not carry the realm and cluster it would be cut by.
func TestNodeOf(t *testing.T) {
	labelled := docker.ContainerListEntry{Name: "sind-dev-worker-0", Labels: docker.Labels{LabelRealm: "sind", LabelCluster: "dev"}}
	assert.Equal(t, "worker-0", nodeOf(labelled))
	assert.Equal(t, "sind-dev-worker-0", nodeOf(docker.ContainerListEntry{Name: "sind-dev-worker-0"}))
}

// A monitored cluster leaves the realm's monitoring stack last, as the
// step "monitoring", which is no longer hidden: the stack is known to
// collect the cluster.
func TestDelete_ReportsTheMonitoringStack(t *testing.T) {
	f := newStackFake(t).withStack()
	f.nodes = []docker.ContainerListEntry{monitoredNode("dev", "controller", "controller")}

	tree, order, err := deleteDev(t, true, f, deleteOnCallOpts{})

	require.NoError(t, err)
	assert.Equal(t, []string{"mesh deregistration", "nodes", "network", "volumes", "monitoring"}, order)
	assert.Contains(t, tree, "  step monitoring: ok\n")
}

// The workers that WorkerRemove removes leave Slurm and the mesh together,
// the steps "slurm" and "mesh deregistration", and then go, as the step
// "nodes" with a target for each; a monitored cluster's stack is last.
func TestWorkerRemove_ReportsItsSteps(t *testing.T) {
	nodesConf := "NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"
	f := newStackFake(t).withStack()
	f.nodes = []docker.ContainerListEntry{monitoredNode("dev", "worker-1", "worker")}
	m := &mock.Executor{OnCall: stackFirst(f, workerRemoveOnCall(t, nodesConf))}
	client := docker.NewClient(m)
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, command := progress.Start(ctx, progress.KindCommand, "delete worker")

	err := WorkerRemove(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"})

	command.End(err)
	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `command delete worker: ok
  step mesh deregistration: ok
  step monitoring: ok
    wait ready timeout=2m0s: ok
  step nodes total=1 [fold]: ok
    target worker-1 role=worker: ok
  step slurm: ok
`, testutil.WithoutCalls(w.Events()))
	order := stepOrder(w.Events())
	assert.Equal(t, "nodes", order[len(order)-2])
	assert.Equal(t, "monitoring", order[len(order)-1])
	assert.ElementsMatch(t, []string{"slurm", "mesh deregistration"}, order[:2])
}

// A worker that cannot be removed fails its target and the step, which
// ends the command; a failed reconfigure fails the step "slurm", and the
// workers stay.
func TestWorkerRemove_ReportsFailures(t *testing.T) {
	nodesConf := "NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n"
	for _, tc := range []struct {
		name string
		fail func(args []string) bool
		want string
	}{
		{"remove", func(args []string) bool { return args[0] == "rm" && args[len(args)-1] == "sind-dev-worker-1" },
			`step mesh deregistration: ok
step nodes total=2 [fold]: failed (target): removing container sind-dev-worker-1: boom
  target worker-0 role=worker: ok
  target worker-1 role=worker: failed (target): removing container sind-dev-{}: boom
step slurm: ok
`},
		{"reconfigure", func(args []string) bool { return args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" },
			`step mesh deregistration: ok
step slurm: failed (target): reconfiguring slurmctld: boom
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := workerRemoveOnCall(t, nodesConf)
			m := &mock.Executor{OnCall: func(args []string, stdin string) mock.Result {
				if tc.fail(args) {
					return mock.Result{Err: errors.New("boom")}
				}
				return base(args, stdin)
			}}
			client := docker.NewClient(m)
			ctx, w := progresstest.Watch(t.Context(), t)

			err := WorkerRemove(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0", "worker-1"})

			require.Error(t, err)
			w.Finish()
			assert.Equal(t, tc.want, testutil.WithoutCalls(w.Events()))
		})
	}
}

// The event log of a delete holds the steps with the fields a display of
// the log draws them from: the kind, the name, the node and its role, how
// each span ended, and the span it was started under.
func TestDelete_EventLog(t *testing.T) {
	var buf bytes.Buffer
	log := progress.NewLog(&buf, progress.LogOptions{})
	bus := progress.NewBus(progress.BusOptions{Sinks: []progress.Sink{log}, Program: "sind"})
	ctx, command := progress.Start(progress.WithBus(t.Context(), bus), progress.KindCommand, "delete cluster")
	m := &mock.Executor{OnCall: stackFirst(newStackFake(t), deleteOnCall(t, testutil.ExitCode1(t), "dev", deleteOnCallOpts{
		containers: deletedNodes()[:2], networkExists: true, volumes: []string{"config"}, otherClusters: true,
		knownHosts: "controller.dev.sind.sind ssh-ed25519 K1\n",
	}))}
	c := docker.NewClient(m)

	require.NoError(t, Delete(ctx, c, mesh.NewManager(c, mesh.DefaultRealm), "dev"))

	command.End(nil)
	bus.Close()
	require.NoError(t, log.Close())
	names := map[string]string{}
	var got []string
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var e struct {
			Type, Span, Parent, Kind, Name, Node, Role, Status string
			Flags                                              []string
			Total                                              int
		}
		require.NoError(t, json.Unmarshal([]byte(line), &e), line)
		if e.Type == "start" {
			names[e.Span] = e.Name
		}
		if e.Kind == "call" || e.Type != "start" && e.Type != "end" {
			continue
		}
		parent := names[e.Parent]
		got = append(got, fmt.Sprintf("%s %s %q parent=%q node=%q role=%q total=%d flags=%v status=%q",
			e.Type, e.Kind, e.Name, parent, e.Node, e.Role, e.Total, e.Flags, e.Status))
	}
	assert.Contains(t, got, `start step "nodes" parent="delete cluster" node="" role="" total=2 flags=[fold] status=""`)
	assert.Contains(t, got, `start target "controller" parent="nodes" node="controller" role="controller" total=0 flags=[] status=""`)
	assert.Contains(t, got, `end target "worker-0" parent="nodes" node="worker-0" role="worker" total=0 flags=[] status="ok"`)
	assert.Contains(t, got, `end step "volumes" parent="delete cluster" node="" role="" total=1 flags=[fold] status="ok"`)
	assert.Contains(t, got, `end step "mesh deregistration" parent="delete cluster" node="" role="" total=0 flags=[] status="ok"`)
}
