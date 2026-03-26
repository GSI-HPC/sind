// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package mesh

import (
	"context"
	"crypto/rand"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// testRealm is a per-process unique realm so parallel integration test
// runs don't collide on Docker resource names.
var testRealm = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("it-mesh-%x", b)
}()

func newTestClient(t *testing.T) (*docker.Client, *docker.Recorder) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	rec := docker.NewIntegrationRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
