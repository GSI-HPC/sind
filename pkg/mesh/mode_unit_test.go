// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package mesh

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// testRealm is DefaultRealm for unit tests; integration tests use a random value.
var testRealm = DefaultRealm

// lifecycleRealm returns testRealm in unit mode. In integration mode it
// returns a per-test unique realm so lifecycle tests can run in parallel.
func lifecycleRealm(*docker.Recorder) string { return testRealm }

func newTestClient(t *testing.T) (*docker.Client, *docker.Recorder) {
	t.Helper()
	rec := docker.NewMockRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
