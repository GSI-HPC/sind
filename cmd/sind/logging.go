// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"io"
	"log/slog"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// newLogger builds a slog.Logger for the given verbosity level.
// At verbosity 0, a no-op logger is returned.
func newLogger(w io.Writer, verbosity int) *slog.Logger {
	if verbosity == 0 {
		return sindlog.Nop()
	}

	level := slog.LevelInfo
	switch {
	case verbosity >= 3:
		level = sindlog.LevelTrace
	case verbosity >= 2:
		level = slog.LevelDebug
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			if a.Key == slog.LevelKey {
				if l, ok := a.Value.Any().(slog.Level); ok && l == sindlog.LevelTrace {
					a.Value = slog.StringValue("TRAC")
				}
			}
			return a
		},
	}))
}
