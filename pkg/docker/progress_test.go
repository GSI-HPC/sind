// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each docker command is a hidden progress call named after its
// subcommand, which ends with docker's exit code when docker failed.
func TestClient_CallsAreSpans(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch args[0] {
		case "network":
			return mock.Result{Err: exitError(t, 1, "Error response from daemon: network with name x already exists\n")}
		case "kill":
			return mock.Result{Err: errors.New("no docker")}
		}
		return mock.Result{}
	}}
	c := NewClient(m)

	_, _, err := c.run(ctx, "version")
	require.NoError(t, err)
	_, _, err = c.run(ctx, "network", "create", "x")
	require.Error(t, err)
	_, _, err = c.run(ctx, "kill", "-s", "USR1", "sind-dns")
	require.Error(t, err)
	_, _, err = c.runWithStdin(ctx, strings.NewReader("tar"), "cp", "-", "sind-dns:/")
	require.NoError(t, err)

	assert.Equal(t, `call docker cp [hidden]: ok
call docker kill [hidden]: failed (target): no docker
call docker network create exit=1 [hidden]: failed (target): exit status 1: Error response from daemon: network with name x already exists
call docker version [hidden]: ok
`, w.Finish())
}

// A call is queued while it waits for a slot, and runs once it has one;
// one whose context ends while it waits ends canceled.
func TestClient_CallWaitsQueued(t *testing.T) {
	e := newBlockingExecutor()
	c := NewClient(e)
	var wg sync.WaitGroup
	for range MaxConcurrentCalls {
		wg.Go(func() { _, _, _ = c.run(t.Context(), "version") })
	}
	for range MaxConcurrentCalls {
		<-e.started
	}

	watched, w := progresstest.Watch(t.Context(), t)
	ctx, cancel := context.WithCancel(watched)
	waiting := make(chan error, 1)
	go func() {
		_, _, err := c.run(ctx, "ps")
		waiting <- err
	}()
	require.Eventually(t, func() bool { return w.Tree() != "" }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "call docker ps [hidden]: queued\n", w.Tree())
	cancel()
	require.ErrorIs(t, <-waiting, context.Canceled)

	queued := make(chan error, 1)
	go func() {
		_, _, err := c.run(watched, "info")
		queued <- err
	}()
	require.Eventually(t, func() bool { return strings.Contains(w.Tree(), "info") }, 5*time.Second, 10*time.Millisecond)
	close(e.release)
	require.NoError(t, <-queued)
	wg.Wait()
	assert.Equal(t, `call docker info [hidden]: ok
call docker ps [hidden]: canceled (canceled): context canceled
`, w.Finish())
}

func TestCallName(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "docker"},
		{[]string{"exec", "-i", "sind-dev-controller", "sh"}, "docker exec"},
		{[]string{"network", "create", "--label", "x=y", "sind-dev-net"}, "docker network create"},
		{[]string{"image", "inspect", "img:1"}, "docker image inspect"},
		{[]string{"volume", "--help"}, "docker volume"},
		{[]string{"volume"}, "docker volume"},
		{[]string{"pull", "img:1"}, "docker pull"},
	} {
		assert.Equal(t, tc.want, callName(tc.args), "%q", tc.args)
	}
}

// dataRecorder records, for each docker subcommand, whether the context it
// runs under says that its standard output is data.
type dataRecorder struct {
	mock.Executor
	data map[string]bool
}

func (e *dataRecorder) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	e.data[strings.Join(args[:min(2, len(args))], " ")] = cmdexec.StdoutIsData(ctx)
	return e.Executor.Run(ctx, name, args...)
}

// The standard output of every docker command but a pull is data, an ID,
// a name or JSON, which no row that shows lines shows; docker pull writes
// its status there.
func TestClient_StdoutIsDataButForAPull(t *testing.T) {
	e := &dataRecorder{Executor: mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{} }}, data: map[string]bool{}}
	c := NewClient(e)
	for _, args := range [][]string{{"pull", "img"}, {"image", "pull"}, {"create", "img"}, {"inspect", "x"}, {"image", "inspect"}} {
		_, _, err := c.run(t.Context(), args...)
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]bool{
		"pull img": false, "image pull": false,
		"create img": true, "inspect x": true, "image inspect": true,
	}, e.data)
}
