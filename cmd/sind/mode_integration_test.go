// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/spf13/afero"
)

// testID is a per-process random suffix for unique resource names.
var testID = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}()

// testRealm is a per-process unique realm so parallel integration test
// runs don't collide on Docker resource names.
var testRealm = "it-cmd-" + testID

// executeWithDocker runs a CLI command backed by a real Docker client.
// Uses context.Background(); for commands needing a deadline use executeWithDockerCtx.
func executeWithDocker(args ...string) (string, string, error) {
	return executeWithDockerCtx(context.Background(), args...)
}

// executeWithRealm runs a CLI command with --realm prepended.
func executeWithRealm(realm string, args ...string) (string, string, error) {
	return executeWithDocker(append([]string{"--realm", realm}, args...)...)
}

// executeWithRealmCtx runs a CLI command with --realm prepended and a context.
func executeWithRealmCtx(ctx context.Context, realm string, args ...string) (string, string, error) {
	return executeWithDockerCtx(ctx, append([]string{"--realm", realm}, args...)...)
}

// executeWithRealmStdin runs a CLI command with --realm prepended and piped stdin.
func executeWithRealmStdin(ctx context.Context, realm string, stdin string, args ...string) (string, string, error) {
	return executeWithStdin(ctx, stdin, append([]string{"--realm", realm}, args...)...)
}

// executeWithDockerCtx runs a CLI command backed by a real Docker client
// with the given context (e.g. for deadline control on long-running commands).
func executeWithDockerCtx(ctx context.Context, args ...string) (string, string, error) {
	return executeWithDockerIn(ctx, nil, args...)
}

// executeWithDockerIn is executeWithDockerCtx with stdin as the command's
// input; nil leaves it at os.Stdin.
func executeWithDockerIn(ctx context.Context, stdin io.Reader, args ...string) (string, string, error) {
	return execute(withClient(ctx, docker.NewClient(&cmdexec.OSExecutor{})), stdin, args...)
}

// executeWithStdin runs a CLI command with the given string as its input.
// The input is set on the command, not by replacing os.Stdin, which other
// tests running in parallel read.
func executeWithStdin(ctx context.Context, stdin string, args ...string) (string, string, error) {
	return executeWithDockerIn(ctx, strings.NewReader(stdin), args...)
}

// realClient returns a docker.Client backed by a real executor.
func realClient(t *testing.T) *docker.Client {
	t.Helper()
	c, _ := testutil.NewClient(t)
	return c
}

// checkPrerequisites skips the test if the host does not meet sind
// prerequisites (Docker version, cgroupv2 with nsdelegate).
func checkPrerequisites(t *testing.T, c *docker.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		t.Skipf("cannot query Docker version: %v", err)
	}
	if vErr := doctor.CheckDockerVersion(info.ServerVersion); vErr != nil {
		t.Skipf("%v", vErr)
	}
	_, _, hasNsd := doctor.CgroupInfo(afero.NewOsFs())
	if !hasNsd {
		t.Skip("host cgroup mount lacks nsdelegate")
	}
}
