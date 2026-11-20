// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package docker

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil/itest"
)

// newTestClient is testutil.NewClient, which this package cannot import.
func newTestClient(t *testing.T) (*Client, *mock.Recorder) {
	t.Helper()
	itest.RequireDocker(t)
	rec := mock.NewIntegrationRecorder()
	return NewClient(rec.RecordingExecutor), rec
}

// skipWithoutDocker does nothing in integration mode, where newTestClient
// skips a test when Docker is not there.
func skipWithoutDocker(*testing.T) {}
