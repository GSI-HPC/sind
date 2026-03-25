// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package docker

import "testing"

func newTestClient(t *testing.T) (*Client, *Recorder) {
	t.Helper()
	rec := NewMockRecorder()
	return NewClient(rec.RecordingExecutor), rec
}
