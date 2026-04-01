// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package slurm

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
)

func newTestClient(t *testing.T) (*docker.Client, *cmdexec.Recorder) {
	t.Helper()
	rec := cmdexec.NewMockRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
