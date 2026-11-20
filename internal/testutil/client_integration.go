// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package testutil

import (
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil/itest"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// Realm returns a unique random realm with the given prefix.
// Each call produces a different value, suitable for parallel tests.
func Realm(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

// NewClient returns a docker.Client backed by a real OSExecutor.
// The test is skipped, or fails under itest.RequireEnv, when docker
// reaches no daemon.
func NewClient(t *testing.T) (*docker.Client, *mock.Recorder) {
	t.Helper()
	itest.RequireDocker(t)
	rec := mock.NewIntegrationRecorder()
	return docker.NewClient(rec.RecordingExecutor), rec
}

// SkipWithoutDocker does nothing in integration mode, where NewClient skips
// a test when Docker is not there.
func SkipWithoutDocker(*testing.T) {}
