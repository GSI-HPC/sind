// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/mcpconfig"
	"github.com/GSI-HPC/sind/pkg/config"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// mcpServerName is the name `sind mcp <editor> enable` registers sind's
// MCP server under unless --server-name says otherwise.
const mcpServerName = "sind"

// mcpEditor is an editor whose MCP configuration `sind mcp <editor>`
// edits.
type mcpEditor struct {
	name  string // the subcommand: claude, vscode, cursor
	title string // the editor's name in help texts
	// key is the top-level member that holds the servers.
	key string
	// stdio sets "type": "stdio" in the entry, which VS Code requires.
	stdio bool
	// workspace is the directory of the workspace configuration, which
	// --workspace selects, or empty if the editor has none.
	workspace string
	// userDir returns the directory of userFile, the user configuration.
	userDir  func() (string, error)
	userFile string
}

// mcpEditors are the editors sind registers its MCP server with, at the
// paths ophis used.
var mcpEditors = []mcpEditor{
	{
		name:     "claude",
		title:    "Claude Desktop",
		key:      "mcpServers",
		userDir:  os.UserConfigDir,
		userFile: "Claude/claude_desktop_config.json",
	},
	{
		name:      "vscode",
		title:     "VS Code",
		key:       "servers",
		stdio:     true,
		workspace: ".vscode",
		userDir:   os.UserConfigDir,
		userFile:  "Code/User/mcp.json",
	},
	{
		name:      "cursor",
		title:     "Cursor",
		key:       "mcpServers",
		stdio:     true,
		workspace: ".cursor",
		userDir:   os.UserHomeDir,
		userFile:  ".cursor/mcp.json",
	},
}

// mcpEditorFlags are the flags of an editor's subcommands.
type mcpEditorFlags struct {
	configPath string
	workspace  bool
	serverName string
	logLevel   string
	env        map[string]string
}

// newMCPEditorCommand returns `sind mcp <editor>` with its enable, disable
// and list subcommands. They edit the editor's configuration in place
// (internal/mcpconfig): ophis's own versions rewrite the whole file from
// the fields they know, which drops the editor's other settings and the
// other servers' fields, and fail on a VS Code file with comments.
func newMCPEditorCommand(e mcpEditor) *cobra.Command {
	cmd := &cobra.Command{
		Use:   e.name,
		Short: "Manage " + e.title + " MCP servers",
	}
	cmd.AddCommand(newMCPEnableCommand(e), newMCPDisableCommand(e), newMCPListCommand(e))
	return cmd
}

func newMCPEnableCommand(e mcpEditor) *cobra.Command {
	f := &mcpEditorFlags{}
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Register sind's MCP server with " + e.title,
		Long: `Register sind's MCP server with ` + e.title + `: add an entry that runs
"sind mcp start", by the absolute path of this sind binary, or replace the
entry of that name, which keeps its environment, unless --env or the realm
change it, and its other settings. The rest of the configuration file stays
as it is, and the file as it was is first copied to <file>.<time>.bak. The
realm in effect, of the root --realm or SIND_REALM, is set as SIND_REALM in
the entry's environment, so the server serves that realm.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPEnable(cmd, e, f)
		},
	}
	addMCPEditorFlags(cmd, e, f)
	cmd.Flags().StringVar(&f.serverName, "server-name", mcpServerName, "name of the MCP server entry")
	cmd.Flags().StringVar(&f.logLevel, "log-level", "", "log level of the MCP server (debug, info, warn, error)")
	// The back quotes name the value KEY=VALUE rather than pflag's type, and
	// the help shows no default, which pflag would print as [].
	cmd.Flags().StringToStringVarP(&f.env, "env", "e", nil, "environment variable of the MCP server, `KEY=VALUE` (repeatable)")
	cmd.Flags().Lookup("env").DefValue = ""
	return cmd
}

func newMCPDisableCommand(e mcpEditor) *cobra.Command {
	f := &mcpEditorFlags{}
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Remove sind's MCP server from " + e.title,
		Long: `Remove the entry of sind's MCP server from ` + e.title + `'s configuration.
The rest of the file stays as it is, and the file as it was is first copied
to <file>.<time>.bak. A server that is not registered is an error.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPDisable(cmd, e, f)
		},
	}
	addMCPEditorFlags(cmd, e, f)
	cmd.Flags().StringVar(&f.serverName, "server-name", mcpServerName, "name of the MCP server entry")
	return cmd
}

func newMCPListCommand(e mcpEditor) *cobra.Command {
	f := &mcpEditorFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the MCP servers registered with " + e.title,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPList(cmd, e, f)
		},
	}
	addMCPEditorFlags(cmd, e, f)
	return cmd
}

// addMCPEditorFlags adds the flags that select the configuration file.
func addMCPEditorFlags(cmd *cobra.Command, e mcpEditor, f *mcpEditorFlags) {
	cmd.Flags().StringVar(&f.configPath, "config-path", "", "path to the "+e.title+" configuration file")
	if e.workspace != "" {
		cmd.Flags().BoolVar(&f.workspace, "workspace", false, "use the workspace configuration ("+filepath.Join(e.workspace, "mcp.json")+") instead of the user's")
	}
}

// path returns the configuration file the flags select.
func (f *mcpEditorFlags) path(e mcpEditor) (string, error) {
	switch {
	case f.configPath != "":
		return f.configPath, nil
	case f.workspace:
		return filepath.Join(e.workspace, "mcp.json"), nil
	}
	dir, err := e.userDir()
	if err != nil {
		return "", fmt.Errorf("locating the %s configuration: %w", e.title, err)
	}
	return filepath.Join(dir, e.userFile), nil
}

// loadMCPConfig loads the configuration file the flags of cmd select. An
// empty --config-path or --server-name is a usage error: an unset variable
// in --config-path "$FILE" would otherwise edit the editor's own file.
func loadMCPConfig(cmd *cobra.Command, e mcpEditor, f *mcpEditorFlags) (*mcpconfig.File, error) {
	if cmd.Flags().Changed("config-path") && f.configPath == "" {
		return nil, cobratree.Usagef("--config-path must not be empty: give a file, or leave the flag out for the default one")
	}
	if cmd.Flags().Lookup("server-name") != nil && f.serverName == "" {
		return nil, cobratree.Usagef("--server-name must not be empty")
	}
	if f.configPath != "" && f.workspace {
		return nil, cobratree.Usagef("--config-path and --workspace are mutually exclusive")
	}
	path, err := f.path(e)
	if err != nil {
		return nil, err
	}
	return mcpconfig.Load(path, e.key)
}

// saveMCPConfig writes the configuration back and logs its backup.
func saveMCPConfig(cmd *cobra.Command, cfg *mcpconfig.File) error {
	backup, err := cfg.Save(time.Now())
	if backup != "" {
		sindlog.From(cmd.Context()).InfoContext(cmd.Context(), "backed up MCP configuration", "file", cfg.Path(), "backup", backup)
	}
	return err
}

func runMCPEnable(cmd *cobra.Command, e mcpEditor, f *mcpEditorFlags) error {
	entry, err := mcpEntry(cmd, e, f)
	if err != nil {
		return err
	}
	cfg, err := loadMCPConfig(cmd, e, f)
	if err != nil {
		return err
	}
	// Run again, as after sind moved, enable keeps what the entry had and
	// the flags do not change: its environment, with the realm the server
	// serves, and the members sind does not write (cfg.Set).
	if old, ok := cfg.Server(entry.Name); ok && len(old.Env) > 0 {
		env := maps.Clone(old.Env)
		maps.Copy(env, entry.Env)
		entry.Env = env
	}
	cfg.Set(entry)
	return saveMCPConfig(cmd, cfg)
}

// mcpEntry returns the entry enable writes: this binary, by its absolute
// path, running `mcp start`, with the environment of --env and the realm in
// effect, of the root's --realm or SIND_REALM. An editor starts its MCP
// servers with its own environment, not the shell's, so a realm the entry
// does not name is the default one.
func mcpEntry(cmd *cobra.Command, e mcpEditor, f *mcpEditorFlags) (mcpconfig.Server, error) {
	exe, err := os.Executable()
	if err != nil {
		return mcpconfig.Server{}, fmt.Errorf("locating the sind binary: %w", err)
	}
	entry := mcpconfig.Server{Name: f.serverName, Command: exe, Args: []string{"mcp", "start"}}
	if e.stdio {
		entry.Type = "stdio"
	}
	if err := checkMCPLogLevel(f.logLevel); err != nil {
		return mcpconfig.Server{}, err
	}
	if f.logLevel != "" {
		entry.Args = append(entry.Args, "--log-level", f.logLevel)
	}
	if env, ok := f.env["SIND_REALM"]; ok {
		if err := config.CheckName("realm", env); err != nil {
			return mcpconfig.Server{}, cobratree.Usagef("--env SIND_REALM: %w", err)
		}
	}
	if len(f.env) > 0 {
		entry.Env = f.env
	}
	// The realm in effect, as every other command takes it: --realm, or
	// else SIND_REALM, unless --env names one.
	_, envRealm := f.env["SIND_REALM"]
	if cmd.Root().Flags().Changed("realm") || (!envRealm && os.Getenv("SIND_REALM") != "") {
		realm, err := realmFromFlag(cmd)
		if err != nil {
			return mcpconfig.Server{}, err
		}
		if env, ok := f.env["SIND_REALM"]; ok && env != realm {
			return mcpconfig.Server{}, cobratree.Usagef("--realm %s and --env SIND_REALM=%s name different realms", realm, env)
		}
		if entry.Env == nil {
			entry.Env = map[string]string{}
		}
		entry.Env["SIND_REALM"] = realm
	}
	return entry, nil
}

func runMCPDisable(cmd *cobra.Command, e mcpEditor, f *mcpEditorFlags) error {
	cfg, err := loadMCPConfig(cmd, e, f)
	if err != nil {
		return err
	}
	if !cfg.Remove(f.serverName) {
		return fmt.Errorf("MCP server %q is not registered in %s", f.serverName, cfg.Path())
	}
	return saveMCPConfig(cmd, cfg)
}

// runMCPList prints the servers of the configuration, a server run by a
// command with its arguments, one reached over HTTP with its URL. It
// leaves out their environment, which can hold secrets.
func runMCPList(cmd *cobra.Command, e mcpEditor, f *mcpEditorFlags) error {
	cfg, err := loadMCPConfig(cmd, e, f)
	if err != nil {
		return err
	}
	t := newTable("NAME", "COMMAND")
	servers := cfg.Servers()
	for _, s := range servers {
		command := s.URL
		if s.Command != "" {
			command = strings.Join(append([]string{s.Command}, s.Args...), " ")
		}
		t.Add(cell(s.Name), cell(command))
	}
	if err := writeTable(cmd.OutOrStdout(), t); err != nil {
		return err
	}
	// The file names where sind looked: a mistyped --config-path reads
	// as an empty file.
	noteNone(cmd, len(servers), "MCP servers", strconv.Quote(cfg.Path()))
	return nil
}
