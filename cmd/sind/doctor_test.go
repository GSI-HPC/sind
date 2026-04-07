// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorCommand_AllPass(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
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
	var m mock.Executor
	m.OnCall = func(_ []string, _ string) mock.Result {
		return mock.Result{Stderr: "Cannot connect to the Docker daemon", Err: assert.AnError}
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

func TestDoctorCommand_UnparseableVersion(t *testing.T) {
	var m mock.Executor
	m.AddResult("bogus", "", nil)

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})

	ctx := withClient(context.Background(), docker.NewClient(&m))
	cmd.SetContext(ctx)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, out.String(), "unable to parse")
}

func TestDoctorCommand_DNSPolicyShown(t *testing.T) {
	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl is-active → resolved running
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)

	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	mgr := mesh.NewManager(docker.NewClient(&m), mesh.DefaultRealm)
	mgr.Exec = &sys

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})

	ctx := withClient(context.Background(), docker.NewClient(&m))
	ctx = context.WithValue(ctx, meshMgrKey, mgr)
	cmd.SetContext(ctx)

	_ = cmd.Execute()
	assert.Contains(t, out.String(), "DNS policy")
}
