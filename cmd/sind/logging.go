// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"io"
	"log/slog"

	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// newLogger builds a slog.Logger for the given verbosity level.
// At verbosity 0, only errors are shown. Higher levels add info, debug, and trace output.
// Uses charmbracelet/log for colorized, human-friendly output on TTYs.
func newLogger(w io.Writer, verbosity int) *slog.Logger {
	level := charmlog.ErrorLevel
	switch {
	case verbosity >= 3:
		level = charmlog.Level(sindlog.LevelTrace)
	case verbosity >= 2:
		level = charmlog.DebugLevel
	case verbosity >= 1:
		level = charmlog.InfoLevel
	}

	handler := charmlog.NewWithOptions(w, charmlog.Options{
		Level: level,
	})

	styles := charmlog.DefaultStyles()
	styles.Levels[charmlog.Level(sindlog.LevelTrace)] = lipgloss.NewStyle().
		SetString("TRAC").
		Bold(true).
		MaxWidth(4)
	handler.SetStyles(styles)

	return slog.New(handler)
}
