// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClusterImages(t *testing.T) {
	cfg := &config.Cluster{Nodes: []config.Node{
		{Role: config.RoleWorker, Image: "worker:1"},
		{Role: config.RoleController, Image: "ctrl:1"},
		{Role: config.RoleWorker, Image: "worker:1"},
		{Role: config.RoleDB, Image: "ctrl:1"},
		{Role: config.RoleSubmitter},
	}}
	assert.Equal(t, []string{"ctrl:1", "worker:1"}, clusterImages(cfg))
}

func TestImagePull_WaitEndsWithContext(t *testing.T) {
	p := &imagePull{done: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, p.wait(ctx), context.Canceled)
}

// pullOnCall answers docker pull with result and records nothing else.
func pullOnCall(result mock.Result) func(args []string, _ string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		if args[0] == "pull" {
			return result, true
		}
		return mock.Result{}, false
	}
}

func TestCreate_PullsEachImageOnce(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), pullOnCall(mock.Result{}))
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cfg := createCfg()
	cfg.Nodes = append(cfg.Nodes, config.Node{Role: config.RoleWorker, Count: 2, Image: "other:2", CPUs: 1, Memory: "1g", TmpSize: "1g"})
	cfg.Pull = true
	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)

	var pulls []string
	lastPull, firstUse := -1, -1
	for i, c := range m.Calls {
		a := c.Args
		assert.NotContains(t, a, "--pull", "%v", a)
		switch {
		case a[0] == "pull":
			pulls = append(pulls, strings.Join(a, " "))
			lastPull = i
		case (a[0] == "run" || a[0] == "create") && firstUse < 0:
			firstUse = i
		}
	}
	slices.Sort(pulls)
	assert.Equal(t, []string{"pull --quiet img:1", "pull --quiet other:2"}, pulls)
	assert.Less(t, lastPull, firstUse, "every image is pulled before a container runs it")
}

func TestCreate_PullFails(t *testing.T) {
	// The pull fails before anything is created; the version check, the
	// resources and the CVMFS check all wait for it.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), pullOnCall(mock.Result{Err: fmt.Errorf("manifest unknown")}))
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	cfg := createCfg()
	cfg.Pull = true
	cfg.Storage.CVMFS = true
	_, err := Create(t.Context(), client, meshMgr, cfg, time.Millisecond)
	require.EqualError(t, err, "pulling img:1: manifest unknown")
	// The network and volumes may exist (and are rolled back), but nothing
	// ran the image.
	for _, c := range m.Calls {
		a := c.Args
		assert.False(t, a[0] == "run" || a[0] == "create" || a[0] == "plugin", "%v", a)
	}
}

func TestWorkerAdd_PullsOnce(t *testing.T) {
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	onCall := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "pull" {
			return mock.Result{}
		}
		return onCall(args, stdin)
	}
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{ClusterName: "dev", Count: 2, Image: "img:1", Pull: true}, time.Millisecond)
	require.NoError(t, err)

	var pulls int
	for _, c := range m.Calls {
		assert.NotContains(t, c.Args, "--pull", "%v", c.Args)
		if c.Args[0] == "pull" {
			pulls++
			assert.Equal(t, []string{"pull", "--quiet", "img:1"}, c.Args)
		}
	}
	assert.Equal(t, 1, pulls)
}

func TestWorkerAdd_PullFails(t *testing.T) {
	var m mock.Executor
	onCall := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "pull" {
			return mock.Result{Err: fmt.Errorf("manifest unknown")}
		}
		return onCall(args, stdin)
	}
	client := docker.NewClient(&m)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := WorkerAdd(t.Context(), client, mgr, WorkerAddOptions{ClusterName: "dev", Image: "img:1", Pull: true}, time.Millisecond)
	require.EqualError(t, err, "pulling img:1: manifest unknown")
	for _, c := range m.Calls {
		assert.NotEqual(t, "create", c.Args[0])
	}
}

// imageExistsOnCall answers ImageExists: the daemon has every image but
// missing, and docker pull succeeds.
func imageExistsOnCall(t *testing.T, missing ...string) func(args []string, _ string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		switch {
		case isImageExists(args) && slices.Contains(missing, args[4]):
			return mock.Result{Stderr: "Error: No such image: " + args[4] + "\n", Err: notFoundErr(t)}, true
		case args[0] == "pull":
			return mock.Result{}, true
		}
		return mock.Result{}, false
	}
}

func TestCreate_PullsMissingImages(t *testing.T) {
	// Without --pull, the images the daemon does not have are pulled once,
	// before any container runs one, so that no node's docker create pulls.
	pipes := &mock.Pipes{}
	defer pipes.CloseAll()

	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), imageExistsOnCall(t, "other:2"))
	m.OnStart = pipes.OnStart
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cfg := createCfg()
	cfg.Nodes = append(cfg.Nodes, config.Node{Role: config.RoleWorker, Count: 2, Image: "other:2", CPUs: 1, Memory: "1g", TmpSize: "1g"})
	_, err := Create(ctx, client, meshMgr, cfg, time.Millisecond)
	require.NoError(t, err)

	var pulls []string
	lastPull, firstUse := -1, -1
	for i, c := range m.Calls {
		a := c.Args
		switch {
		case a[0] == "pull":
			pulls = append(pulls, strings.Join(a, " "))
			lastPull = i
		case (a[0] == "run" || a[0] == "create") && firstUse < 0:
			firstUse = i
		}
	}
	assert.Equal(t, []string{"pull --quiet other:2"}, pulls, "only the missing image is pulled")
	assert.Less(t, lastPull, firstUse, "the missing image is pulled before a container runs an image")
	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "--format", "{{.Id}}", "img:1"))
	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "--format", "{{.Id}}", "other:2"))
}

func TestCreate_ImageInspectFails(t *testing.T) {
	var m mock.Executor
	m.OnCall = happyOnCall(t, notFoundErr(t), func(args []string, _ string) (mock.Result, bool) {
		if isImageExists(args) {
			return mock.Result{Stderr: "Cannot connect to the Docker daemon\n", Err: fmt.Errorf("exit status 1")}, true
		}
		return mock.Result{}, false
	})
	client := docker.NewClient(&m)
	meshMgr := mesh.NewManager(client, mesh.DefaultRealm)

	_, err := Create(t.Context(), client, meshMgr, createCfg(), time.Millisecond)
	require.ErrorContains(t, err, "inspecting image img:1: exit status 1")
	for _, c := range m.Calls {
		a := c.Args
		assert.False(t, a[0] == "run" || a[0] == "create" || a[0] == "pull", "%v", a)
	}
}

func TestWorkerAdd_PullsMissingImage(t *testing.T) {
	// An --image the daemon does not have is pulled once without --pull.
	var m mock.Executor
	m.OnCall = workerAddOnCall(t)
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 2, Image: "custom:v3",
	}, time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "--format", "{{.Id}}", "custom:v3"))
	assert.Equal(t, 1, countCalls(m.Calls, "pull", "--quiet", "custom:v3"))
}

func TestWorkerAdd_LocalImageNotPulled(t *testing.T) {
	var m mock.Executor
	onCall := workerAddOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if isImageExists(args) {
			return mock.Result{Stdout: "sha256:local\n"}
		}
		return onCall(args, stdin)
	}
	client := docker.NewClient(&m)

	_, err := WorkerAdd(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, Image: "custom:v3",
	}, time.Millisecond)
	require.NoError(t, err)

	assert.Zero(t, countCalls(m.Calls, "pull"))
}
