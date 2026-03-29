// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorCommand_AllPass(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("29.0.0", "", nil) // docker version

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})

	ctx := withClient(context.Background(), docker.NewClient(&m))
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Docker Engine")
	assert.Contains(t, output, "29.0.0")
}

func TestDoctorCommand_DockerTooOld(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("27.5.0", "", nil) // docker version

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})

	ctx := withClient(context.Background(), docker.NewClient(&m))
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.Error(t, err)

	output := out.String()
	assert.Contains(t, output, "27.5.0")
}

func TestDoctorCommand_DockerNotReachable(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		return docker.MockResult{Stderr: "Cannot connect to the Docker daemon", Err: assert.AnError}
	}

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})

	ctx := withClient(context.Background(), docker.NewClient(&m))
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.Error(t, err)

	output := out.String()
	assert.Contains(t, output, "Docker Engine")
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		major int
		minor int
	}{
		{"28.0.0", 28, 0},
		{"29.3.1", 29, 3},
		{"28.0.0-beta.1", 28, 0},
	}
	for _, tt := range tests {
		major, minor, err := parseVersion(tt.input)
		require.NoError(t, err, tt.input)
		assert.Equal(t, tt.major, major, tt.input)
		assert.Equal(t, tt.minor, minor, tt.input)
	}
}
