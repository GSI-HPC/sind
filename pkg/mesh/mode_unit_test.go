// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package mesh

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// testRealm is DefaultRealm for unit tests; integration tests use a random value.
var testRealm = DefaultRealm

func newTestClient(t *testing.T) (*docker.Client, *docker.Recorder) {
	t.Helper()
	rec := docker.NewMockRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
