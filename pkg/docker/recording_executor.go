// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// RecordingExecutor wraps another Executor and records all calls with
// their results. Useful for observing actual Docker CLI I/O during tests.
type RecordingExecutor struct {
	Inner Executor

	mu    sync.Mutex
	calls []RecordedCall
}

// RecordedCall captures one invocation of the executor.
type RecordedCall struct {
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

// Run implements Executor.
func (r *RecordingExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	stdout, stderr, err := r.Inner.Run(ctx, name, args...)
	r.record(RecordedCall{Name: name, Args: args, Stdout: stdout, Stderr: stderr, Err: err})
	return stdout, stderr, err
}

// RunWithStdin implements Executor.
func (r *RecordingExecutor) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	stdout, stderr, err := r.Inner.RunWithStdin(ctx, stdin, name, args...)
	r.record(RecordedCall{Name: name, Args: args, Stdout: stdout, Stderr: stderr, Err: err})
	return stdout, stderr, err
}

func (r *RecordingExecutor) record(call RecordedCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

// Calls returns a copy of all recorded calls.
func (r *RecordingExecutor) Calls() []RecordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RecordedCall{}, r.calls...)
}

// Dump returns a human-readable log of all recorded calls.
func (r *RecordingExecutor) Dump() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for i, c := range r.calls {
		fmt.Fprintf(&b, "[%d] %s %s\n", i, c.Name, strings.Join(c.Args, " "))
		if c.Stdout != "" {
			fmt.Fprintf(&b, "     stdout: %s", c.Stdout)
			if !strings.HasSuffix(c.Stdout, "\n") {
				b.WriteByte('\n')
			}
		}
		if c.Stderr != "" {
			fmt.Fprintf(&b, "     stderr: %s", c.Stderr)
			if !strings.HasSuffix(c.Stderr, "\n") {
				b.WriteByte('\n')
			}
		}
		if c.Err != nil {
			fmt.Fprintf(&b, "     err: %v\n", c.Err)
		}
	}
	return b.String()
}
