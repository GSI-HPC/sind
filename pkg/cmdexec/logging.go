// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"context"
	"io"
	"strings"
)

// LogFunc is called before each command execution with the context and
// the full command string (name + args joined by spaces).
type LogFunc func(ctx context.Context, cmd string)

// LoggingExecutor wraps another Executor and calls a LogFunc before each
// command invocation.
type LoggingExecutor struct {
	Inner Executor
	Log   LogFunc
}

// Run implements Executor.
func (l *LoggingExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	l.Log(ctx, name+" "+strings.Join(args, " "))
	return l.Inner.Run(ctx, name, args...)
}

// RunWithStdin implements Executor.
func (l *LoggingExecutor) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	l.Log(ctx, name+" "+strings.Join(args, " "))
	return l.Inner.RunWithStdin(ctx, stdin, name, args...)
}

// Start implements Executor.
func (l *LoggingExecutor) Start(ctx context.Context, name string, args ...string) (*Process, error) {
	l.Log(ctx, name+" "+strings.Join(args, " "))
	return l.Inner.Start(ctx, name, args...)
}
