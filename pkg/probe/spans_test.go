// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"errors"
	"testing"
	"time"

	spans "github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scripted returns a probe named name that fails as long as fails says.
func scripted(name string, fails func() bool) Probe {
	return Probe{Name: name, Check: func(context.Context, *docker.Client, docker.ContainerName) error {
		if fails() {
			return errors.New(name + " not ready")
		}
		return nil
	}}
}

// messages returns the messages of the updates of the wait in events.
func messages(events []spans.Event) []string {
	var out []string
	for _, e := range events {
		if e.Kind == spans.KindWait && e.Type == spans.TypeUpdate {
			out = append(out, e.Message)
		}
	}
	return out
}

// The wait is the progress wait "ready": its message names the probe that
// fails, once each time that changes, not each round.
func TestUntilReady_WaitNamesTheFailingProbe(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	round := 0
	probes := []Probe{
		scripted("systemd", func() bool { round++; return round <= 3 }),
		scripted("sshd", func() bool { return round <= 5 }),
	}

	require.NoError(t, UntilReady(ctx, nil, testContainer, probes, time.Millisecond))

	assert.Equal(t, []string{"systemd", "sshd"}, messages(w.Events()))
	assert.Equal(t, "wait ready message=sshd: ok\n", w.Finish())
}

// A wait under a deadline is bounded by the time left until it, in whole
// seconds.
func TestUntilReady_WaitIsBoundedByTheDeadline(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	round := 0

	err := UntilReady(ctx, nil, testContainer, []Probe{scripted("munge", func() bool { round++; return round < 3 })}, time.Millisecond)

	require.NoError(t, err)
	assert.Equal(t, "wait ready timeout=1m0s message=munge: ok\n", w.Finish())
}

// A deadline that has passed bounds the wait by nothing, and the wait it
// ends times out.
func TestUntilReady_PassedDeadline(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()

	err := UntilReady(ctx, nil, testContainer, []Probe{scripted("munge", func() bool { return true })}, time.Millisecond)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, "wait ready message=munge: failed (timeout): node sind-dev-controller not ready: context deadline exceeded; last probe error: probe munge: munge not ready\n", w.Finish())
}

// A wait that ends while a probe runs kills the probe's docker call, which
// then fails with "signal: killed": the wait reports the probe that failed
// before, as its error and as its message, not the killed call. A probe
// that tells the node will never be ready still ends it with its error.
func TestUntilReady_EndKeepsTheFailingProbe(t *testing.T) {
	for _, tc := range []struct {
		name, killed, want, failing string
		class                       spans.Class
	}{
		{"killed", "", "node sind-dev-controller not ready: context canceled; last probe error: probe munge: munge not ready", "munge", spans.ClassCanceled},
		{"terminal", inspectJSON("exited"), "node sind-dev-controller not ready: probe container: container sind-dev-controller is exited (exit code 0)", "container", spans.ClassTarget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, w := progresstest.Watch(t.Context(), t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			inspects := 0
			m := mock.Executor{OnCall: func([]string, string) mock.Result {
				inspects++
				if inspects == 1 {
					return mock.Result{Stdout: inspectJSON("running")}
				}
				cancel()
				if tc.killed == "" {
					return mock.Result{Err: errors.New("signal: killed")}
				}
				return mock.Result{Stdout: tc.killed}
			}}
			probes := []Probe{
				{Name: "container", Check: ContainerRunning},
				scripted("munge", func() bool { return true }),
			}

			err := UntilReady(ctx, docker.NewClient(&m), testContainer, probes, time.Millisecond)

			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			w.Finish()
			var end spans.Event
			for _, e := range w.Events() {
				if e.Kind == spans.KindWait && e.Type == spans.TypeEnd {
					end = e
				}
			}
			assert.Equal(t, tc.failing, end.Message)
			assert.Equal(t, tc.class, end.Class)
			assert.Equal(t, tc.want, end.Err)
		})
	}
}

// A probe that hangs until the wait ends, after the probe that failed
// before has passed, is the check the wait's message names at its end, as
// its error does.
func TestUntilReady_WaitNamesTheProbeThatHangs(t *testing.T) {
	watched, w := progresstest.Watch(t.Context(), t)
	ctx := newEndsOnDemand(watched)
	round := 0
	probes := []Probe{
		scripted("container", func() bool { round++; return round == 1 }),
		{Name: "munge", Check: hangsUntilEnd(ctx)},
	}

	err := UntilReady(ctx, nil, testContainer, probes, time.Millisecond)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, []string{"container", "munge"}, messages(w.Events()))
	assert.Equal(t, "wait ready message=munge: failed (timeout): node sind-dev-controller not ready: context deadline exceeded; last probe error: probe munge: signal: killed\n", w.Finish())
}
