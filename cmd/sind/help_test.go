// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
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
