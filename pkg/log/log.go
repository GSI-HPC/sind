// SPDX-License-Identifier: LGPL-3.0-or-later

// Package log provides context-based structured logging.
//
// The logger is injected into context at the CLI layer and extracted
// in pkg/ code via [From]. When no logger is present in the context,
// a no-op logger is returned. This design keeps logging local and
// injectable, allowing the slog backend to be replaced with a richer
// progress UI in the future without changing call sites.
package log

import (
	"context"
	"log/slog"
)

// LevelTrace is a custom level below Debug for low-level diagnostics
// such as docker commands and probe retry attempts.
const LevelTrace = slog.Level(-8)

type contextKey struct{}

// With stores a logger in the context.
func With(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// nop is the logger From returns when the context has none.
var nop = slog.New(slog.DiscardHandler)

// From extracts the logger from the context.
// Returns a no-op logger if none is set.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return l
	}
	return nop
}

type warnKey struct{}

// WithWarn stores in the context where the warnings meant for the user go
// (Warn). The sind CLI prints each as a "Warning:" line on stderr.
func WithWarn(ctx context.Context, warn func(msg string)) context.Context {
	return context.WithValue(ctx, warnKey{}, warn)
}

// Warn reports a warning meant for the user, something that did not fail
// the operation but that its caller should know, such as a step that was
// skipped: to the function the context carries (WithWarn), or else, for a
// library caller that set none, to the context's logger at warn level.
func Warn(ctx context.Context, msg string) {
	if warn, ok := ctx.Value(warnKey{}).(func(string)); ok {
		warn(msg)
		return
	}
	From(ctx).WarnContext(ctx, msg)
}
