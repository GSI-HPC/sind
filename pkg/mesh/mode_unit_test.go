// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package mesh

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// testRealm is DefaultRealm for unit tests; integration tests use a random value.
var testRealm = DefaultRealm

// lifecycleRealm returns testRealm in unit mode. In integration mode it
// returns a per-test unique realm so lifecycle tests can run in parallel.
func lifecycleRealm(*cmdexec.Recorder) string { return testRealm }

func newTestClient(t *testing.T) (*docker.Client, *cmdexec.Recorder) {
	t.Helper()
	rec := cmdexec.NewMockRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
