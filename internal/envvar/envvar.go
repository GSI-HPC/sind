// SPDX-License-Identifier: LGPL-3.0-or-later

// Package envvar names the SIND_* environment variables that sind reads, and
// reads them. A variable stands in for a flag, or for an argument, that the
// command line does not give: the command line comes first, then the
// variable, then the config file, which only create cluster reads, then the
// default. A variable set to the empty string counts as not set, except
// SIND_PROGRESS_LOG, whose empty value asks for no event log.
package envvar

import (
	"os"
	"slices"
)

// The variables that stand in for a flag or an argument.
const (
	// Realm stands in for --realm.
	Realm = "SIND_REALM"
	// Progress stands in for --progress.
	Progress = "SIND_PROGRESS"
	// ProgressLog stands in for --progress-log.
	ProgressLog = "SIND_PROGRESS_LOG"
)

// The variables that stand in for nothing on the command line.
const (
	// LogTimestamps, set to "false", leaves the time stamp off log lines.
	// The MCP server sets it for the sind processes of its tool calls,
	// whose stderr a call returns.
	LogTimestamps = "SIND_LOG_TIMESTAMPS"
	// MCPToken is the bearer token of sind mcp stream, which a command
	// line would show to the other users of the host.
	MCPToken = "SIND_MCP_TOKEN"
)

// CommandLine lists the variables that stand in for a flag or an argument.
// sind ui runs its commands without them, so that a run does what its
// command line says.
var CommandLine = []string{Realm, Progress, ProgressLog}

// All lists every variable that sind reads.
var All = append(slices.Clone(CommandLine), LogTimestamps, MCPToken)

// Lookup returns the value of the variable name, and whether it is set to
// one: an empty variable counts as not set.
func Lookup(name string) (string, bool) {
	value := os.Getenv(name)
	return value, value != ""
}
