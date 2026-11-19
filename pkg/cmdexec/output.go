// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"context"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
)

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

// stderrMergedKey is the context key of WithStderrMerged.
type stderrMergedKey struct{}

// WithStderrMerged returns ctx for a long-lived command (Start) whose
// standard error is read with its standard output, as one stream: docker
// logs, for one, writes what the container wrote to its standard error to
// its own.
func WithStderrMerged(ctx context.Context) context.Context {
	return context.WithValue(ctx, stderrMergedKey{}, true)
}

// StderrMerged reports whether ctx is one WithStderrMerged returned.
func StderrMerged(ctx context.Context) bool {
	merged, _ := ctx.Value(stderrMergedKey{}).(bool)
	return merged
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

// stopGraceKey is the context key of WithStopGrace.
type stopGraceKey struct{}

// WithStopGrace returns ctx for a command run WithProcessGroup that stops
// on SIGTERM itself, with what it started, such as sind, which rolls back
// what it began. When ctx ends, OSExecutor sends SIGTERM to the command
// alone, and kills its process group only once grace has passed. Without
// it, the end of ctx kills the group at once.
func WithStopGrace(ctx context.Context, grace time.Duration) context.Context {
	return context.WithValue(ctx, stopGraceKey{}, grace)
}

// StopGrace returns the grace of WithStopGrace, zero without one.
func StopGrace(ctx context.Context) time.Duration {
	grace, _ := ctx.Value(stopGraceKey{}).(time.Duration)
	return grace
}

// orphanSafeKey is the context key of WithOrphanSafe.
type orphanSafeKey struct{}

// WithOrphanSafe returns ctx for a command that must not die with this
// process, such as a sind that sind ui runs: once nobody reads a pipe, the
// next write to it ends the writer with SIGPIPE, which can be before its
// rollback. OSExecutor gives such a command no pipe: its standard output
// goes to the null device, its standard error to an unlinked temporary
// file, which Run returns when the command ends. On Linux the command also
// gets SIGINT when this process dies, as Ctrl-C sends, so that it stops
// and rolls back.
func WithOrphanSafe(ctx context.Context) context.Context {
	return context.WithValue(ctx, orphanSafeKey{}, true)
}

// OrphanSafe reports whether ctx is one WithOrphanSafe returned.
func OrphanSafe(ctx context.Context) bool {
	safe, _ := ctx.Value(orphanSafeKey{}).(bool)
	return safe
}

// lineParserKey is the context key of WithLineParser.
type lineParserKey struct{}

// WithLineParser returns ctx for a command whose output a caller follows
// line by line as it is written, such as the status lines of docker pull
// that tell how far an image has got: OSExecutor hands each line of its
// standard error, and of its standard output unless that is data
// (WithStdoutAsData), to parse as it ends, without its line break, with the
// stream it came from. parse is called from the goroutine that copies the
// stream, so the calls for the two streams may run at the same time, and
// must not block; a line that has not ended when the output does is none
// it is given. The output is captured as without a parser.
func WithLineParser(ctx context.Context, parse func(progress.Stream, string)) context.Context {
	return context.WithValue(ctx, lineParserKey{}, parse)
}

// LineParser returns the function WithLineParser put in ctx, or nil.
func LineParser(ctx context.Context) func(progress.Stream, string) {
	parse, _ := ctx.Value(lineParserKey{}).(func(progress.Stream, string))
	return parse
}

// envKey is the context key of WithEnv.
type envKey struct{}

// WithEnv returns ctx for a command that OSExecutor runs with env, in the
// form of os.Environ, as its whole environment instead of the process's
// own. An empty env is the process's own, as without WithEnv.
func WithEnv(ctx context.Context, env []string) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

// Env returns the environment WithEnv put in ctx, or nil.
func Env(ctx context.Context) []string {
	env, _ := ctx.Value(envKey{}).([]string)
	return env
}

// startedKey is the context key of WithStarted.
type startedKey struct{}

// WithStarted returns ctx for a command whose process ID its caller needs
// while it runs, such as to signal it: OSExecutor calls started with the ID
// once the command has started, from the goroutine that runs it, before it
// waits for the command to end. It is not called for a command that cannot
// be started.
func WithStarted(ctx context.Context, started func(pid int)) context.Context {
	return context.WithValue(ctx, startedKey{}, started)
}

// Started returns the function WithStarted put in ctx, or nil.
func Started(ctx context.Context) func(pid int) {
	started, _ := ctx.Value(startedKey{}).(func(pid int))
	return started
}
