// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"
)

const testContainerName ContainerName = "sind-dev-controller"

// testID generates a short random hex suffix for unique resource names.
// testID is a per-process random suffix so parallel integration test
// runs don't collide on Docker resource names.
var testID = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}()

// itName returns a unique Docker resource name for integration tests.
// In unit mode the suffix is still appended but mock data uses the same value.
func itName(base string) string {
	return "sind-it-" + base + "-" + testID
}

// itContainerName returns a unique ContainerName for integration tests.
func itContainerName(t *testing.T, base string) ContainerName {
	t.Helper()
	return ContainerName(itName(base))
}

// itNetworkName returns a unique NetworkName for integration tests.
func itNetworkName(t *testing.T, base string) NetworkName {
	t.Helper()
	return NetworkName(itName(base))
}

// itVolumeName returns a unique VolumeName for integration tests.
func itVolumeName(t *testing.T, base string) VolumeName {
	t.Helper()
	return VolumeName(itName(base))
}

// testRecorder holds a RecordingExecutor and the underlying mock (if any).
// In unit mode, mock is non-nil and AddResult configures responses.
// In integration mode, mock is nil and AddResult is a no-op.
type testRecorder struct {
	*RecordingExecutor
	mock *MockExecutor
}

// AddResult queues a mock response. No-op in integration mode.
func (r *testRecorder) AddResult(stdout, stderr string, err error) {
	if r.mock != nil {
		r.mock.AddResult(stdout, stderr, err)
	}
}

// IsIntegration returns true when running against real Docker.
func (r *testRecorder) IsIntegration() bool {
	return r.mock == nil
}

// copyFromTar builds a tar archive containing a single file, as docker cp would return.
func copyFromTar(content string) string {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte(content)
	_ = tw.WriteHeader(&tar.Header{Name: "file", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	return buf.String()
}

// inspectRunning returns mock docker inspect JSON for a running container.
func inspectRunning(name string) string {
	return `[{"Id":"abc123","Name":"/` + name + `","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}

// inspectExited returns mock docker inspect JSON for an exited container.
func inspectExited(name string) string {
	return `[{"Id":"abc123","Name":"/` + name + `","State":{"Status":"exited"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}

// inspectPaused returns mock docker inspect JSON for a paused container.
func inspectPaused(name string) string {
	return `[{"Id":"abc123","Name":"/` + name + `","State":{"Status":"paused"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}
