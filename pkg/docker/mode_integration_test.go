// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package docker

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

func newTestClient(t *testing.T) (*Client, *cmdexec.Recorder) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not running")
	}
	rec := cmdexec.NewIntegrationRecorder()
	return NewClient(rec.RecordingExecutor), rec
}
