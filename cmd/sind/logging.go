// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"io"
	"log/slog"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// newLogger builds a slog.Logger for the given verbosity level.
// At verbosity 0, only errors are shown. Higher levels add info, debug, and trace output.
func newLogger(w io.Writer, verbosity int) *slog.Logger {
	level := slog.LevelError
	switch {
	case verbosity >= 3:
		level = sindlog.LevelTrace
	case verbosity >= 2:
		level = slog.LevelDebug
	case verbosity >= 1:
		level = slog.LevelInfo
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
