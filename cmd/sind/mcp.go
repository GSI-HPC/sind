// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"maps"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// mcpStreamHost is the address `sind mcp stream` listens on unless --host
// says otherwise. The stream has no authentication, and its tools create
// and delete containers, so it is not offered to the network by default.
const mcpStreamHost = "127.0.0.1"

// mcpExcluded lists the commands, by path below the root, that are not
// offered as MCP tools although they could run: interactive ones, which
// have no terminal under MCP, and ones that print a secret.
var mcpExcluded = map[string]bool{
	"enter":               true, // interactive shell
	"ssh":                 true, // interactive shell or remote command without a terminal
	"get ssh-private-key": true, // the mesh's SSH private key
	"get munge-key":       true, // the cluster's munge key
}

// mcpEffect is what an MCP tool does to its environment, which sets the
// readOnlyHint and destructiveHint annotations clients use to decide
// whether to ask before a call.
type mcpEffect int

const (
	// readOnly tools only report state.
	readOnly mcpEffect = iota
	// additive tools create resources and destroy none.
	additive
	// destructive tools remove resources, stop nodes or run arbitrary
	// commands.
	destructive
)

// mcpEffects classifies every MCP tool, by command path below the root.
var mcpEffects = map[string]mcpEffect{
	"doctor":              readOnly,
	"version":             readOnly,
	"logs":                readOnly,
	"get cluster":         readOnly,
	"get clusters":        readOnly,
	"get dns":             readOnly,
	"get mesh":            readOnly,
	"get networks":        readOnly,
	"get node":            readOnly,
	"get nodes":           readOnly,
	"get realms":          readOnly,
	"get ssh-config":      readOnly,
	"get ssh-known-hosts": readOnly,
	"get ssh-public-key":  readOnly,
	"get volumes":         readOnly,
	"create cluster":      additive,
	"create worker":       additive,
	"power on":            additive,
	"power unfreeze":      additive,
	"delete cluster":      destructive,
	"delete worker":       destructive,
	"exec":                destructive,
	"power cut":           destructive,
	"power cycle":         destructive,
	"power freeze":        destructive,
	"power reboot":        destructive,
	"power shutdown":      destructive,
}

// annotateMCPTools sets the MCP hint annotations of every command that
// mcpEffects classifies.
func annotateMCPTools(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		annotateMCPTools(sub)
	}
	effect, ok := mcpEffects[commandPath(cmd)]
	if !ok {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[ophis.AnnotationReadOnly] = strconv.FormatBool(effect == readOnly)
	if effect != readOnly {
		cmd.Annotations[ophis.AnnotationDestructive] = strconv.FormatBool(effect == destructive)
	}
}

// mcpConfig returns the ophis configuration of sind's MCP server.
func mcpConfig() *ophis.Config {
	return &ophis.Config{
		Selectors: []ophis.Selector{
			{
				// Commands with -o always return JSON, which an agent
				// reads more reliably than a table.
				CmdSelector: func(cmd *cobra.Command) bool {
					return isMCPTool(cmd) && cmd.Flag("output") != nil
				},
				LocalFlagSelector:     isMCPFlagNotOutput,
				InheritedFlagSelector: isMCPFlagNotOutput,
				Middleware:            forceJSONOutput,
			},
			{
				CmdSelector:           isMCPTool,
				LocalFlagSelector:     isMCPFlag,
				InheritedFlagSelector: isMCPFlag,
			},
		},
	}
}

// isMCPTool reports whether cmd is offered as an MCP tool. Command groups,
// the root among them, are not: they only print help, and they are
// runnable only so that an unknown subcommand fails.
func isMCPTool(cmd *cobra.Command) bool {
	return !cmd.HasSubCommands() && !mcpExcluded[commandPath(cmd)]
}

// mcpExcludedFlags lists the flags that are not offered to MCP tools.
var mcpExcludedFlags = map[string]bool{
	// A count flag is passed as "--verbose 2", which pflag reads as
	// --verbose and a stray positional argument: unknown command "2".
	"verbose": true,
	// logs --follow never ends, and a tool's output is returned only when
	// the command exits.
	"follow": true,
}

// isMCPFlag reports whether a flag is offered in a tool's input schema.
func isMCPFlag(f *pflag.Flag) bool {
	return !mcpExcludedFlags[f.Name]
}

// isMCPFlagNotOutput is isMCPFlag for a tool whose -o the server sets.
func isMCPFlagNotOutput(f *pflag.Flag) bool {
	return isMCPFlag(f) && f.Name != "output"
}

// forceJSONOutput runs a tool with -o json, whatever flags it was given.
func forceJSONOutput(ctx context.Context, req *mcp.CallToolRequest, in ophis.ToolInput, next ophis.ExecuteFunc) (*mcp.CallToolResult, ophis.ToolOutput, error) {
	flags := make(map[string]any, len(in.Flags)+1)
	maps.Copy(flags, in.Flags)
	flags["output"] = "json"
	in.Flags = flags
	return next(ctx, req, in)
}

// commandPath returns the path of cmd below the root, e.g. "get munge-key".
func commandPath(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

// newMCPCommand returns the `sind mcp` command group, which serves sind's
// commands as MCP tools through ophis.
func newMCPCommand(cfg *ophis.Config) *cobra.Command {
	cmd := ophis.Command(cfg)
	for _, sub := range cmd.Commands() {
		if sub.Name() != "stream" {
			continue
		}
		// ophis listens on all interfaces by default.
		host := sub.Flags().Lookup("host")
		host.DefValue = mcpStreamHost
		_ = host.Value.Set(mcpStreamHost)
		host.Usage = "host to listen on (use 0.0.0.0 for all interfaces)"
	}
	return cmd
}
