// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The golden files of testdata hold what scripts and tools depend on: the
// commands and flags of sind (cli.txt) and the JSON output of each command
// that has one (json/). A test fails when its output differs from its
// file, so that no change to the surface is made without a look at it.
// When the change is intended, write the files again and review their
// diff, with the docs and DESIGN.md in the same commit:
//
//	go test ./cmd/sind -run 'JSON|TestCLISurface' -update
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden compares got with testdata/name, or writes it there with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(file, []byte(got), 0o644))
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err, "run go test ./cmd/sind -update to write the file")
	assert.Equal(t, string(want), got, "testdata/%s: if the change is intended, run go test ./cmd/sind -update", name)
}

// goldenJSON compares the JSON output of a command with
// testdata/json/name.json: its fields, their order and its layout.
func goldenJSON(t *testing.T, name, stdout string) {
	t.Helper()
	require.True(t, json.Valid([]byte(stdout)), "the output is JSON: %s", stdout)
	golden(t, filepath.Join("json", name+".json"), stdout)
}

// The commands of sind, with the arguments, aliases and flags of each one,
// are those of testdata/cli.txt.
func TestCLISurface(t *testing.T) {
	var b strings.Builder
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		b.WriteString(commandLine(cmd) + "\n")
		// VisitAll visits the flags in the order of their names. --help is
		// one that cobra adds to each command.
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name != "help" {
				b.WriteString("    " + flagLine(f, cmd.PersistentFlags().Lookup(f.Name) != nil) + "\n")
			}
		})
		subs := cmd.Commands()
		slices.SortFunc(subs, func(a, b *cobra.Command) int { return strings.Compare(a.Name(), b.Name()) })
		for _, sub := range subs {
			walk(sub)
		}
	}
	walk(NewRootCommand())

	b.WriteString("\nenvironment variables:\n")
	for _, name := range slices.Sorted(slices.Values(envvar.All)) {
		b.WriteString("    " + name + "\n")
	}
	golden(t, "cli.txt", b.String())
}

// commandLine is the line of a command in cli.txt: its path and
// arguments, and what else its callers see of it.
func commandLine(cmd *cobra.Command) string {
	line := cmd.CommandPath()
	if _, args, ok := strings.Cut(cmd.Use, " "); ok {
		line += " " + args
	}
	if len(cmd.Aliases) > 0 {
		line += fmt.Sprintf("  (aliases: %s)", strings.Join(cmd.Aliases, ", "))
	}
	if cmd.Hidden {
		line += "  (hidden)"
	}
	if cmd.Deprecated != "" {
		line += "  (deprecated)"
	}
	return line
}

// flagLine is the line of a flag in cli.txt: its names, the type of its
// value, its default, and whether the subcommands have it too.
func flagLine(f *pflag.Flag, persistent bool) string {
	line := "--" + f.Name
	if f.Shorthand != "" {
		line = "-" + f.Shorthand + ", " + line
	}
	line += " " + f.Value.Type()
	if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "0s" && f.DefValue != "[]" {
		line += fmt.Sprintf(" (default %s)", f.DefValue)
	}
	if f.NoOptDefVal != "" && f.Value.Type() != "bool" && f.Value.Type() != "count" {
		line += fmt.Sprintf(" (without a value: %s)", f.NoOptDefVal)
	}
	if persistent {
		line += " (and subcommands)"
	}
	if f.Hidden {
		line += " (hidden)"
	}
	if f.Deprecated != "" {
		line += " (deprecated)"
	}
	return line
}

// Every get subcommand has a golden file of its JSON output, which a
// test of the command compares (goldenJSON), and each golden file is of a
// command that sind has: CLAUDE.md requires -o json on every get
// subcommand.
func TestJSONGoldens_CoverTheCommands(t *testing.T) {
	if *update {
		t.Skip("the tests write the golden files")
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "json"))
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		files = append(files, strings.TrimSuffix(e.Name(), ".json"))
	}

	root := NewRootCommand()
	get, _, err := root.Find([]string{"get"})
	require.NoError(t, err)
	// The commands with a JSON output: each one of get, and these.
	commands := []string{"doctor", "version"}
	for _, sub := range get.Commands() {
		require.NotNil(t, sub.Flag("output"), "sind get %s has --output", sub.Name())
		commands = append(commands, "get-"+sub.Name())
	}

	for _, command := range commands {
		assert.True(t, slices.ContainsFunc(files, func(file string) bool {
			return file == command || strings.HasPrefix(file, command+"_")
		}), "no golden file testdata/json/%s.json: compare the JSON output of the command with goldenJSON in a test", command)
	}
	for _, file := range files {
		command, _, _ := strings.Cut(file, "_")
		assert.Contains(t, commands, command, "testdata/json/%s.json is of no command with a JSON output", file)
	}
}
