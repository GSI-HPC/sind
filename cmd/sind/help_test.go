// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelpTopicTexts(t *testing.T) {
	stdout, _, err := executeCommand("help", "realm")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Realm (word)")
	assert.Contains(t, stdout, "$ sind get realms")

	stdout, _, err = executeCommand("help", "slow-create")
	require.NoError(t, err)
	assert.Contains(t, stdout, "(how to)")

	stdout, _, err = executeCommand("help", "topics")
	require.NoError(t, err)
	assert.Contains(t, stdout, "How to:")
	assert.Contains(t, stdout, "Words:")
}

// A topic named like a command is left to the command, and what is neither
// still fails as before.
func TestHelpTopicCommandsAndUnknown(t *testing.T) {
	stdout, _, err := executeCommand("help", "power")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Control node power state")
	assert.NotContains(t, stdout, "action of sind ui")

	stdout, _, err = executeCommand("help", "get", "cluster")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Show cluster health status")

	_, _, err = executeCommand("help", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown help topic "bogus"`)

	_, _, err = executeCommand("help", "realm", "extra")
	require.Error(t, err)

	stdout, _, err = executeCommand("help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Available Commands:")
}

// TestHelpTopicsListsWhatHelpPrints checks that every topic that sind help
// topics lists prints its own text, and that the list leaves out exactly
// the topics named like a command, such as the action power, for which
// sind help prints the command's help.
func TestHelpTopicsListsWhatHelpPrints(t *testing.T) {
	index, _, err := executeCommand("help", "topics")
	require.NoError(t, err)
	h := help.Default()
	var listed []string
	for _, line := range strings.Split(index, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		id := strings.Fields(line)[0]
		listed = append(listed, id)
		want, ok := h.Text(id)
		require.True(t, ok, id)
		stdout, _, err := executeCommand("help", id)
		require.NoError(t, err, id)
		assert.Equal(t, want, stdout, id)
	}

	root := NewRootCommand()
	var ids []string
	for _, w := range h.Words() {
		ids = append(ids, w.ID)
	}
	for _, a := range h.Actions() {
		ids = append(ids, a.ID)
	}
	for _, howto := range h.Howtos() {
		ids = append(ids, howto.ID)
	}
	for _, id := range ids {
		found, rest, _ := root.Find([]string{id})
		command := found != root && len(rest) == 0
		assert.Equal(t, !command, slices.Contains(listed, id), "%s is listed unless it names a command", id)
	}
	assert.NotContains(t, listed, "power")
}

// shellWords splits a command line at spaces, honouring single quotes.
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, started := false, false
	for _, r := range s {
		switch {
		case r == '\'':
			quoted, started = !quoted, true
		case r == ' ' && !quoted:
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

func TestShellWords(t *testing.T) {
	assert.Equal(t, []string{"sind", "power", "reboot", "worker-[0-3].dev"}, shellWords("sind power reboot 'worker-[0-3].dev'"))
	assert.Equal(t, []string{"a", ""}, shellWords("a ''"))
}

// TestHelpCommandsExist checks that every command that the help texts name
// is a command of sind, with flags that it has.
func TestHelpCommandsExist(t *testing.T) {
	root := NewRootCommand()
	cmds := help.Default().Commands()
	require.NotEmpty(t, cmds)
	for _, line := range cmds {
		words := shellWords(line)
		require.Equal(t, "sind", words[0], line)
		args := words[1:]
		found, _, err := root.Find(args)
		require.NoError(t, err, line)
		require.NotEqual(t, root, found, line)
		assert.False(t, found.HasSubCommands(), "%s names a group, not a command", line)
		for _, a := range args {
			if a == "--" {
				break
			}
			if name, ok := strings.CutPrefix(a, "--"); ok {
				name, _, _ = strings.Cut(name, "=")
				assert.True(t, found.Flags().Lookup(name) != nil || found.InheritedFlags().Lookup(name) != nil,
					"%s: %s has no flag --%s", line, found.CommandPath(), name)
			}
		}
	}
}
