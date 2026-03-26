// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSH_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"ssh"})
	require.NoError(t, err)
	assert.Contains(t, c.Use, "ssh")
}

func TestEnter_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"enter"})
	require.NoError(t, err)
	assert.Equal(t, "enter [CLUSTER]", c.Use)
}

func TestEnter_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("enter", "a", "b")
	assert.Error(t, err)
}

func TestExec_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"exec"})
	require.NoError(t, err)
	assert.Contains(t, c.Use, "exec")
}

func TestParseSSHArgs_NodeOnly(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"worker-0"})
	require.NoError(t, err)
	assert.Empty(t, opts)
	assert.Equal(t, "worker-0", node)
	assert.Nil(t, cmd)
}

func TestParseSSHArgs_WithOptions(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"-v", "-L", "8080:localhost:80", "controller"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-v", "-L", "8080:localhost:80"}, opts)
	assert.Equal(t, "controller", node)
	assert.Nil(t, cmd)
}

func TestParseSSHArgs_WithCommand(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"worker-0", "--", "hostname"})
	require.NoError(t, err)
	assert.Empty(t, opts)
	assert.Equal(t, "worker-0", node)
	assert.Equal(t, []string{"hostname"}, cmd)
}

func TestParseSSHArgs_Full(t *testing.T) {
	opts, node, cmd, err := parseSSHArgs([]string{"-t", "worker-0.dev", "--", "top", "-b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"-t"}, opts)
	assert.Equal(t, "worker-0.dev", node)
	assert.Equal(t, []string{"top", "-b"}, cmd)
}

func TestParseSSHArgs_Empty(t *testing.T) {
	_, _, _, err := parseSSHArgs(nil)
	assert.Error(t, err)
}

func TestParseSSHArgs_OnlyDash(t *testing.T) {
	_, _, _, err := parseSSHArgs([]string{"--"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node argument required")
}

func TestParseExecArgs_Simple(t *testing.T) {
	cluster, cmd, err := parseExecArgs([]string{"--", "hostname"})
	require.NoError(t, err)
	assert.Equal(t, "default", cluster)
	assert.Equal(t, []string{"hostname"}, cmd)
}

func TestParseExecArgs_WithCluster(t *testing.T) {
	cluster, cmd, err := parseExecArgs([]string{"dev", "--", "squeue"})
	require.NoError(t, err)
	assert.Equal(t, "dev", cluster)
	assert.Equal(t, []string{"squeue"}, cmd)
}

func TestParseExecArgs_MissingSeparator(t *testing.T) {
	_, _, err := parseExecArgs([]string{"hostname"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "-- separator")
}

func TestParseExecArgs_MissingCommand(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "command required")
}

func TestParseExecArgs_ExtraArgsBefore(t *testing.T) {
	_, _, err := parseExecArgs([]string{"a", "b", "--", "cmd"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at most one argument before --")
}
