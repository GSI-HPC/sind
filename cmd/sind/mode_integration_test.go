// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os/exec"
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
func executeWithDocker(args ...string) (string, string, error) {
	cmd := NewRootCommand()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	client := docker.NewClient(&docker.OSExecutor{})
	ctx := withClient(context.Background(), client)
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
