// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// envLogTimestamps names the variable that, set to "false", leaves the
// time stamp off log lines. The MCP server sets it for the sind processes
// of its tool calls (asMCPServer), whose stderr a call returns.
const envLogTimestamps = "SIND_LOG_TIMESTAMPS"

// newLogger builds a slog.Logger for the given verbosity level.
// At verbosity 0, only errors are shown. Higher levels add info, debug, and trace output.
// Uses charmbracelet/log for colorized, human-friendly output on TTYs.
func newLogger(w io.Writer, verbosity int) *slog.Logger {
	return slog.New(newLogHandler(w, verbosity))
}

// newLogHandler builds the charmbracelet/log handler of newLogger, in the
// colours w shows (colourProfile); a writer that is not the terminal
// itself, such as a progress display's (cliprogress.Start), sets them with
// SetColorProfile.
//
// The handler writes to w through terminalQuiet: on an *os.File that is a
// terminal, charmbracelet/log has termenv ask the terminal for its
// colours, and a terminal that does not answer, as under script, expect or
// docker run -t, holds every command for 10 seconds and gets the queries in
// its output.
func newLogHandler(w io.Writer, verbosity int) *charmlog.Logger {
	level := charmlog.ErrorLevel
	switch {
	case verbosity >= 3:
		level = charmlog.Level(sindlog.LevelTrace)
	case verbosity >= 2:
		level = charmlog.DebugLevel
	case verbosity >= 1:
		level = charmlog.InfoLevel
	}

	handler := charmlog.NewWithOptions(terminalQuiet{w}, charmlog.Options{
		Level:           level,
		TimeFormat:      "15:04:05.000",
		ReportTimestamp: os.Getenv(envLogTimestamps) != "false",
	})
	handler.SetColorProfile(colourProfile(w))

	styles := charmlog.DefaultStyles()
	styles.Levels[charmlog.Level(sindlog.LevelTrace)] = lipgloss.NewStyle().
		SetString("TRAC").
		Bold(true).
		MaxWidth(4)
	handler.SetStyles(styles)

	return handler
}

// terminalQuiet hides from termenv that the writer it holds may be a
// terminal, which termenv tells only of an *os.File. It is a value, so
// that the loggers built on one writer share the renderer charmbracelet/log
// keeps for it.
type terminalQuiet struct{ io.Writer }
