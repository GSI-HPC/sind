// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/go-clikit/progress/cliprogress"
	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
)

// mcpStreamHost is the address `sind mcp stream` listens on unless --host
// says otherwise. Its tools create and delete containers, so it is not
// offered to the network by default; the bearer token it requires is what
// keeps out the other users of this host, who reach loopback too.
const mcpStreamHost = "127.0.0.1"

// mcpExcluded lists the commands, by path below the root, that are not
// offered as MCP tools although they could run: interactive ones, which
// have no terminal under MCP, and ones that print a secret.
var mcpExcluded = map[string]bool{
	"enter":               true, // interactive shell
	"ssh":                 true, // interactive shell or remote command without a terminal
	"get ssh-private-key": true, // the mesh's SSH private key
	"get auth-key":        true, // the cluster's munge key or slurm.key
	"ui":                  true, // serves a web UI until interrupted
}

// mcpEffect is what an MCP tool does to its environment, which sets the
// readOnlyHint and destructiveHint annotations clients use to decide
// whether to ask before a call.
type mcpEffect int

const (
	// readOnly tools only report state.
	readOnly mcpEffect = iota
	// additive tools restore resources and destroy none.
	additive
	// destructive tools remove resources, stop nodes or run arbitrary
	// code. create cluster and create worker count: their flags choose
	// the image a node runs as root, extra capabilities, devices, security
	// options, a config file and a host directory mounted read-write, so
	// one call can run code with host-root power.
	destructive
)

// mcpEffects classifies every MCP tool, by command path below the root.
var mcpEffects = map[string]mcpEffect{
	"doctor":               readOnly,
	"version":              readOnly,
	"logs":                 readOnly,
	"get cluster":          readOnly,
	"get clusters":         readOnly,
	"get coredns-config":   readOnly,
	"get dns":              readOnly,
	"get mesh":             readOnly,
	"get monitoring":       readOnly,
	"get networks":         readOnly,
	"get node":             readOnly,
	"get nodes":            readOnly,
	"get realms":           readOnly,
	"get ssh-config":       readOnly,
	"get ssh-known-hosts":  readOnly,
	"get ssh-public-key":   readOnly,
	"get volumes":          readOnly,
	"connect container":    additive,
	"power on":             additive,
	"power unfreeze":       additive,
	"create cluster":       destructive,
	"create worker":        destructive,
	"delete cluster":       destructive,
	"delete realm":         destructive,
	"delete worker":        destructive,
	"disconnect container": destructive,
	"exec":                 destructive,
	"power cut":            destructive,
	"power cycle":          destructive,
	"power freeze":         destructive,
	"power reboot":         destructive,
	"power shutdown":       destructive,
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
				LocalFlagSelector:     ophis.ExcludeFlags(append(mcpExcludedFlags, "output")...),
				InheritedFlagSelector: ophis.ExcludeFlags(append(mcpExcludedFlags, "output")...),
				Middleware:            inProcessGroup(refuseFlagArgs(forceJSONOutput)),
			},
			{
				CmdSelector:           isMCPTool,
				LocalFlagSelector:     ophis.ExcludeFlags(mcpExcludedFlags...),
				InheritedFlagSelector: ophis.ExcludeFlags(mcpExcludedFlags...),
				Middleware:            inProcessGroup(refuseFlagArgs(nil)),
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
var mcpExcludedFlags = []string{
	// A count flag is passed as "--verbose 2", which pflag reads as
	// --verbose and a stray positional argument: unknown command "2".
	"verbose",
	// logs --follow never ends, and a tool's output is returned only when
	// the command exits.
	"follow",
	// A tool's output is no terminal, and holds no progress and no
	// events (asMCPServer).
	"progress", "progress-log",
}

// mcpNoDefault lists the flags, by command path below the root, whose input
// schema has no default. ophis takes a flag's default from its DefValue,
// but these flags mean something else when they are not given than with
// that value, which a client that fills in the defaults of a schema would
// send: logs --tail 0 shows no line, and logs without it every line; the
// create commands without --wait and --pull take SIND_WAIT and SIND_PULL
// from the server's environment, and --wait 0 waits without a limit.
var mcpNoDefault = map[string][]string{
	"logs":           {"tail"},
	"create cluster": {"wait", "pull"},
	"create worker":  {"wait", "pull"},
}

// mcpFlagUsage replaces, by command path below the root and flag name, the
// help of a flag that describes a form a tool call cannot use: a tool's
// sind process reads no stdin, so create cluster --config - would read
// an empty configuration.
var mcpFlagUsage = map[string]map[string]string{
	"create cluster": {"config": "path to cluster configuration file"},
}

// withMCPFlagSchemas makes cmd, `sind mcp start` or `sind mcp tools`,
// apply mcpNoDefault and mcpFlagUsage to the flags before ophis builds the
// tools from them. It changes the flags of this process alone, which only
// serves or exports the tools, so the help of the commands stays as it is.
func withMCPFlagSchemas(cmd *cobra.Command) {
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		adaptMCPFlags(cmd.Root())
		return run(cmd, args)
	}
}

// adaptMCPFlags applies mcpNoDefault and mcpFlagUsage to cmd and the
// commands below it.
func adaptMCPFlags(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		adaptMCPFlags(sub)
	}
	path := commandPath(cmd)
	for _, name := range mcpNoDefault[path] {
		cmd.Flags().Lookup(name).DefValue = ""
	}
	for name, usage := range mcpFlagUsage[path] {
		cmd.Flags().Lookup(name).Usage = usage
	}
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
// positional arguments hold a flag, as a usage error that reads as the
// tool's own (exit status 2, isError), and otherwise calls next, or the tool
// itself when next is nil, with the flags of emptyListsAsNone. ophis puts
// the arguments after the flags on the command line, so a flag among them
// would get past the input schema: -v, --follow, or a -o that overrides the
// -o json forceJSONOutput sets. exec passes everything after its own "--"
// to the node, so only the arguments before it are checked.
func refuseFlagArgs(next ophis.MiddlewareFunc) ophis.MiddlewareFunc {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ophis.ToolInput, run ophis.ExecuteFunc) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		for _, arg := range in.Args {
			if arg == "--" && req.Params.Name == "sind_exec" {
				break
			}
			if strings.HasPrefix(arg, "-") {
				return &mcp.CallToolResult{IsError: true}, ophis.ToolOutput{
					StdErr:   fmt.Sprintf("ERRO argument %q is a flag; pass flags in \"flags\"\n", arg),
					ExitCode: exitUsage,
				}, nil
			}
		}
		in.Flags = emptyListsAsNone(in.Flags)
		if mcpRunsToCompletion(req.Params.Name) {
			ctx = context.WithoutCancel(ctx)
		}
		run = failedAsError(run)
		if next == nil {
			return run(ctx, req, in)
		}
		return next(ctx, req, in, run)
	}
}

// inProcessGroup returns a middleware that calls mw with mcpExecute in
// place of ophis's own run function.
func inProcessGroup(mw ophis.MiddlewareFunc) ophis.MiddlewareFunc {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ophis.ToolInput, _ ophis.ExecuteFunc) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		return mw(ctx, req, in, mcpExecute)
	}
}

// mcpExecute runs a tool call as ophis does, the sind binary with the
// call's command path, flags and arguments (mcpToolArgs), but in a process
// group of its own (cmdexec.WithProcessGroup). ophis kills only the sind
// process when the call's context ends, and the docker exec or docker logs
// it started would go on, holding its output open: the cancelled call
// would answer only when that docker command ended, if ever. Killing the
// group ends both, and the call answers at once.
//
// A call that came with a progress token gets the progress of the process
// as progress notifications (withToolProgress).
func mcpExecute(ctx context.Context, req *mcp.CallToolRequest, in ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, ophis.ToolOutput{}, fmt.Errorf("locating the sind binary: %w", err)
	}
	ctx, endProgress := withToolProgress(cmdexec.WithProcessGroup(ctx), req)
	var e cmdexec.OSExecutor
	stdout, stderr, err := e.Run(ctx, exe, mcpToolArgs(req.Params.Name, in)...)
	endProgress()
	out := ophis.ToolOutput{StdOut: stdout, StdErr: stderr}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		out.ExitCode = exitErr.ExitCode()
	} else if err != nil {
		return nil, ophis.ToolOutput{}, err
	}
	return nil, out, nil
}

// mcpToolArgs returns the command line of a tool call, as ophis builds it:
// the command path from the tool's name ("sind_get_nodes" is "get nodes"),
// the flags, and the positional arguments. A list flag is given once per
// item, a map flag once per KEY=VALUE, a boolean flag only when true, and
// a null flag not at all. The flags come in name order.
func mcpToolArgs(tool string, in ophis.ToolInput) []string {
	args := strings.Split(tool, "_")[1:]
	for _, name := range slices.Sorted(maps.Keys(in.Flags)) {
		switch value := in.Flags[name].(type) {
		case []any:
			for _, item := range value {
				args = append(args, mcpFlagArgs(name, item)...)
			}
		case map[string]any:
			for _, key := range slices.Sorted(maps.Keys(value)) {
				args = append(args, "--"+name, fmt.Sprintf("%s=%v", key, value[key]))
			}
		default:
			args = append(args, mcpFlagArgs(name, value)...)
		}
	}
	return append(args, in.Args...)
}

// mcpFlagArgs returns the arguments that give the flag name the value.
func mcpFlagArgs(name string, value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		if v {
			return []string{"--" + name}
		}
		return nil
	default:
		return []string{"--" + name, fmt.Sprint(v)}
	}
}

// failedAsError wraps a tool run so that a command that exits non-zero
// comes back as an error result (isError: true). ophis returns it as an
// ordinary result with the exit code in the output, and clients that key
// on isError would take the failure for a success.
func failedAsError(run ophis.ExecuteFunc) ophis.ExecuteFunc {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		res, out, err := run(ctx, req, in)
		if err != nil || out.ExitCode == 0 {
			return res, out, err
		}
		if res == nil {
			res = &mcp.CallToolResult{}
		}
		res.IsError = true
		return res, out, nil
	}
}

// mcpRunsToCompletion reports whether the tool changes state and so runs
// on after its call is cancelled or its client disconnects. mcpExecute
// kills a tool's sind process and the docker commands it started with
// SIGKILL when the call's context ends, which skips the rollback of a
// failed create and leaves partial resources and the realm lock behind.
// Run to completion, the command finishes or rolls back on its own,
// bounded by its own timeouts (--wait), and the call answers when it ends.
// Read-only tools and exec keep their cancellation: exec runs a command in
// a node that nothing rolls back, and a client that stops waiting for it
// means to stop it. Killing docker exec does not stop that command, though:
// Docker has no way to signal a process that docker exec started.
func mcpRunsToCompletion(tool string) bool {
	for path, effect := range mcpEffects {
		if mcpToolName(path) == tool {
			return effect != readOnly && path != "exec"
		}
	}
	return false
}

// mcpToolName returns the name ophis gives the tool of a command path:
// "create cluster" is "sind_create_cluster", "get ssh-config" is
// "sind_get_ssh-config".
func mcpToolName(path string) string {
	return "sind_" + strings.ReplaceAll(path, " ", "_")
}

// emptyListsAsNone returns flags with each empty array replaced by an
// array of one empty string, which ophis passes as the flag given empty
// (--device ""): for an empty array it passes no flag at all, and a list
// flag of create worker that is not given inherits the newest worker's
// list instead of asking for none (see listFlag). It copies flags rather
// than change them.
func emptyListsAsNone(flags map[string]any) map[string]any {
	var out map[string]any
	for name, value := range flags {
		if items, ok := value.([]any); ok && len(items) == 0 {
			if out == nil {
				out = maps.Clone(flags)
			}
			out[name] = []any{""}
		}
	}
	if out == nil {
		return flags
	}
	return out
}

// commandPath returns the path of cmd below the root, e.g. "get auth-key".
func commandPath(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

// newMCPCommand returns the `sind mcp` command group, which serves sind's
// commands as MCP tools through ophis. `sind mcp stream` keeps ophis's
// flags but runs sind's own HTTP server, which requires a bearer token
// (runMCPStream), and the editor commands are sind's own
// (newMCPEditorCommand).
func newMCPCommand(cfg *ophis.Config) *cobra.Command {
	cmd := ophis.Command(cfg)
	// ophis's help strings are worded unlike sind's own: noun phrases and
	// capitalized flag usages.
	cmd.Short = "Serve sind's commands as MCP tools"
	cmd.Long = `Serve sind's commands as MCP tools to AI assistants, over stdio (start) or
HTTP (stream), and register the server with an editor.`
	var start, stream *cobra.Command
	for _, sub := range cmd.Commands() {
		switch sub.Name() {
		case "start":
			asMCPServer(sub)
			withMCPFlagSchemas(sub)
			start = sub
			sub.Short = "Serve the MCP tools on stdio"
			sub.Long = `Serve sind's MCP tools on standard input and output, for an AI assistant
that starts sind as its MCP server.`
		case "stream":
			stream = sub
			sub.Short = "Serve the MCP tools over HTTP"
		case "tools":
			withMCPFlagSchemas(sub)
			sub.Short = "Write the MCP tool definitions to mcp-tools.json"
			sub.Long = `Write the definitions of the MCP tools that start and stream serve, with
their input schemas, to mcp-tools.json in the working directory.`
		case "claude", "vscode", "cursor":
			cmd.RemoveCommand(sub)
			continue
		}
		if f := sub.Flags().Lookup("log-level"); f != nil {
			f.Usage = "log level of the MCP server (debug, info, warn, error)"
		}
	}
	for _, e := range mcpEditors {
		cmd.AddCommand(newMCPEditorCommand(e))
	}
	stream.Long = `Serve sind's MCP tools over streamable HTTP. Every request needs the
header "Authorization: Bearer <token>". The token is ` + envvar.MCPToken + ` or, when
that is not set, a new random one, written at every start to ` + mcpTokenFile + `
in sind's state directory ($XDG_STATE_HOME/sind or ~/.local/state/sind).`
	stream.RunE = func(cmd *cobra.Command, _ []string) error {
		return runMCPStream(cmd, cfg, start)
	}
	// ophis listens on all interfaces by default.
	host := stream.Flags().Lookup("host")
	host.DefValue = mcpStreamHost
	_ = host.Value.Set(mcpStreamHost)
	host.Usage = "host to listen on (use 0.0.0.0 for all interfaces)"
	refuseArgs(cmd)
	return cmd
}

// asMCPServer prepares `sind mcp start`, which `sind mcp stream` runs too,
// for the sind processes it runs for its tool calls, once it checked its
// --log-level, which ophis would read as info if it does not know it. The
// tool processes run with the call's own flags and this process's
// environment, so it sets:
//   - SIND_REALM to the root's --realm, or `sind --realm ci mcp start`
//     would serve the default realm;
//   - SIND_PROGRESS=none and no SIND_PROGRESS_LOG: a SIND_PROGRESS=plain
//     set for the user's shell would add its lines to every tool's output,
//     a SIND_PROGRESS_LOG of /dev/stderr its events, and one that cannot be
//     opened a note. Their stderr is no terminal, so nothing else would
//     draw. With the variable unset, a tool call writes the default
//     activity log, as every command does, and nothing on its stderr;
//   - SIND_LOG_TIMESTAMPS=false: the time an error line was written means
//     nothing to the client, which gets the line with the call's result;
//   - the root's Version, which ophis reports to MCP clients, only while
//     the server runs: set on the root for good, it would give every sind
//     command a --version flag.
func asMCPServer(cmd *cobra.Command) {
	run := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		logLevel, _ := cmd.Flags().GetString("log-level")
		if err := checkMCPLogLevel(logLevel); err != nil {
			return err
		}
		root := cmd.Root()
		if root.Flags().Changed("realm") {
			realm, _ := root.Flags().GetString("realm")
			_ = os.Setenv(envvar.Realm, realm)
		}
		_ = os.Setenv(envvar.Progress, cliprogress.ModeNone.String())
		_ = os.Unsetenv(envvar.ProgressLog)
		_ = os.Setenv(envvar.LogTimestamps, "false")
		root.Version = strings.TrimPrefix(resolveVersion(), "v")
		return run(cmd, args)
	}
}

// mcpLogLevels are the values of the --log-level of `sind mcp start` and
// `sind mcp stream`, which ophis reads case-insensitively and turns any
// other value into info.
var mcpLogLevels = []string{"debug", "info", "warn", "error"}

// checkMCPLogLevel returns a usage error for a --log-level value that is
// not one of mcpLogLevels; empty is ophis's default.
func checkMCPLogLevel(level string) error {
	if level == "" || slices.Contains(mcpLogLevels, strings.ToLower(level)) {
		return nil
	}
	return invalidFlagValue("log-level", level, mcpLogLevels...)
}

// refuseArgs makes the commands of ophis's tree that take no arguments refuse
// them, as sind's own commands do (cobratree.Strict then makes that a usage
// error): ophis leaves Args unset, which accepts any.
func refuseArgs(cmd *cobra.Command) {
	if cmd.Args == nil && cmd.Runnable() && !cmd.HasSubCommands() {
		cmd.Args = noArgs
	}
	for _, sub := range cmd.Commands() {
		refuseArgs(sub)
	}
}
