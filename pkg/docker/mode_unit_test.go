// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package docker

import "testing"

// newTestClient returns a Client backed by a MockExecutor wrapped
// in a RecordingExecutor. Use rec.AddResult() to configure responses.
func newTestClient(t *testing.T) (*Client, *testRecorder) {
	t.Helper()
	m := &MockExecutor{}
	rec := &testRecorder{
		RecordingExecutor: &RecordingExecutor{Inner: m},
		mock:              m,
	}
	return NewClient(rec.RecordingExecutor), rec
}
