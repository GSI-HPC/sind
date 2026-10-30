// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execute runs a command line in ctx, with stdin as its input (nil leaves
// it at os.Stdin), and returns what it wrote to its output and error
// streams.
func execute(ctx context.Context, stdin io.Reader, args ...string) (string, string, error) {
	cmd := NewRootCommand()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	cmd.SetContext(ctx)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func executeCommand(args ...string) (string, string, error) {
	return execute(context.Background(), nil, args...)
}

func executeWithMock(mock *mock.Executor, args ...string) (string, string, error) {
	return execute(withClient(context.Background(), docker.NewClient(mock)), nil, args...)
}

func TestRootCommand_Help(t *testing.T) {
	stdout, _, err := executeCommand("--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "sind creates and manages containerized Slurm clusters")
}

func TestRootCommand_NoArgs(t *testing.T) {
	stdout, _, err := executeCommand()
	require.NoError(t, err)
	assert.Contains(t, stdout, "sind creates and manages containerized Slurm clusters")
}

// TestUnknownSubcommand verifies that command groups reject unknown or
// removed subcommands instead of printing help and exiting successfully.
func TestUnknownSubcommand(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"status", "default"}, `unknown command "status" for "sind" (it is now "get cluster")`},
		{[]string{"-v", "status"}, `unknown command "status" for "sind" (it is now "get cluster")`},
		{[]string{"gte"}, `unknown command "gte" for "sind" (did you mean "get"?)`},
		{[]string{"get", "clsuter"}, `unknown command "clsuter" for "sind get" (did you mean "cluster"?)`},
		{[]string{"create", "bogus"}, `unknown command "bogus" for "sind create"`},
		{[]string{"delete", "bogus"}, `unknown command "bogus" for "sind delete"`},
		{[]string{"power", "bogus"}, `unknown command "bogus" for "sind power"`},
		{[]string{"mcp", "claude", "bogus"}, `unknown command "bogus" for "sind mcp claude"`},
		{[]string{"completion", "fish-typo"}, `unknown command "fish-typo" for "sind completion"`},
		{[]string{"completion", "zshh"}, `unknown command "zshh" for "sind completion" (did you mean "zsh"?)`},
		{[]string{"help", "bogus"}, `unknown help topic "bogus": unknown command "bogus" for "sind" (did you mean "logs"?)`},
		{[]string{"help", "crate"}, `unknown help topic "crate": unknown command "crate" for "sind" (did you mean "create"?)`},
		{[]string{"help", "get", "bogus"}, `unknown help topic "get bogus": unknown command "bogus" for "sind get"`},
		{[]string{"help", "get", "clustr"}, `unknown help topic "get clustr": unknown command "clustr" for "sind get" (did you mean "cluster"?)`},
		{[]string{"help", "version", "extra"}, `unknown help topic "version extra": unknown command "extra" for "sind version"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			stdout, _, err := executeCommand(tt.args...)
			require.EqualError(t, err, tt.wantErr)
			assert.Empty(t, stdout)
		})
	}
}

// TestUnknownSubcommandWithHelp verifies that the help flag does not make
// an unknown subcommand succeed: a script that probes for one with --help
// gets the error it gets without.
func TestUnknownSubcommandWithHelp(t *testing.T) {
	for _, tt := range []struct {
		args    []string
		wantErr string
	}{
		{[]string{"get", "bogus", "--help"}, `unknown command "bogus" for "sind get"`},
		{[]string{"get", "munge-key", "-h"}, `unknown command "munge-key" for "sind get"`},
		{[]string{"power", "stop", "-h"}, `unknown command "stop" for "sind power" (did you mean "shutdown"?)`},
		{[]string{"--help", "bogus"}, `unknown command "bogus" for "sind" (did you mean "logs"?)`},
		{[]string{"get", "--help=bogus"}, `invalid --help value "bogus": want true or false`},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			stdout, _, err := executeCommand(tt.args...)
			require.EqualError(t, err, tt.wantErr)
			assert.True(t, cobratree.IsUsage(err))
			assert.Empty(t, stdout)
		})
	}
	for _, args := range [][]string{{"get", "--help"}, {"get", "-h"}, {"-h"}, {"mcp", "claude", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, err := executeCommand(args...)
			require.NoError(t, err)
			assert.Contains(t, stdout, "Available Commands:")
			assert.Contains(t, stdout, "  -h, --help ")
		})
	}
}

// TestHelpTopic verifies that help still prints the help of every command
// it names in full.
func TestHelpTopic(t *testing.T) {
	for _, args := range [][]string{
		{"help"},
		{"help", "get"},
		{"help", "get", "cluster"},
		{"help", "completion", "bash"},
		{"help", "help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, err := executeCommand(args...)
			require.NoError(t, err)
			assert.Contains(t, stdout, "Usage:")
		})
	}
}

func TestCommandGroup_NoArgsPrintsHelp(t *testing.T) {
	for _, group := range []string{"get", "create", "delete", "power", "mcp", "completion"} {
		t.Run(group, func(t *testing.T) {
			stdout, _, err := executeCommand(group)
			require.NoError(t, err)
			assert.Contains(t, stdout, "Available Commands:")
		})
	}
}

// TestRootCommand_RealmFlagAfterSubcommand verifies --realm can appear in any
// position — before or after the subcommand. A persistent flag is required for
// the flag to be recognized when placed past the subcommand.
func TestRootCommand_RealmFlagAfterSubcommand(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil) // empty container list for realms query

	// Flag placed AFTER the subcommand must parse cleanly.
	stdout, _, err := executeWithMock(&m, "get", "realms", "--realm", "some-realm", "-o", "json")
	require.NoError(t, err)
	assert.Equal(t, "[]\n", stdout)
}

// TestFlagHelpDefaults verifies that the help shows each flag's default
// once, and in words where pflag would show a misleading one.
func TestFlagHelpDefaults(t *testing.T) {
	tests := []struct {
		args    []string
		want    string
		notWant string
	}{
		{[]string{"create", "cluster", "--help"}, `"volume" for a Docker volume (default ".")`, "default: the working directory"},
		{[]string{"logs", "--help"}, "show only the last N lines (default: all lines; 0 shows none)", ""},
		{[]string{"mcp", "claude", "enable", "--help"}, "-e, --env KEY=VALUE ", "(default [])"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			stdout, _, err := executeCommand(tt.args...)
			require.NoError(t, err)
			assert.Contains(t, stdout, tt.want)
			if tt.notWant != "" {
				assert.NotContains(t, stdout, tt.notWant)
			}
		})
	}
}

// clusterArgs matches the arguments in the Use of a command that name a
// cluster, or nodes, whose cluster SIND_CLUSTER names when they name none.
var clusterArgs = regexp.MustCompile(`\b(NAME|CLUSTER|NODES?)\b`)

// TestEnvVarsInHelp checks that each variable of envvar.CommandLine stands
// in for the flag of its name (SIND_PROGRESS_LOG for --progress-log), or for
// SIND_CLUSTER the arguments that name a cluster, and that the usage of
// every flag of that name, or the help of every command with such an
// argument, names the variable.
func TestEnvVarsInHelp(t *testing.T) {
	seen := map[string]bool{}
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, name := range envvar.CommandLine {
			flag := strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(name, "SIND_")), "_", "-")
			if f := cmd.LocalFlags().Lookup(flag); f != nil {
				assert.Contains(t, f.Usage, "(default: "+name+", else ", "--%s of %s", flag, cmd.CommandPath())
				seen[name] = true
			}
		}
		if clusterArgs.MatchString(cmd.Use) {
			assert.Contains(t, cmd.Long, envvar.Cluster, "the help of %s", cmd.CommandPath())
			seen[envvar.Cluster] = true
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCommand())
	for _, name := range envvar.CommandLine {
		assert.True(t, seen[name], "%s stands in for no flag", name)
	}
}
