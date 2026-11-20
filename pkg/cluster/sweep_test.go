// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warnings counts the warnings of a run: those for the user (sindlog.Warn)
// and the log records at the level Warn and above.
type warnings struct {
	slog.Handler
	n atomic.Int64
}

func (w *warnings) Enabled(_ context.Context, level slog.Level) bool { return level >= slog.LevelWarn }

func (w *warnings) Handle(context.Context, slog.Record) error {
	w.n.Add(1)
	return nil
}

// sweepRun runs work as one sind command on the daemon: with an executor
// and a mesh manager of its own, and at most 30 seconds, which no run
// needs. It returns the number of warnings and work's error.
func sweepRun(t *testing.T, d *fakeDaemon, work func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error) (int, error) {
	t.Helper()
	w := &warnings{}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ctx = sindlog.With(ctx, slog.New(w))
	ctx = sindlog.WithWarn(ctx, func(string) { w.n.Add(1) })
	client := docker.NewClient(d.executor())
	err := work(ctx, client, mesh.NewManager(client, mesh.DefaultRealm))
	require.NoError(t, ctx.Err(), "the run ended in time")
	return int(w.n.Load()), err
}

// sweepWait is how long a create of a sweep with a failure waits for its
// nodes: a create retries a probe that fails, so one that lost the daemon
// would wait for as long as it may. A create that reaches the limit
// though its daemon works rolls back, which the sweep takes as well.
const sweepWait = 100 * time.Millisecond

// sweepCreate creates the cluster name of createCfg on the daemon, and
// waits at most wait for its nodes, or without a limit for zero.
func sweepCreate(t *testing.T, d *fakeDaemon, name string, wait time.Duration) (int, error) {
	t.Helper()
	return sweepRun(t, d, func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
		cfg := createCfg()
		cfg.Name = name
		cfg.Wait = wait
		return CreateInRealm(ctx, client, meshMgr, cfg, CreateOptions{Interval: time.Millisecond})
	})
}

// sweepDelete deletes the cluster name on the daemon.
func sweepDelete(t *testing.T, d *fakeDaemon, name string) (int, error) {
	t.Helper()
	return sweepRun(t, d, func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
		return Delete(ctx, client, meshMgr, name)
	})
}

// sweepRealms are the realms of the sweeps: an empty one, in which the
// cluster of the sweep is the first and the last, so that its mesh comes
// and goes with it, and one with another cluster, whose mesh stays.
var sweepRealms = []struct {
	name  string
	other bool
}{
	{"only cluster of the realm", false},
	{"realm with another cluster", true},
}

// sweepRealm returns a daemon with the realm: with other, the cluster
// "keep" is there.
func sweepRealm(t *testing.T, other bool) *fakeDaemon {
	t.Helper()
	d := newFakeDocker(t)
	if other {
		_, err := sweepCreate(t, d, "keep", 0)
		require.NoError(t, err)
	}
	return d
}

// first returns the first of the calls that failed, shortened.
func first(failed []string) string {
	call, _, _ := strings.Cut(failed[0], "\n")
	return fmt.Sprintf("%.120s", call)
}

// A create in which one docker call fails, whichever it is, either rolls
// back to the realm as it was, or goes on to the cluster of a create
// without a failure. A create that loses the daemon at any call fails, and
// a delete of the cluster then returns the realm to what it was.
//
// The calls of a create run in part at the same time, so the k-th call is
// not the same in every run: each run of the test sweeps the calls in
// another order.
func TestCreate_FaultSweep(t *testing.T) {
	for _, realm := range sweepRealms {
		t.Run(realm.name, func(t *testing.T) {
			d := sweepRealm(t, realm.other)
			before, start := d.state(), d.count()
			_, err := sweepCreate(t, d, "dev", 0)
			require.NoError(t, err)
			created, calls := d.state(), d.count()-start
			require.NotEmpty(t, stateDiff(before, created))

			rolledBack, wentOn := 0, 0
			for k := 1; k <= calls; k++ {
				d := sweepRealm(t, realm.other)
				d.failAt(k, false)
				_, err := sweepCreate(t, d, "dev", sweepWait)
				failed := d.heal()
				if len(failed) == 0 {
					continue // this run made fewer calls
				}
				if err != nil {
					rolledBack++
					assert.Empty(t, stateDiff(before, d.state()), "call %d fails (%s): the create failed and rolled back: %v", k, first(failed), err)
				} else {
					wentOn++
					assert.Empty(t, stateDiff(created, d.state()), "call %d fails (%s): the create went on", k, first(failed))
				}

				d = sweepRealm(t, realm.other)
				d.failAt(k, true)
				_, err = sweepCreate(t, d, "dev", sweepWait)
				what := fmt.Sprintf("the daemon is gone from call %d on (%s)", k, first(d.heal()))
				require.ErrorContains(t, err, errFault.Error(), what)
				_, err = sweepDelete(t, d, "dev")
				require.NoError(t, err, "%s: the delete after it", what)
				assert.Empty(t, stateDiff(before, d.state()), "%s: the delete after it", what)
			}
			assert.Positive(t, rolledBack, "some failures end the create")
			assert.Positive(t, wentOn, "some failures do not: a probe that the create retries")
		})
	}
}

// A delete in which one docker call fails, or that loses the daemon at any
// call, leaves a realm that the next delete returns to what it was before
// the cluster, without a warning. A delete that reports success without a
// warning has done so itself (CleanAfterSuccess in spec/tla/DeleteRetry.tla,
// here on the code).
func TestDelete_FaultSweep(t *testing.T) {
	for _, realm := range sweepRealms {
		t.Run(realm.name, func(t *testing.T) {
			world := func() *fakeDaemon {
				d := sweepRealm(t, realm.other)
				_, err := sweepCreate(t, d, "dev", 0)
				require.NoError(t, err)
				return d
			}
			before := sweepRealm(t, realm.other).state()
			d := world()
			start := d.count()
			warned, err := sweepDelete(t, d, "dev")
			require.NoError(t, err)
			require.Zero(t, warned)
			require.Empty(t, stateDiff(before, d.state()))
			calls := d.count() - start

			swept := 0
			for k := 1; k <= calls; k++ {
				for _, onward := range []bool{false, true} {
					d := world()
					d.failAt(k, onward)
					warned, err := sweepDelete(t, d, "dev")
					failed := d.heal()
					if len(failed) == 0 {
						continue // this run made fewer calls
					}
					what := fmt.Sprintf("call %d fails (%s)", k, first(failed))
					if onward {
						what += ", and every call after it"
					}
					swept++
					if err == nil && warned == 0 {
						assert.Empty(t, stateDiff(before, d.state()), "%s: the delete reported success", what)
					}
					warned, err = sweepDelete(t, d, "dev")
					require.NoError(t, err, "%s: the next delete", what)
					assert.Zero(t, warned, "%s: the next delete", what)
					assert.Empty(t, stateDiff(before, d.state()), "%s: the next delete", what)
				}
			}
			assert.Positive(t, swept, "some calls failed")
		})
	}
}
