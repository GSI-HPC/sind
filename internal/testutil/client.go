// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package testutil

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// Realm returns the default realm "sind" in unit mode.
// In integration mode it returns a unique random realm with the given prefix.
func Realm(string) string { return "sind" }

// NewClient returns a docker.Client backed by a MockExecutor.
func NewClient(t *testing.T) (*docker.Client, *cmdexec.Recorder) {
	t.Helper()
	rec := cmdexec.NewMockRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}
