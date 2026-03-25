// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

const testContainerName ContainerName = "sind-dev-controller"

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
