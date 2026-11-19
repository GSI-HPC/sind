// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitContainers lists cluster dev: a controller in controllerState, a
// managed db node, the managed worker-0 and the unmanaged worker-1.
func waitContainers(controllerState string) string {
	return testutil.NDJSON(
		testutil.PsEntry{ID: "c1", Names: "sind-dev-controller", State: controllerState, Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller"},
		testutil.PsEntry{ID: "c2", Names: "sind-dev-db", State: "running", Image: "img:1", Labels: "sind.cluster=dev,sind.role=db"},
		testutil.PsEntry{ID: "c3", Names: "sind-dev-worker-0", State: "running", Image: "img:1", Labels: "sind.cluster=dev,sind.role=worker"},
		testutil.PsEntry{ID: "c4", Names: "sind-dev-worker-1", State: "running", Image: "img:1", Labels: "sind.cluster=dev,sind.role=worker,sind.managed=false"},
	)
}

// waitOnCall answers the calls of WaitNodes for the nodes of
// waitContainers, all of them ready and worker-0 registered. handle can
// answer a call first.
func waitOnCall(t *testing.T, handle func(args []string) (mock.Result, bool)) func([]string, string) mock.Result {
	t.Helper()
	return func(args []string, _ string) mock.Result {
		if handle != nil {
			if r, ok := handle(args); ok {
				return r
			}
		}
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "ps":
			return mock.Result{Stdout: waitContainers("running")}
		case args[0] == "inspect":
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", nil)}
		case strings.Contains(joined, "is-system-running"):
			return mock.Result{Stdout: "running\n"}
		case strings.Contains(joined, "/dev/tcp"):
			return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
		case args[0] == "exec" && args[2] == "systemctl" && args[3] == "is-active":
			return mock.Result{Stdout: "active\n"}
		case args[0] == "exec" && args[2] == "scontrol":
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
		case isSinfo(args):
			return sinfoIdle(args)
		}
		t.Errorf("unexpected call: %v", args)
		return mock.Result{Err: errors.New("unexpected call")}
	}
}

// timeouts matches the bounds of the waits in a span tree, which depend on
// how long the wait took so far.
var timeouts = regexp.MustCompile(` timeout=[0-9hms.]+`)

func TestWaitNodes(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	m := &mock.Executor{OnCall: waitOnCall(t, nil)}

	err := WaitNodes(ctx, docker.NewClient(m), "sind", "dev", []string{"controller", "db", "worker-0", "worker-1"}, time.Minute, time.Millisecond)

	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `step readiness total=4 [fold]: ok
  target controller role=controller: ok
    wait ready: ok
  target db role=db: ok
    wait ready: ok
  target worker-[0-1] role=worker: ok
    wait ready: ok
  wait ready: ok
`, timeouts.ReplaceAllString(testutil.WithoutCalls(w.Events()), ""))
	var sinfo [][]string
	for _, c := range m.Calls {
		if isSinfo(c.Args) {
			sinfo = append(sinfo, c.Args)
		}
		assert.NotEqual(t, []string{"exec", "sind-dev-db", "systemctl", "is-active", "mariadb"}, c.Args, "sind does not wait for mariadb")
	}
	require.Len(t, sinfo, 1, "one sinfo for the managed workers")
	assert.Equal(t, "sind-dev-controller", sinfo[0][1])
	assert.Equal(t, "--nodes=worker-0", sinfo[0][len(sinfo[0])-1])
}

func TestWaitNodes_Twice(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	m := &mock.Executor{OnCall: waitOnCall(t, nil)}

	err := WaitNodes(ctx, docker.NewClient(m), "sind", "dev", []string{"worker-0", "worker-0"}, time.Minute, time.Millisecond)

	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `step readiness total=1 [fold]: ok
  target worker-0 role=worker: ok
    wait ready: ok
  wait ready: ok
`, timeouts.ReplaceAllString(testutil.WithoutCalls(w.Events()), ""), "a node named twice is waited for once")
	for _, c := range m.Calls {
		if isSinfo(c.Args) {
			assert.Equal(t, "--nodes=worker-0", c.Args[len(c.Args)-1])
		}
	}
}

func TestWaitNodes_NoNodes(t *testing.T) {
	var m mock.Executor
	require.NoError(t, WaitNodes(t.Context(), docker.NewClient(&m), "sind", "dev", nil, 0, 0))
	assert.Empty(t, m.Calls)
}

func TestWaitNodes_NotFound(t *testing.T) {
	m := &mock.Executor{OnCall: waitOnCall(t, nil)}
	err := WaitNodes(t.Context(), docker.NewClient(m), "sind", "dev", []string{"worker-9"}, 0, time.Millisecond)
	require.ErrorIs(t, err, ErrNodeNotFound)

	m = &mock.Executor{OnCall: waitOnCall(t, func(args []string) (mock.Result, bool) {
		return mock.Result{Err: errors.New("daemon down")}, args[0] == "ps"
	})}
	err = WaitNodes(t.Context(), docker.NewClient(m), "sind", "dev", []string{"worker-0"}, 0, time.Millisecond)
	require.ErrorContains(t, err, "daemon down")
}

// Managed workers cannot register without a running controller: the wait
// fails before it starts.
func TestWaitNodes_NoController(t *testing.T) {
	m := &mock.Executor{OnCall: waitOnCall(t, func(args []string) (mock.Result, bool) {
		return mock.Result{Stdout: waitContainers("exited")}, args[0] == "ps"
	})}

	err := WaitNodes(t.Context(), docker.NewClient(m), "sind", "dev", []string{"worker-0", "worker-1"}, 0, time.Millisecond)

	require.EqualError(t, err, `no controller of cluster "dev" runs: the workers worker-0 cannot register with slurmctld`)
	assert.Len(t, m.Calls, 1)

	// Unmanaged workers need none.
	require.NoError(t, WaitNodes(t.Context(), docker.NewClient(m), "sind", "dev", []string{"worker-1"}, 0, time.Millisecond))
}

func TestWaitNodes_WaitLimit(t *testing.T) {
	for name, handle := range map[string]func([]string) (mock.Result, bool){
		"node": func(args []string) (mock.Result, bool) {
			return mock.Result{Stdout: "starting\n"}, args[1] == "sind-dev-worker-0" && strings.Contains(strings.Join(args, " "), "is-system-running")
		},
		"registration": func(args []string) (mock.Result, bool) {
			return mock.Result{Stdout: "worker-0|idle*|0/0/2/2|2048|all*|none\n"}, isSinfo(args)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := &mock.Executor{OnCall: waitOnCall(t, handle)}
			err := WaitNodes(t.Context(), docker.NewClient(m), "sind", "dev", []string{"worker-0"}, 100*time.Millisecond, time.Millisecond)

			require.ErrorIs(t, err, ErrNotReady)
			assert.ErrorContains(t, err, `nodes of cluster "dev" not ready within 100ms: `)
			assert.ErrorContains(t, err, "timed out; last probe error")
		})
	}
}

// An interrupt is no wait limit, and neither is a failure.
func TestWaitNodes_NotWaitLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	m := &mock.Executor{OnCall: waitOnCall(t, func(args []string) (mock.Result, bool) {
		return mock.Result{Stdout: "starting\n"}, strings.Contains(strings.Join(args, " "), "is-system-running")
	})}
	err := WaitNodes(ctx, docker.NewClient(m), "sind", "dev", []string{"worker-0"}, time.Hour, time.Millisecond)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotReady)

	m = &mock.Executor{OnCall: waitOnCall(t, func(args []string) (mock.Result, bool) {
		return mock.Result{Stdout: "worker-0|inval|0/0/2/2|2048|all*|Low RealMemory\n"}, isSinfo(args)
	})}
	err = WaitNodes(t.Context(), docker.NewClient(m), "sind", "dev", []string{"worker-0"}, 0, time.Millisecond)
	require.EqualError(t, err, "workers' registration with slurmctld: probe registered: slurmctld rejected the registration of worker-0 (inval: Low RealMemory)")
}

func TestNodeProbes(t *testing.T) {
	names := func(probes []probe.Probe) []string {
		var out []string
		for _, p := range probes {
			out = append(out, p.Name)
		}
		return out
	}
	assert.Equal(t, []string{"container", "systemd", "munge", "sshd", "slurmd"}, names(nodeProbes(config.RoleWorker, docker.Labels{})))
	assert.Equal(t, []string{"container", "systemd", "munge", "sshd"}, names(nodeProbes(config.RoleWorker, docker.Labels{LabelManaged: "false"})))
	assert.Equal(t, []string{"container", "systemd", "munge", "sshd", "slurmdbd"}, names(nodeProbes(config.RoleDB, docker.Labels{})))
	assert.Equal(t, []string{"container", "systemd", "sshd", "sackd"}, names(nodeProbes(config.RoleSubmitter, docker.Labels{LabelIdentity: string(config.IdentityClientIDs)})))
	assert.True(t, slices.ContainsFunc(nodeProbes(config.RoleController, docker.Labels{}), func(p probe.Probe) bool { return p.Name == "slurmctld" }))
}
