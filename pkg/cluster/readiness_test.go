// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadiness_NoLimit(t *testing.T) {
	rd := &readiness{}
	nctx, cancel := rd.nodeContext(t.Context())
	defer cancel()
	assert.Equal(t, t.Context(), nctx)
	rctx, cancelReady := rd.context(t.Context())
	defer cancelReady()
	assert.Equal(t, t.Context(), rctx)
	assert.False(t, rd.expired())
}

func TestReadiness_Limit(t *testing.T) {
	rd := &readiness{wait: time.Hour}
	before := time.Now()
	first, cancelFirst := rd.nodeContext(t.Context())
	defer cancelFirst()
	second, cancelSecond := rd.nodeContext(t.Context())
	defer cancelSecond()

	d1, ok := first.Deadline()
	require.True(t, ok)
	assert.False(t, d1.Before(before.Add(time.Hour)))
	d2, _ := second.Deadline()
	// The steps after setupNodes end with the last node's wait.
	rctx, cancelReady := rd.context(t.Context())
	defer cancelReady()
	d, ok := rctx.Deadline()
	require.True(t, ok)
	assert.Equal(t, d2, d)
	assert.False(t, rd.expired())

	// A context cancelled before its deadline did not expire.
	cancelFirst()
	assert.False(t, rd.expired())
}

func TestReadiness_Expired(t *testing.T) {
	rd := &readiness{wait: time.Millisecond}
	nctx, cancel := rd.nodeContext(t.Context())
	defer cancel()
	<-nctx.Done()
	require.ErrorIs(t, nctx.Err(), context.DeadlineExceeded)
	assert.True(t, rd.expired())
}

func TestReadiness_ParentCancelledIsNotExpiry(t *testing.T) {
	ctx, cancelParent := context.WithCancel(t.Context())
	rd := &readiness{wait: time.Hour}
	_, cancel := rd.nodeContext(ctx)
	defer cancel()
	cancelParent()
	assert.False(t, rd.expired())
}

// neverReadyOnCall is happyOnCall with munge never active on worker-0.
func neverReadyOnCall(t *testing.T) func([]string, string) mock.Result {
	return happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && args[1] == "sind-dev-worker-0" && strings.HasSuffix(strings.Join(args, " "), "is-active munge") {
			return mock.Result{Stdout: "activating\n", Err: notFoundErr(t)}, true
		}
		return mock.Result{}, false
	})
}

func TestCreate_WaitLimit(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = neverReadyOnCall(t)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cfg := createCfg()
	cfg.Wait = 500 * time.Millisecond
	_, err := Create(t.Context(), client, meshMgr, cfg, time.Millisecond)

	require.ErrorIs(t, err, ErrNotReady)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "cluster dev not ready within 500ms: waiting for worker-0")
	assert.Contains(t, err.Error(), "last probe error: probe munge: munge not ready: activating")
	// The rollback ran: preflight (twice: the cluster's containers and
	// those on the mesh and the cluster network), diagnostics and
	// deletion list the containers.
	var psCalls int
	for _, c := range m.Calls {
		if c.Args[0] == "ps" {
			psCalls++
		}
	}
	assert.Equal(t, 4, psCalls)
}

func TestCreate_WaitLimitAfterNodes(t *testing.T) {
	// The steps after the nodes are ready share the limit.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is DOWN\n"}, true
		}
		return mock.Result{}, false
	})
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cfg := createCfg()
	cfg.Wait = 500 * time.Millisecond
	_, err := Create(t.Context(), client, meshMgr, cfg, time.Millisecond)

	require.ErrorIs(t, err, ErrNotReady)
	assert.Contains(t, err.Error(), "controller is DOWN")
}

func TestCreate_InterruptIsNotWaitLimit(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = neverReadyOnCall(t)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	cfg := createCfg()
	cfg.Wait = time.Hour
	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotReady)
}

// slowCreate delays docker create of one container outside the mock's
// lock, as a pull would.
type slowCreate struct {
	*mock.Executor
	name  string
	delay time.Duration
}

func (s slowCreate) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if args[0] == "create" && strings.Contains(strings.Join(args, " "), "--name "+s.name) {
		time.Sleep(s.delay)
	}
	return s.Executor.Run(ctx, name, args...)
}

func (s slowCreate) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	return s.Executor.RunWithStdin(ctx, stdin, name, args...)
}

func TestCreate_WaitLimitExcludesContainerCreation(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	m := &mock.Executor{}
	m.OnCall = happyOnCall(t, notFoundErr(t), nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(slowCreate{Executor: m, name: "sind-dev-worker-0", delay: 1500 * time.Millisecond})
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cfg := createCfg()
	cfg.Wait = time.Second
	_, err := Create(t.Context(), client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)
}

func TestWorkerAdd_WaitLimit(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	onCall := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "sind-dev-worker-1" && strings.HasSuffix(strings.Join(args, " "), "is-active munge") {
			return mock.Result{Stdout: "activating\n", Err: notFoundErr(t)}
		}
		return onCall(args, stdin)
	}
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 1, Wait: 200 * time.Millisecond}, time.Millisecond)

	require.ErrorIs(t, err, ErrNotReady)
	assert.Contains(t, err.Error(), "workers not ready within 200ms: waiting for worker-1")
	var removed bool
	for _, c := range m.Calls {
		if c.Args[0] == "rm" && strings.Contains(strings.Join(c.Args, " "), "sind-dev-worker-1") {
			removed = true
		}
	}
	assert.True(t, removed, "the new worker should be removed")
}
