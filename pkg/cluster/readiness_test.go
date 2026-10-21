// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
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

// neverReady is a happyOnCall override under which munge never gets
// active on worker-0.
func neverReady(t *testing.T) func([]string, string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		return mock.Result{Stdout: "activating\n", Err: testutil.ExitCode1(t)},
			args[0] == "exec" && args[1] == "sind-dev-worker-0" && strings.HasSuffix(strings.Join(args, " "), "is-active munge")
	}
}

func TestCreate_WaitLimit(t *testing.T) {
	cfg := createCfg()
	cfg.Wait = 500 * time.Millisecond
	m, _, err := runCreate(t.Context(), t, cfg, false, neverReady(t))

	require.ErrorIs(t, err, ErrNotReady)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "cluster \"dev\" not ready within 500ms: node worker-0: timed out; last probe error")
	assert.Contains(t, err.Error(), "last probe error: probe munge: munge not ready: activating")
	// The rollback ran: preflight (twice: the cluster's containers and
	// those on the mesh and the cluster network), the connect of external
	// containers, diagnostics and deletion list the containers.
	assert.Equal(t, 5, countCalls(m.Calls, "ps"))
}

func TestCreate_WaitLimitAfterNodes(t *testing.T) {
	// The steps after the nodes are ready share the limit.
	cfg := createCfg()
	cfg.Wait = 500 * time.Millisecond
	_, _, err := runCreate(t.Context(), t, cfg, false, func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "exec" && len(args) > 2 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is DOWN\n"}, true
		}
		return mock.Result{}, false
	})

	require.ErrorIs(t, err, ErrNotReady)
	assert.Contains(t, err.Error(), "controller is DOWN")
}

func TestCreate_InterruptIsNotWaitLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	cfg := createCfg()
	cfg.Wait = time.Hour
	_, _, err := runCreate(ctx, t, cfg, false, neverReady(t))

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
	m.OnCall = happyOnCall(t, nil)
	m.OnStart = pipes.OnStart
	client := docker.NewClient(slowCreate{Executor: m, name: "sind-dev-worker-0", delay: 1500 * time.Millisecond})
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cfg := createCfg()
	cfg.Wait = time.Second
	_, err := Create(t.Context(), client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)
}

func TestWorkerAdd_WaitLimit(t *testing.T) {
	onCall := workerAddOnCall(t)
	m, _, err := addWorkers(t, func(args []string, stdin string) mock.Result {
		if args[0] == "exec" && args[1] == "sind-dev-worker-1" && strings.HasSuffix(strings.Join(args, " "), "is-active munge") {
			return mock.Result{Stdout: "activating\n", Err: testutil.ExitCode1(t)}
		}
		return onCall(args, stdin)
	}, WorkerAddOptions{ClusterName: "dev", Count: 1, Wait: 200 * time.Millisecond})
	require.ErrorIs(t, err, ErrNotReady)
	assert.Contains(t, err.Error(), "workers of cluster \"dev\" not ready within 200ms: node worker-1: timed out; last probe error")
	assert.Equal(t, 1, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"), "the new worker should be removed")
}
