// SPDX-License-Identifier: LGPL-3.0-or-later

// Package cmdexec provides a testable abstraction for running external commands.
package cmdexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// Executor runs external commands and captures their output.
type Executor interface {
	// Run executes the named command with the given arguments and returns
	// the captured stdout and stderr. If the command exits with a non-zero
	// status, the returned error wraps an *exec.ExitError.
	Run(ctx context.Context, name string, args ...string) (stdout string, stderr string, err error)

	// RunWithStdin is like Run but pipes the given reader to the command's stdin.
	RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (stdout string, stderr string, err error)
}

// OSExecutor runs commands using os/exec.
type OSExecutor struct{}

func (e *OSExecutor) run(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// Run implements Executor.
func (e *OSExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return e.run(ctx, nil, name, args...)
}

// RunWithStdin implements Executor.
func (e *OSExecutor) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	return e.run(ctx, stdin, name, args...)
}

// MockCall records a single invocation of MockExecutor.
type MockCall struct {
	Name  string
	Args  []string
	Stdin string // captured stdin content (empty if no stdin)
}

// MockResult holds the return values for a single MockExecutor call.
type MockResult struct {
	Stdout string
	Stderr string
	Err    error
}

// MockExecutor records all calls and returns preconfigured results.
// It is safe for concurrent use.
//
// Results are dispatched in two modes:
//   - If OnCall is set, it is called for every invocation to produce a result.
//   - Otherwise, results queued via AddResult are returned in FIFO order.
//
// OnCall is useful when multiple goroutines share a MockExecutor and
// result dispatch must be based on command arguments rather than call order.
type MockExecutor struct {
	// OnCall, if set, is called to produce a result for each invocation.
	// The args slice contains the subcommand and its arguments
	// (e.g. ["inspect", "sind-dns"]). Stdin is non-empty for RunWithStdin calls.
	OnCall func(args []string, stdin string) MockResult

	mu      sync.Mutex
	Calls   []MockCall
	results []MockResult
	idx     int
}

// AddResult enqueues a result to be returned by the next Run or RunWithStdin call.
// Only used when OnCall is nil.
func (m *MockExecutor) AddResult(stdout, stderr string, err error) {
	m.results = append(m.results, MockResult{Stdout: stdout, Stderr: stderr, Err: err})
}

func (m *MockExecutor) record(name string, args []string, stdin string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = append(m.Calls, MockCall{Name: name, Args: args, Stdin: stdin})

	if m.OnCall != nil {
		r := m.OnCall(args, stdin)
		return r.Stdout, r.Stderr, r.Err
	}

	if m.idx >= len(m.results) {
		return "", "", fmt.Errorf("mock: unexpected call #%d: %s %v", len(m.Calls), name, args)
	}
	r := m.results[m.idx]
	m.idx++
	return r.Stdout, r.Stderr, r.Err
}

// Run implements Executor.
func (m *MockExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	return m.record(name, args, "")
}

// RunWithStdin implements Executor.
func (m *MockExecutor) RunWithStdin(_ context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", "", fmt.Errorf("mock: reading stdin: %w", err)
	}
	return m.record(name, args, string(data))
}
