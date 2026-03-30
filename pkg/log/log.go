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

// From extracts the logger from the context.
// Returns a no-op logger if none is set.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.New(discardHandler{})
}

// discardHandler is a slog.Handler that silently drops all log records.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }
