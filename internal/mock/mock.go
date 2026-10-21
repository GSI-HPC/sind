// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mock provides test doubles for the cmdexec.Executor interface.
package mock

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// StreamResult holds the return values for a single Executor.Start call.
type StreamResult struct {
	Reader io.ReadCloser
	Err    error
}

// Call records a single invocation of Executor.
type Call struct {
	Name  string
	Args  []string
	Stdin string // captured stdin content (empty if no stdin)
}

// Result holds the return values for a single Executor call. An Err that is
// a bare *exec.ExitError is returned as a *cmdexec.ExitError carrying
// Stderr, the shape the real executor returns.
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

	// OnStart, if set, is called to produce a StreamResult for Start calls.
	// If nil, Start returns an error.
	OnStart func(args []string) StreamResult

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

func (m *Executor) record(ctx context.Context, name string, args []string, stdin string) (string, string, error) {
	stdout, stderr, err := m.dispatch(name, args, stdin)
	parseLines(ctx, stdout, stderr)
	return stdout, stderr, err
}

// dispatch records the call and returns its result.
func (m *Executor) dispatch(name string, args []string, stdin string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = append(m.Calls, Call{Name: name, Args: args, Stdin: stdin})

	if m.OnCall != nil {
		r := m.OnCall(args, stdin)
		return r.Stdout, r.Stderr, cmdexec.WrapExitError(r.Err, r.Stderr)
	}

	if m.idx >= len(m.results) {
		return "", "", fmt.Errorf("mock: unexpected call #%d: %s %v", len(m.Calls), name, args)
	}
	r := m.results[m.idx]
	m.idx++
	return r.Stdout, r.Stderr, cmdexec.WrapExitError(r.Err, r.Stderr)
}

// parseLines hands the lines of a result to the line parser ctx carries
// (cmdexec.WithLineParser), as OSExecutor does while the command runs: the
// lines of standard output, unless ctx says it is data, then those of
// standard error, each without its line break; a last line without one is
// left out.
func parseLines(ctx context.Context, stdout, stderr string) {
	parse := cmdexec.LineParser(ctx)
	if parse == nil {
		return
	}
	feed := func(st progress.Stream, text string) {
		for {
			line, rest, ended := strings.Cut(text, "\n")
			if !ended {
				return
			}
			parse(st, strings.TrimSuffix(line, "\r"))
			text = rest
		}
	}
	if !cmdexec.StdoutIsData(ctx) {
		feed(progress.Stdout, stdout)
	}
	feed(progress.Stderr, stderr)
}

// Run implements cmdexec.Executor.
func (m *Executor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return m.record(ctx, name, args, "")
}

// RunWithStdin implements cmdexec.Executor.
func (m *Executor) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", "", fmt.Errorf("mock: reading stdin: %w", err)
	}
	return m.record(ctx, name, args, string(data))
}

// Pipes manages io.Pipe pairs for mock streaming. Each OnStart call
// creates a new pipe and returns its reader. Use Write to feed data into
// a pipe by index, and CloseAll to shut them down.
type Pipes struct {
	mu    sync.Mutex
	pipes []*io.PipeWriter
}

// OnStart is an OnStart callback that creates a new pipe per call.
func (p *Pipes) OnStart(_ []string) StreamResult {
	pr, pw := io.Pipe()
	p.mu.Lock()
	p.pipes = append(p.pipes, pw)
	p.mu.Unlock()
	return StreamResult{Reader: pr}
}

// Write sends data to the pipe at the given index.
func (p *Pipes) Write(index int, data string) {
	p.mu.Lock()
	pw := p.pipes[index]
	p.mu.Unlock()
	_, _ = pw.Write([]byte(data))
}

// Len returns the number of pipes created so far.
func (p *Pipes) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pipes)
}

// CloseWithError closes the pipe at the given index with an error,
// causing the reader to return that error.
func (p *Pipes) CloseWithError(index int, err error) {
	p.mu.Lock()
	pw := p.pipes[index]
	p.mu.Unlock()
	_ = pw.CloseWithError(err)
}

// CloseAll closes all pipe writers.
func (p *Pipes) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pw := range p.pipes {
		_ = pw.Close()
	}
}

// Start implements cmdexec.Executor.
func (m *Executor) Start(_ context.Context, name string, args ...string) (*cmdexec.Process, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, Call{Name: name, Args: args})
	m.mu.Unlock()

	if m.OnStart != nil {
		r := m.OnStart(args)
		if r.Err != nil {
			return nil, r.Err
		}
		return &cmdexec.Process{Stdout: r.Reader}, nil
	}
	return nil, fmt.Errorf("mock: unexpected Start call: %v", args)
}
