// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package docker

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// newTestClient returns a Client backed by an OSExecutor wrapped
// in a RecordingExecutor. Skips the test if Docker is unavailable.
func newTestClient(t *testing.T) (*Client, *testRecorder) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	rec := &testRecorder{
		RecordingExecutor: &RecordingExecutor{Inner: &OSExecutor{}},
	}
	return NewClient(rec.RecordingExecutor), rec
}
