// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
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
	cfg := createCfg()
	cfg.Nodes = append(cfg.Nodes, config.Node{Role: config.RoleWorker, Count: 2, Image: "other:2", CPUs: 1, Memory: "1g", TmpSize: "1g"})
	cfg.Pull = true
	m, _, err := runCreate(t.Context(), t, cfg, false, pullOnCall(mock.Result{}))
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
	assert.Equal(t, []string{"pull img:1", "pull other:2"}, pulls)
	assert.Less(t, lastPull, firstUse, "every image is pulled before a container runs it")
}

func TestCreate_PullFails(t *testing.T) {
	// The pull fails before anything is created; the version check, the
	// resources and the CVMFS check all wait for it.
	cfg := createCfg()
	cfg.Pull = true
	cfg.Storage.CVMFS = true
	m, _, err := runCreate(t.Context(), t, cfg, false, pullOnCall(mock.Result{Err: fmt.Errorf("manifest unknown")}))
	require.EqualError(t, err, "pulling img:1: manifest unknown")
	// The network and volumes may exist (and are rolled back), but nothing
	// ran the image.
	for _, c := range m.Calls {
		a := c.Args
		assert.False(t, a[0] == "run" || a[0] == "create" || a[0] == "plugin", "%v", a)
	}
}

func TestWorkerAdd_PullsOnce(t *testing.T) {
	onCall := workerAddOnCall(t)
	m, _, err := addWorkers(t, func(args []string, stdin string) mock.Result {
		if args[0] == "pull" {
			return mock.Result{}
		}
		return onCall(args, stdin)
	}, WorkerAddOptions{ClusterName: "dev", Count: 2, Image: "img:1", Pull: true})
	require.NoError(t, err)

	var pulls int
	for _, c := range m.Calls {
		assert.NotContains(t, c.Args, "--pull", "%v", c.Args)
		if c.Args[0] == "pull" {
			pulls++
			assert.Equal(t, []string{"pull", "img:1"}, c.Args)
		}
	}
	assert.Equal(t, 1, pulls)
}

func TestWorkerAdd_PullFails(t *testing.T) {
	onCall := workerAddOnCall(t)
	m, _, err := addWorkers(t, func(args []string, stdin string) mock.Result {
		if args[0] == "pull" {
			return mock.Result{Err: fmt.Errorf("manifest unknown")}
		}
		return onCall(args, stdin)
	}, WorkerAddOptions{ClusterName: "dev", Image: "img:1", Pull: true})
	require.EqualError(t, err, "pulling img:1: manifest unknown")
	assert.Zero(t, countCalls(m.Calls, "create"))
}

// imageExistsOnCall answers ImageExists: the daemon has every image but
// missing, and docker pull succeeds.
func imageExistsOnCall(t *testing.T, missing ...string) func(args []string, _ string) (mock.Result, bool) {
	return func(args []string, _ string) (mock.Result, bool) {
		switch {
		case isImageExists(args) && slices.Contains(missing, args[4]):
			return mock.Result{Stderr: "Error: No such image: " + args[4] + "\n", Err: testutil.ExitCode1(t)}, true
		case args[0] == "pull":
			return mock.Result{}, true
		}
		return mock.Result{}, false
	}
}

func TestCreate_PullsMissingImages(t *testing.T) {
	// Without --pull, the images the daemon does not have are pulled once,
	// before any container runs one, so that no node's docker create pulls.
	cfg := createCfg()
	cfg.Nodes = append(cfg.Nodes, config.Node{Role: config.RoleWorker, Count: 2, Image: "other:2", CPUs: 1, Memory: "1g", TmpSize: "1g"})
	m, _, err := runCreate(t.Context(), t, cfg, false, imageExistsOnCall(t, "other:2"))
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
	assert.Equal(t, []string{"pull other:2"}, pulls, "only the missing image is pulled")
	assert.Less(t, lastPull, firstUse, "the missing image is pulled before a container runs an image")
	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "--format", "{{.Id}}", "img:1"))
	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "--format", "{{.Id}}", "other:2"))
}

func TestCreate_ImageInspectFails(t *testing.T) {
	m, _, err := runCreate(t.Context(), t, createCfg(), false, func(args []string, _ string) (mock.Result, bool) {
		if isImageExists(args) {
			return mock.Result{Stderr: "Cannot connect to the Docker daemon\n", Err: fmt.Errorf("exit status 1")}, true
		}
		return mock.Result{}, false
	})
	require.ErrorContains(t, err, "inspecting image img:1: exit status 1")
	for _, c := range m.Calls {
		a := c.Args
		assert.False(t, a[0] == "run" || a[0] == "create" || a[0] == "pull", "%v", a)
	}
}

func TestWorkerAdd_PullsMissingImage(t *testing.T) {
	// An --image the daemon does not have is pulled once without --pull.
	m, _, err := addWorkers(t, workerAddOnCall(t), WorkerAddOptions{ClusterName: "dev", Count: 2, Image: "custom:v3"})
	require.NoError(t, err)

	assert.Equal(t, 1, countCalls(m.Calls, "image", "inspect", "--format", "{{.Id}}", "custom:v3"))
	assert.Equal(t, 1, countCalls(m.Calls, "pull", "custom:v3"))
}

func TestWorkerAdd_LocalImageNotPulled(t *testing.T) {
	onCall := workerAddOnCall(t)
	m, _, err := addWorkers(t, func(args []string, stdin string) mock.Result {
		if isImageExists(args) {
			return mock.Result{Stdout: "sha256:local\n"}
		}
		return onCall(args, stdin)
	}, WorkerAddOptions{ClusterName: "dev", Count: 1, Image: "custom:v3"})
	require.NoError(t, err)

	assert.Zero(t, countCalls(m.Calls, "pull"))
}

// The pull is the step "pull images", with a target for each image that
// reports its layers as work in items: those done out of those docker has
// told of, the whole image when it was there already; the output text is no
// part of the events.
func TestPullImages_ReportsTheLayers(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		if args[1] == "img:1" {
			return mock.Result{Stdout: "latest: Pulling from library/img\n" +
				"a3ed95caeb02: Pulling fs layer\nb4f1e0d2c9a1: Already exists\n" +
				"a3ed95caeb02: Pull complete\nStatus: Downloaded newer image for img:1\n"}
		}
		return mock.Result{Stdout: "Status: Image is up to date for other:2\n"}
	}}

	err := pullImages(ctx, docker.NewClient(m), []string{"img:1", "other:2"})

	require.NoError(t, err)
	w.Finish()
	assert.Equal(t, `step pull images total=2 [fold,show-lines]: ok
  target img:1 amount=2 size=2 unit=items [show-lines]: ok
  target other:2 amount=1 size=1 unit=items [show-lines]: ok
`, testutil.WithoutCalls(w.Events()))
}
