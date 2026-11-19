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

// skipWithoutDocker skips a test that checks the client against Docker,
// which unit mode does not have: answers queued for it would only replay
// what the test expects. TestCommands checks the commands, and the unit
// tests of each method how its output is read.
func skipWithoutDocker(t *testing.T) {
	t.Helper()
	t.Skip("needs Docker (-tags integration)")
}
