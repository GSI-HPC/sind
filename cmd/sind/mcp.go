// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
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
				Middleware:            refuseFlagArgs(forceJSONOutput),
			},
			{
				CmdSelector:           isMCPTool,
				LocalFlagSelector:     isMCPFlag,
				InheritedFlagSelector: isMCPFlag,
				Middleware:            refuseFlagArgs(nil),
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

// refuseFlagArgs returns a middleware that refuses a tool call whose
// positional arguments hold a flag, and otherwise calls next, or the tool
// itself when next is nil. ophis puts the arguments after the flags on the
// command line, so a flag among them would get past the input schema: -v,
// --follow, or a -o that overrides the -o json forceJSONOutput sets. exec
// passes everything after its own "--" to the node, so only the arguments
// before it are checked.
func refuseFlagArgs(next ophis.MiddlewareFunc) ophis.MiddlewareFunc {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ophis.ToolInput, run ophis.ExecuteFunc) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		for _, arg := range in.Args {
			if arg == "--" && req.Params.Name == "sind_exec" {
				break
			}
			if strings.HasPrefix(arg, "-") {
				return nil, ophis.ToolOutput{}, fmt.Errorf("argument %q is a flag; pass flags in \"flags\"", arg)
			}
		}
		if next == nil {
			return run(ctx, req, in)
		}
		return next(ctx, req, in, run)
	}
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
		switch sub.Name() {
		case "start", "stream":
			withServerVersion(sub)
		}
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

// withServerVersion makes a server command report sind's version to MCP
// clients. ophis takes the server version from the root command's Version,
// which is set only while the server runs: set on the root for good, it
// would give every sind command a --version flag.
func withServerVersion(cmd *cobra.Command) {
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmd.Root().Version = strings.TrimPrefix(resolveVersion(), "v")
		return run(cmd, args)
	}
}
