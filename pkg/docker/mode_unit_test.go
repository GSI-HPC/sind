// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package docker

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
)

func newTestClient(t *testing.T) (*Client, *mock.Recorder) {
	t.Helper()
	rec := mock.NewRecorder()
	return NewClient(rec.RecordingExecutor), rec
}
