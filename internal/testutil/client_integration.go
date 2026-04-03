// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package testutil

import (
	"context"
	"crypto/rand"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// Realm returns a unique random realm with the given prefix.
// Each call produces a different value, suitable for parallel tests.
func Realm(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

// dockerInfoTimeout is the maximum time to wait for docker info to respond
// when checking whether the Docker daemon is available.
const dockerInfoTimeout = 5 * time.Second

// NewClient returns a docker.Client backed by a real OSExecutor.
// The test is skipped when docker is not available.
func NewClient(t *testing.T) (*docker.Client, *cmdexec.Recorder) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}
	ctx, cancel := context.WithTimeout(t.Context(), dockerInfoTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	rec := cmdexec.NewIntegrationRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
