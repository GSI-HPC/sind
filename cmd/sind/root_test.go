// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeCommand(args ...string) (string, string, error) {
	cmd := NewRootCommand()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func executeWithMock(mock *cmdexec.MockExecutor, args ...string) (string, string, error) {
	cmd := NewRootCommand()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)

	client := docker.NewClient(mock)
	ctx := withClient(context.Background(), client)
	cmd.SetContext(ctx)

	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestRootCommand_Version(t *testing.T) {
	stdout, _, err := executeCommand("--version")
	require.NoError(t, err)
	assert.Contains(t, stdout, "sind version dev")
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
