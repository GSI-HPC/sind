// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validCgroupMounts = "cgroup2 /sys/fs/cgroup cgroup2 rw,nsdelegate 0 0\n"

// errMeshDisabled stubs out DNS-advisory checks in doctor unit tests that do
// not care about the mesh path — ResolvedActive returns false and the branch
// is skipped.
var errMeshDisabled = errors.New("mesh disabled in hermetic doctor test")

// hermeticDoctorCtx builds a context for doctor unit tests that does not touch
// the real host: it injects an afero memfs with a fake /proc/mounts
// (cgroupv2+nsdelegate) and a mesh.Manager whose Exec is a caller-supplied
// mock, or, when meshExec is nil, an always-erroring stub that disables the
// DNS advisory branch entirely.
func hermeticDoctorCtx(t *testing.T, dockerExec, meshExec *mock.Executor) context.Context {
	return hermeticDoctorCtxWithMounts(t, dockerExec, meshExec, validCgroupMounts)
}

// hermeticDoctorCtxWithMounts is hermeticDoctorCtx with caller-chosen
// /proc/mounts content, for testing cgroup failure paths.
func hermeticDoctorCtxWithMounts(
	t *testing.T,
	dockerExec, meshExec *mock.Executor,
	mounts string,
) context.Context {
	t.Helper()

	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/proc/mounts", []byte(mounts), 0o644))

	if meshExec == nil {
		meshExec = &mock.Executor{
			OnCall: func(_ []string, _ string) mock.Result {
				return mock.Result{Err: errMeshDisabled}
			},
		}
	}
	client := docker.NewClient(dockerExec)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	mgr.Exec = meshExec

	ctx := withClient(context.Background(), client)
	ctx = withMeshMgr(ctx, mgr)
	return withFs(ctx, fs)
}

func TestDoctorCommand_AllPass(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})
	cmd.SetContext(hermeticDoctorCtx(t, &m, nil))

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
	cmd.SetContext(hermeticDoctorCtx(t, &m, nil))

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker")

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
	cmd.SetContext(hermeticDoctorCtx(t, &m, nil))

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker")

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
	cmd.SetContext(hermeticDoctorCtx(t, &m, nil))

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, out.String(), "unable to parse")
}

func TestDoctorCommand_CgroupMissing(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})
	cmd.SetContext(hermeticDoctorCtxWithMounts(t, &m, nil, "tmpfs /tmp tmpfs rw 0 0\n"))

	err := cmd.Execute()
	require.EqualError(t, err, "checks failed: cgroup")
	assert.Contains(t, out.String(), "v2 not mounted")
}

func TestDoctorCommand_CgroupNsdelegateMissing(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})
	cmd.SetContext(hermeticDoctorCtxWithMounts(t, &m, nil,
		"cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n"))

	err := cmd.Execute()
	require.EqualError(t, err, "checks failed: cgroup-nsdelegate")
	assert.Contains(t, out.String(), "nsdelegate not found")
}

func TestDoctorCommand_DNSPolicyShown(t *testing.T) {
	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl is-active → resolved running
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)

	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"doctor"})
	cmd.SetContext(hermeticDoctorCtx(t, &m, &sys))

	_ = cmd.Execute()
	assert.Contains(t, out.String(), "DNS policy")
}
