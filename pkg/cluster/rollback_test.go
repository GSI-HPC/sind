// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rollbackStep returns the end of the step "rollback" in events, and
// checks that it is the last step under the command to start, once every
// other step has ended, and that it ends before the command.
func rollbackStep(t *testing.T, events []progress.Event) progress.Event {
	t.Helper()
	var command progress.SpanID
	var start, end progress.Event
	var lastEnd, commandEnd uint64
	for _, e := range events {
		switch {
		case e.Kind == progress.KindCommand && e.Type == progress.TypeStart:
			command = e.Span
		case e.Kind == progress.KindCommand && e.Type == progress.TypeEnd:
			commandEnd = e.Seq
		case e.Kind != progress.KindStep || e.Parent != command:
		case e.Name == "rollback" && e.Type == progress.TypeStart:
			start = e
		case e.Name == "rollback" && e.Type == progress.TypeEnd:
			end = e
		case e.Type == progress.TypeEnd:
			lastEnd = e.Seq
		}
	}
	require.NotZero(t, start.Seq, "no step rollback under the command")
	assert.Less(t, lastEnd, start.Seq, "the rollback starts once the other steps have ended")
	assert.Less(t, end.Seq, commandEnd, "the rollback ends before the command")
	return end
}

// underCommand runs work under the command "create" of a progresstest Bus
// when watched, and without a Bus otherwise, and returns the events, none
// unwatched, and work's error.
func underCommand(t *testing.T, watched bool, work func(ctx context.Context) error) ([]progress.Event, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if !watched {
		return nil, work(ctx)
	}
	ctx, w := progresstest.Watch(ctx, t)
	ctx, command := progress.Start(ctx, progress.KindCommand, "create")
	err := work(ctx)
	command.End(err)
	w.Finish()
	return w.Events(), err
}

// The rollback of a Create that failed is the step "rollback" under the
// command, after the step that failed: ok when it undid everything, and
// failed with what it could not undo otherwise. Create returns the same
// error with and without the step.
func TestCreate_RollbackIsAStep(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		status     progress.Status
	}{
		{"undone", "", progress.StatusOK},
		{"mesh left", "rolling back: removing the mesh: removing DNS container: docker daemon unavailable", progress.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(ctx context.Context) error {
				inCleanup := false
				var m mock.Executor
				m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
					if args[0] == "exec" && len(args) > 3 && args[1] == "sind-dev-controller" && args[2] == "systemctl" && args[3] == "enable" {
						inCleanup = true
						return mock.Result{Err: errors.New("systemctl failed")}, true
					}
					if tc.want != "" && inCleanup && args[0] == "rm" && args[len(args)-1] == "sind-dns" {
						return mock.Result{Err: errors.New("docker daemon unavailable")}, true
					}
					return mock.Result{}, false
				})
				client := docker.NewClient(&m)
				meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
				_ = meshMgr.EnsureMeshNetwork(context.WithoutCancel(ctx)) // creates the network, and fails on the inspect after
				require.True(t, meshMgr.Created())
				_, err := Create(ctx, client, meshMgr, createCfg(), time.Millisecond)
				return err
			}

			_, plain := underCommand(t, false, run)
			events, err := underCommand(t, true, run)

			require.Error(t, err)
			assert.Equal(t, plain.Error(), err.Error())
			end := rollbackStep(t, events)
			assert.Equal(t, tc.status, end.Status)
			assert.Equal(t, tc.want, end.Err)
			if tc.want != "" {
				assert.True(t, strings.HasSuffix(err.Error(), "\n"+tc.want), err.Error())
			}
		})
	}
}

// A Create that fails before it made anything has nothing to roll back,
// and no step "rollback".
func TestCreate_NoRollbackNoStep(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	var m mock.Executor
	client := docker.NewClient(&m)
	cfg := createCfg()
	cfg.Name = ""

	_, err := Create(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), cfg, time.Millisecond)

	require.Error(t, err)
	assert.Empty(t, w.Finish())
}

// The rollback of a WorkerAdd that failed is the step "rollback" too, failed
// with what it could not undo, and WorkerAdd returns the same error with
// and without it.
func TestWorkerAdd_RollbackIsAStep(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		status     progress.Status
	}{
		{"undone", "", progress.StatusOK},
		{"nodes.conf left", "rolling back: restoring sind-nodes.conf: reconfiguring slurmctld: revert failed", progress.StatusFailed},
		{"both left", "rolling back: restoring sind-nodes.conf: reconfiguring slurmctld: revert failed\n" +
			"rolling back: removing container sind-dev-worker-1: rm failed", progress.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(ctx context.Context) error {
				var m mock.Executor
				base := workerAddOnCall(t)
				slurmdFailed := false
				m.OnCall = func(args []string, stdin string) mock.Result {
					switch {
					case args[0] == "exec" && len(args) > 3 && args[1] == "sind-dev-worker-1" && args[2] == "systemctl" && args[3] == "enable":
						slurmdFailed = true
						return mock.Result{Err: fmt.Errorf("slurmd failed")}
					case slurmdFailed && tc.want != "" && args[0] == "exec" && args[1] == "sind-dev-controller" && args[2] == "scontrol":
						return mock.Result{Err: fmt.Errorf("revert failed")}
					case slurmdFailed && strings.Contains(tc.want, "rm failed") && args[0] == "rm":
						return mock.Result{Err: fmt.Errorf("rm failed")}
					}
					return base(args, stdin)
				}
				client := docker.NewClient(&m)
				_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{ClusterName: "dev", Count: 1}, time.Millisecond)
				return err
			}

			_, plain := underCommand(t, false, run)
			events, err := underCommand(t, true, run)

			require.Error(t, err)
			assert.Equal(t, plain.Error(), err.Error())
			end := rollbackStep(t, events)
			assert.Equal(t, tc.status, end.Status)
			assert.Equal(t, strings.ReplaceAll(tc.want, "\n", `\n`), end.Err)
		})
	}
}

// A rollback that fails once the command was interrupted fails, though
// the Bus's fallback calls every failure after the interrupt canceled: it
// runs on after the interrupt, so what it left behind is no work the
// interrupt stopped. One that ran out of its time timed out.
func TestStartRollback_FailsAfterAnInterrupt(t *testing.T) {
	interrupt, cancel := context.WithCancel(t.Context())
	ctx, w := progresstest.Watch(interrupt, t, progresstest.Classify(func(error) progress.Class {
		if interrupt.Err() != nil {
			return progress.ClassCanceled
		}
		return progress.ClassNone
	}))
	cancel()

	rctx, end := startRollback(ctx)
	require.NoError(t, rctx.Err(), "the rollback runs on after the interrupt")
	end(errors.New("removing cluster resources: docker rm: exit status 1"))
	_, end = startRollback(ctx)
	end(fmt.Errorf("removing the mesh: %w", context.DeadlineExceeded))

	assert.Equal(t, `step rollback: failed (target): removing cluster resources: docker rm: exit status 1
step rollback: failed (timeout): removing the mesh: context deadline exceeded
`, w.Finish())
}
