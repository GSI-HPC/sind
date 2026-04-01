// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package docker

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

func newTestClient(t *testing.T) (*Client, *cmdexec.Recorder) {
	t.Helper()
	rec := cmdexec.NewMockRecorder()
	return NewClient(rec.RecordingExecutor), rec
}
