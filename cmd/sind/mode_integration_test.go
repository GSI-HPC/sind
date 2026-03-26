// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// testID is a per-process random suffix for unique resource names.
var testID = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}()

// executeWithDocker runs a CLI command backed by a real Docker client.
// Uses context.Background(); for commands needing a deadline use executeWithDockerCtx.
func executeWithDocker(args ...string) (string, string, error) {
	return executeWithDockerCtx(context.Background(), args...)
}

// executeWithDockerCtx runs a CLI command backed by a real Docker client
// with the given context (e.g. for deadline control on long-running commands).
func executeWithDockerCtx(ctx context.Context, args ...string) (string, string, error) {
	cmd := NewRootCommand()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	client := docker.NewClient(&docker.OSExecutor{})
	ctx = withClient(ctx, client)
	cmd.SetContext(ctx)

	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// realClient returns a docker.Client backed by a real executor.
func realClient(t *testing.T) *docker.Client {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	return docker.NewClient(&docker.OSExecutor{})
}

// skipIfNoNsdelegate skips the test if the host cgroup mount lacks nsdelegate.
func skipIfNoNsdelegate(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		t.Skip("cannot read /proc/mounts")
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "cgroup2") && strings.Contains(line, "nsdelegate") {
			return
		}
	}
	t.Skip("host cgroup mount lacks nsdelegate")
}

// skipIfNoImage skips the test if the sind-node image is not available.
func skipIfNoImage(t *testing.T, c *docker.Client) string {
	t.Helper()
	image := os.Getenv("SIND_TEST_IMAGE")
	if image == "" {
		image = "ghcr.io/gsi-hpc/sind-node:latest"
	}
	exists, err := c.ImageExists(t.Context(), image)
	if err != nil || !exists {
		t.Skipf("image %s not available", image)
	}
	return image
}
