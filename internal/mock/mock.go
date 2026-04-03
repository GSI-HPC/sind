// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mock provides test doubles for the cmdexec.Executor interface.
package mock

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// Call records a single invocation of Executor.
type Call struct {
	Name  string
	Args  []string
	Stdin string // captured stdin content (empty if no stdin)
}

// Result holds the return values for a single Executor call.
type Result struct {
	Stdout string
	Stderr string
	Err    error
}

// Executor records all calls and returns preconfigured results.
// It is safe for concurrent use.
//
// Results are dispatched in two modes:
//   - If OnCall is set, it is called for every invocation to produce a result.
//   - Otherwise, results queued via AddResult are returned in FIFO order.
//
// OnCall is useful when multiple goroutines share a Executor and
// result dispatch must be based on command arguments rather than call order.
type Executor struct {
	// OnCall, if set, is called to produce a result for each invocation.
	// The args slice contains the subcommand and its arguments
	// (e.g. ["inspect", "sind-dns"]). Stdin is non-empty for RunWithStdin calls.
	OnCall func(args []string, stdin string) Result

	mu      sync.Mutex
	Calls   []Call
	results []Result
	idx     int
}

// Ensure Executor implements cmdexec.Executor.
var _ cmdexec.Executor = (*Executor)(nil)

// AddResult enqueues a result to be returned by the next Run or RunWithStdin call.
// Only used when OnCall is nil.
func (m *Executor) AddResult(stdout, stderr string, err error) {
	m.results = append(m.results, Result{Stdout: stdout, Stderr: stderr, Err: err})
}

func (m *Executor) record(name string, args []string, stdin string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = append(m.Calls, Call{Name: name, Args: args, Stdin: stdin})

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

// Run implements cmdexec.Executor.
func (m *Executor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	return m.record(name, args, "")
}

// RunWithStdin implements cmdexec.Executor.
func (m *Executor) RunWithStdin(_ context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", "", fmt.Errorf("mock: reading stdin: %w", err)
	}
	return m.record(name, args, string(data))
}
