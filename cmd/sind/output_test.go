// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsJSONOutput(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"get", "clusters", "--output", "json"})

	get, _, err := cmd.Find([]string{"get", "clusters"})
	require.NoError(t, err)

	// Before parsing, flag is not set.
	assert.False(t, isJSONOutput(get))
}

func TestIsJSONOutput_NoFlag(t *testing.T) {
	t.Setenv("SIND_OUTPUT", "json")
	cmd := NewRootCommand()
	// version command has no --output flag
	ver, _, err := cmd.Find([]string{"version"})
	require.NoError(t, err)
	assert.False(t, isJSONOutput(ver))
}

func TestGetOutput_DefaultIsHuman(t *testing.T) {
	cmd := NewRootCommand()
	get, _, err := cmd.Find([]string{"get", "clusters"})
	require.NoError(t, err)
	assert.Equal(t, "human", outputFlag(get))
	assert.False(t, isJSONOutput(get))
}

func TestGetOutput_RejectsUnknownValue(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"get", "clusters", "--output", "yaml"})
	err := cmd.Execute()
	require.ErrorContains(t, err, `invalid --output value "yaml"`)
}

// TestGetOutput_FromEnv covers SIND_OUTPUT: the output format of a get
// without --output, which wins over it.
func TestGetOutput_FromEnv(t *testing.T) {
	t.Setenv("SIND_OUTPUT", "json")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"get", "realms"}, "[]\n"},
		{[]string{"get", "realms", "-o", "human"}, "NAME"},
	} {
		var m mock.Executor
		m.AddResult("", "", nil) // a realm without containers
		stdout, _, err := executeWithMock(&m, tc.args...)
		require.NoError(t, err)
		assert.Contains(t, stdout, tc.want, "%v", tc.args)
	}
}

// TestGetOutput_InvalidEnv checks that an invalid SIND_OUTPUT fails a get
// with exit status 1, before it acts, and only when -o does not win over it.
func TestGetOutput_InvalidEnv(t *testing.T) {
	t.Setenv("SIND_OUTPUT", "yaml")
	var m mock.Executor
	_, _, err := executeWithMock(&m, "get", "realms")
	require.EqualError(t, err, `invalid SIND_OUTPUT value "yaml": want human or json`)
	assert.False(t, cobratree.IsUsage(err))
	assert.Empty(t, m.Calls)

	m.AddResult("", "", nil)
	stdout, _, err := executeWithMock(&m, "get", "realms", "-o", "json")
	require.NoError(t, err)
	assert.Equal(t, "[]\n", stdout)
}
