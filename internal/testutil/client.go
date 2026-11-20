// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package testutil

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// Realm returns the default realm "sind" in unit mode.
// In integration mode it returns a unique random realm with the given prefix.
func Realm(string) string { return "sind" }

// NewClient returns a docker.Client backed by a MockExecutor.
func NewClient(t *testing.T) (*docker.Client, *mock.Recorder) {
	t.Helper()
	rec := mock.NewRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}

// SkipWithoutDocker skips a test that checks sind against Docker, which
// unit mode does not have: answers queued for it would only replay what
// the test expects. The unit tests check the commands and how their output
// is read.
func SkipWithoutDocker(t *testing.T) {
	t.Helper()
	t.Skip("needs Docker (-tags integration)")
}
