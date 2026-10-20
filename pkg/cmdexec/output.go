// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import "context"

// stdoutDataKey is the context key of WithStdoutAsData.
type stdoutDataKey struct{}

// WithStdoutAsData returns ctx for a command whose standard output is data
// its caller reads, such as an ID, a name or JSON, rather than lines that
// say what the command is doing: under a progress span that shows lines,
// OSExecutor then shows the lines of its standard error alone.
func WithStdoutAsData(ctx context.Context) context.Context {
	return context.WithValue(ctx, stdoutDataKey{}, true)
}

// StdoutIsData reports whether ctx is one WithStdoutAsData returned.
func StdoutIsData(ctx context.Context) bool {
	data, _ := ctx.Value(stdoutDataKey{}).(bool)
	return data
}

// processGroupKey is the context key of WithProcessGroup.
type processGroupKey struct{}

// WithProcessGroup returns ctx for a command that OSExecutor runs in a
// process group of its own and, when ctx ends, kills with every process it
// started that is still in that group, such as a docker CLI. Killing the
// command alone would leave them running, holding its output open, so the
// command would be reported done only when they end.
func WithProcessGroup(ctx context.Context) context.Context {
	return context.WithValue(ctx, processGroupKey{}, true)
}

// InProcessGroup reports whether ctx is one WithProcessGroup returned.
func InProcessGroup(ctx context.Context) bool {
	group, _ := ctx.Value(processGroupKey{}).(bool)
	return group
}
