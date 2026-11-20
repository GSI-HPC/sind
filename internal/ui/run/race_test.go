// SPDX-License-Identifier: LGPL-3.0-or-later

package run

import (
	"context"
	"errors"
	"io"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/execopt"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// groupExec is an executor of children that go on until their process
// group gets a SIGINT, and then end as a sind that rolled back does. Each
// command has a process ID of its own, and the executor counts the SIGINTs
// of each one.
type groupExec struct {
	// interrupted is the error of a command that exited with 130.
	interrupted error

	mu      sync.Mutex
	pids    int
	signals map[int]int
	ends    map[int]chan struct{}
}

func (g *groupExec) Run(ctx context.Context, _ string, _ ...string) (string, string, error) {
	g.mu.Lock()
	g.pids++
	pid, end := g.pids, make(chan struct{})
	g.ends[pid] = end
	g.mu.Unlock()
	execopt.Started(ctx)(pid)
	select {
	case <-end:
		return "", "", g.interrupted
	case <-ctx.Done():
		return "", "killed", errors.New("signal: killed")
	}
}

func (g *groupExec) RunWithStdin(context.Context, io.Reader, string, ...string) (string, string, error) {
	panic("not used")
}

func (g *groupExec) Start(context.Context, string, ...string) (*cmdexec.Process, error) {
	panic("not used")
}

// kill is the Kill of the runner: the first SIGINT of a group ends its
// command.
func (g *groupExec) kill(pid int, _ syscall.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.signals[pid]++
	if g.signals[pid] == 1 {
		close(g.ends[pid])
	}
	return nil
}

// TestRunner_EachChildGetsOneInterrupt starts tasks, stops some of them
// twice and shuts the runner down, all at once, with the shutdown at
// another moment in each round. Whatever the order:
//   - every child gets exactly one SIGINT, from Stop, from the shutdown or
//     from its own start when the shutdown began before it: none would
//     leave it to be killed after the grace, and a second one would kill
//     sind in the middle of its rollback;
//   - the shutdown returns when every child has ended, and no task starts
//     after it.
func TestRunner_EachChildGetsOneInterrupt(t *testing.T) {
	const starters, each = 4, 3
	interrupted := exitErr(t, "130", "")
	for round := range 200 {
		g := &groupExec{interrupted: interrupted, signals: map[int]int{}, ends: map[int]chan struct{}{}}
		st := store.New()
		r := New(Config{Exec: g, Store: st, Binary: "/usr/bin/sind", Kill: g.kill})

		// The shutdown begins when this many calls of Start have returned.
		after := round % (starters*each + 1)
		returned := make(chan struct{}, starters*each)
		var started, refused sync.Map
		var wg sync.WaitGroup
		for s := range starters {
			wg.Go(func() {
				for i := range each {
					id, err := r.Start(spec)
					returned <- struct{}{}
					if err != nil {
						refused.Store([2]int{s, i}, err)
						continue
					}
					started.Store(id, true)
					if (s+i)%2 == 0 {
						// Stop it, twice: the second one is no second SIGINT.
						// The task may have ended by the shutdown already.
						for range 2 {
							if err := r.Cancel(id); err != nil && !errors.Is(err, ErrNotRunning) {
								t.Errorf("round %d: Cancel: %v", round, err)
							}
						}
					}
				}
			})
		}
		wg.Go(func() {
			for range after {
				<-returned
			}
			r.Shutdown(30 * time.Second)
		})
		wg.Wait()

		refused.Range(func(_, err any) bool {
			assert.ErrorIs(t, err.(error), ErrClosing, "round %d", round)
			return true
		})
		tasks := st.Snapshot().Tasks
		ids := 0
		started.Range(func(id, _ any) bool {
			ids++
			task, ok := st.Snapshot().Task(id.(string))
			require.True(t, ok, "round %d", round)
			assert.Equal(t, store.TaskCanceled, task.Status, "round %d: %s", round, task.Err)
			return true
		})
		assert.Len(t, tasks, ids, "round %d: a task for each Start that returned an ID", round)
		g.mu.Lock()
		assert.Equal(t, ids, g.pids, "round %d: a child for each task", round)
		for pid, n := range g.signals {
			assert.Equal(t, 1, n, "round %d: SIGINTs of child %d", round, pid)
		}
		assert.Len(t, g.signals, g.pids, "round %d: every child got its SIGINT", round)
		g.mu.Unlock()

		_, err := r.Start(spec)
		require.ErrorIs(t, err, ErrClosing, "round %d", round)
		r.mu.Lock()
		assert.Empty(t, r.running, "round %d", round)
		r.mu.Unlock()
		if t.Failed() {
			return
		}
	}
}
