// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

// Recorder holds a RecordingExecutor and the underlying mock (if any).
// In unit mode, mock is non-nil and AddResult configures responses.
// In integration mode, mock is nil and AddResult is a no-op.
type Recorder struct {
	*RecordingExecutor
	mock *MockExecutor
}

// NewMockRecorder returns a Recorder backed by a MockExecutor.
func NewMockRecorder() *Recorder {
	m := &MockExecutor{}
	return &Recorder{
		RecordingExecutor: &RecordingExecutor{Inner: m},
		mock:              m,
	}
}

// NewIntegrationRecorder returns a Recorder backed by an OSExecutor.
func NewIntegrationRecorder() *Recorder {
	return &Recorder{
		RecordingExecutor: &RecordingExecutor{Inner: &OSExecutor{}},
	}
}

// AddResult queues a mock response. No-op in integration mode.
func (r *Recorder) AddResult(stdout, stderr string, err error) {
	if r.mock != nil {
		r.mock.AddResult(stdout, stderr, err)
	}
}

// IsIntegration returns true when running against real executors.
func (r *Recorder) IsIntegration() bool {
	return r.mock == nil
}
