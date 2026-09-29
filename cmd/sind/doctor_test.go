// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
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
	assert.Contains(t, out.String(), "cgroupv2: not mounted")
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

// resolvedWithPolkit returns a mesh executor for which systemd-resolved is
// running and polkit grants the DNS actions when authorized is true, or
// refuses the first one otherwise.
func resolvedWithPolkit(authorized bool) *mock.Executor {
	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl is-active → resolved running
	if !authorized {
		sys.AddResult("", "", assert.AnError) // pkcheck refuses
		return &sys
	}
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	return &sys
}

// executeDoctor runs `sind doctor` with args in ctx and returns what it
// wrote to its output stream.
func executeDoctor(ctx context.Context, t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommand()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(append([]string{"doctor"}, args...))
	cmd.SetContext(ctx)
	err := cmd.Execute()
	return out.String(), err
}

// captureStdout runs fn with os.Stdout redirected to a file and returns what
// fn wrote to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	orig := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = orig }()
	fn()

	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	return string(data)
}

// TestDoctorCommand_WritesToStdout checks that the results go to stdout and
// only the error line to stderr: cobra's Print functions write to stderr,
// so `sind doctor 2>/dev/null` used to print nothing.
func TestDoctorCommand_WritesToStdout(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version
	var stderr bytes.Buffer
	var code int
	stdout := captureStdout(t, func() {
		code = run(hermeticDoctorCtx(t, &m, nil), []string{"doctor"}, &stderr)
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, "✓ Docker Engine: 29.0.0 (>= 28.0)\n✓ cgroupv2: nsdelegate enabled (/sys/fs/cgroup)\n", stdout)
	assert.Empty(t, stderr.String())

	m = mock.Executor{}
	m.AddResult("27.5.0", "", nil) // docker version
	stderr.Reset()
	stdout = captureStdout(t, func() {
		code = run(hermeticDoctorCtx(t, &m, nil), []string{"doctor"}, &stderr)
	})
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "✗ Docker Engine: 27.5.0 (requires >= 28.0)\n")
	assert.Contains(t, stderr.String(), "checks failed: docker")
	assert.NotContains(t, stderr.String(), "Docker Engine")
}

func TestDoctorCommand_RemediationBetweenBlankLines(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	ctx := hermeticDoctorCtxWithMounts(t, &m, resolvedWithPolkit(true), "cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n")
	out, err := executeDoctor(ctx, t)
	require.EqualError(t, err, "checks failed: cgroup-nsdelegate")
	assert.Equal(t, `✓ Docker Engine: 29.0.0 (>= 28.0)
✗ cgroupv2: nsdelegate not found

Enable nsdelegate temporarily:

sudo mount -o remount,nsdelegate /sys/fs/cgroup

Enable nsdelegate on boot (systemd):

sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d
echo -e '[Mount]\nOptions=nsdelegate' \
  | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf
sudo systemctl daemon-reload

✓ DNS policy: host resolution available
`, out)
}

func TestDoctorCommand_DNSPolicyNotAuthorized(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, resolvedWithPolkit(false)), t)
	require.NoError(t, err, "the DNS policy check is advisory")
	assert.Contains(t, out, "✗ DNS policy: not authorized (optional)\n\nInstall a polkit rule")
	assert.Contains(t, out, "subject.active && subject.local) {")
	assert.True(t, strings.HasSuffix(out, "RULES\n\n"), out)
}

func TestDoctorCommand_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult("29.0.0", "", nil) // docker version

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, resolvedWithPolkit(true)), t, "-o", "json")
	require.NoError(t, err)

	var checks []doctorCheck
	require.NoError(t, json.Unmarshal([]byte(out), &checks))
	assert.Equal(t, []doctorCheck{
		{Name: "Docker Engine", Status: checkOK, Detail: "29.0.0 (>= 28.0)"},
		{Name: "cgroupv2", Status: checkOK, Detail: "nsdelegate enabled (/sys/fs/cgroup)"},
		{Name: "DNS policy", Status: checkOK, Detail: "host resolution available"},
	}, checks)
	assert.NotContains(t, out, "remediation")
}

func TestDoctorCommand_JSONFailures(t *testing.T) {
	var m mock.Executor
	m.AddResult("27.5.0", "", nil) // docker version

	ctx := hermeticDoctorCtxWithMounts(t, &m, resolvedWithPolkit(false), "cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n")
	out, err := executeDoctor(ctx, t, "--output", "json")
	require.EqualError(t, err, "checks failed: docker, cgroup-nsdelegate")

	var checks []doctorCheck
	require.NoError(t, json.Unmarshal([]byte(out), &checks), "stdout holds only the JSON document")
	require.Len(t, checks, 3)
	assert.Equal(t, doctorCheck{Name: "Docker Engine", Status: checkFailed, Detail: "27.5.0 (requires >= 28.0)"}, checks[0])
	assert.Equal(t, "cgroupv2", checks[1].Name)
	assert.Equal(t, checkFailed, checks[1].Status)
	assert.Equal(t, "nsdelegate not found", checks[1].Detail)
	assert.Equal(t, nsdelegateRemediation("/sys/fs/cgroup"), checks[1].Remediation)
	assert.Contains(t, checks[1].Remediation, "sudo mount -o remount,nsdelegate /sys/fs/cgroup\n")
	assert.Equal(t, doctorCheck{Name: "DNS policy", Status: checkWarning, Detail: "not authorized (optional)",
		Remediation: dnsPolicyRemediation}, checks[2])
}

func TestDoctorCommand_JSONDockerNotReachable(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect to the Docker daemon", assert.AnError)

	ctx := hermeticDoctorCtxWithMounts(t, &m, nil, "tmpfs /tmp tmpfs rw 0 0\n")
	out, err := executeDoctor(ctx, t, "-o", "json")
	require.EqualError(t, err, "checks failed: docker, cgroup")

	var checks []doctorCheck
	require.NoError(t, json.Unmarshal([]byte(out), &checks))
	assert.Equal(t, []doctorCheck{
		{Name: "Docker Engine", Status: checkFailed, Detail: "not reachable"},
		{Name: "cgroupv2", Status: checkFailed, Detail: "not mounted (sind requires cgroupv2)"},
	}, checks)
}

func TestDoctorCommand_InvalidOutput(t *testing.T) {
	var m mock.Executor

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, nil), t, "-o", "yaml")
	require.EqualError(t, err, `invalid --output value "yaml": must be "human" or "json"`)
	assert.Empty(t, out)
	assert.Empty(t, m.Calls, "no check runs")
}
