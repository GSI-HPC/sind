// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validCgroupMounts = "cgroup2 /sys/fs/cgroup cgroup2 rw,nsdelegate 0 0\n"

// defaultContext is the `docker context inspect --format '{{json .}}'`
// output of the default context without DOCKER_HOST.
const defaultContext = `{"Name":"default","Metadata":{},"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock","SkipTLSVerify":false}}}`

// localDockerHost is the DOCKER_HOST of the hermetic doctor tests, and
// localHostCheck the Docker host check it gives.
const localDockerHost = "unix:///var/run/docker.sock"

var localHostCheck = doctor.Check{Name: "Docker host", Status: doctor.StatusOK, Detail: "this machine (unix:///var/run/docker.sock)"}

// dockerInfo returns the `docker info --format '{{json .}}'` output of a
// rootful cgroup v2 daemon with the given version.
func dockerInfo(version string) string {
	return fmt.Sprintf(`{"ServerVersion":%q,"CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]}`, version)
}

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
	// The local socket as DOCKER_HOST: the daemon runs on this machine,
	// and doctor needs no `docker context inspect` to tell. A test that
	// needs another endpoint sets it after this.
	t.Setenv("DOCKER_HOST", localDockerHost)

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
	m.AddResult(dockerInfo("29.0.0"), "", nil) // docker info

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, nil), t)
	require.NoError(t, err)

	assert.Contains(t, out, "Docker Engine")
	assert.Contains(t, out, "29.0.0")
}

func TestDoctorCommand_DockerTooOld(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("27.5.0"), "", nil) // docker info

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, nil), t)
	require.ErrorContains(t, err, "Docker Engine")

	assert.Contains(t, out, "27.5.0")
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
	out, _, err := execute(ctx, nil, append([]string{"doctor"}, args...)...)
	return out, err
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
	m.AddResult(dockerInfo("29.0.0"), "", nil) // docker info
	var stderr bytes.Buffer
	var code int
	stdout := captureStdout(t, func() {
		code = run(hermeticDoctorCtx(t, &m, nil), []string{"doctor"}, &stderr)
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, "✓ Docker Engine: 29.0.0 (>= 28.0)\n✓ Docker daemon: rootful, no userns-remap\n"+
		"✓ Docker host: this machine (unix:///var/run/docker.sock)\n✓ cgroupv2: nsdelegate enabled (/sys/fs/cgroup)\n", stdout)
	assert.Empty(t, stderr.String())

	m = mock.Executor{}
	m.AddResult(dockerInfo("27.5.0"), "", nil) // docker info
	stderr.Reset()
	stdout = captureStdout(t, func() {
		code = run(hermeticDoctorCtx(t, &m, nil), []string{"doctor"}, &stderr)
	})
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "✗ Docker Engine: 27.5.0 (requires >= 28.0)\n")
	assert.Contains(t, stderr.String(), "checks failed: Docker Engine")
	assert.NotContains(t, stderr.String(), "27.5.0")
}

func TestDoctorCommand_RemediationBetweenBlankLines(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("29.0.0"), "", nil) // docker info

	ctx := hermeticDoctorCtxWithMounts(t, &m, resolvedWithPolkit(true), "cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n")
	out, err := executeDoctor(ctx, t)
	require.EqualError(t, err, "checks failed: cgroupv2")
	assert.Equal(t, `✓ Docker Engine: 29.0.0 (>= 28.0)
✓ Docker daemon: rootful, no userns-remap
✓ Docker host: this machine (unix:///var/run/docker.sock)
✗ cgroupv2: nsdelegate not found

Enable nsdelegate on the Docker host temporarily:

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
	m.AddResult(dockerInfo("29.0.0"), "", nil) // docker info

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, resolvedWithPolkit(false)), t)
	require.NoError(t, err, "the DNS policy check is advisory")
	assert.Contains(t, out, "! DNS policy: not authorized (optional)\n\nInstall a polkit rule")
	assert.Contains(t, out, "subject.active && subject.local) {")
	assert.True(t, strings.HasSuffix(out, "RULES\n\n"), out)
}

func TestDoctorCommand_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("29.0.0"), "", nil) // docker info

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, resolvedWithPolkit(true)), t, "-o", "json")
	require.NoError(t, err)

	var checks []doctor.Check
	require.NoError(t, json.Unmarshal([]byte(out), &checks))
	assert.Equal(t, []doctor.Check{
		{Name: "Docker Engine", Status: doctor.StatusOK, Detail: "29.0.0 (>= 28.0)"},
		{Name: "Docker daemon", Status: doctor.StatusOK, Detail: "rootful, no userns-remap"},
		localHostCheck,
		{Name: "cgroupv2", Status: doctor.StatusOK, Detail: "nsdelegate enabled (/sys/fs/cgroup)"},
		{Name: "DNS policy", Status: doctor.StatusOK, Detail: "host resolution available"},
	}, checks)
	assert.NotContains(t, out, "remediation")
}

func TestDoctorCommand_JSONFailures(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("27.5.0"), "", nil) // docker info

	ctx := hermeticDoctorCtxWithMounts(t, &m, resolvedWithPolkit(false), "cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n")
	out, err := executeDoctor(ctx, t, "--output", "json")
	require.EqualError(t, err, "checks failed: Docker Engine, cgroupv2")

	var checks []doctor.Check
	require.NoError(t, json.Unmarshal([]byte(out), &checks), "stdout holds only the JSON document")
	require.Len(t, checks, 5)
	assert.Equal(t, doctor.Check{Name: "Docker Engine", Status: doctor.StatusFailed, Detail: "27.5.0 (requires >= 28.0)"}, checks[0])
	assert.Equal(t, doctor.Check{Name: "Docker daemon", Status: doctor.StatusOK, Detail: "rootful, no userns-remap"}, checks[1])
	assert.Equal(t, localHostCheck, checks[2])
	assert.Equal(t, "cgroupv2", checks[3].Name)
	assert.Equal(t, doctor.StatusFailed, checks[3].Status)
	assert.Equal(t, "nsdelegate not found", checks[3].Detail)
	assert.Equal(t, doctor.NsdelegateRemediation("/sys/fs/cgroup"), checks[3].Remediation)
	assert.Contains(t, checks[3].Remediation, "sudo mount -o remount,nsdelegate /sys/fs/cgroup\n")
	assert.Equal(t, doctor.Check{Name: "DNS policy", Status: doctor.StatusWarning, Detail: "not authorized (optional)",
		Remediation: checks[4].Remediation}, checks[4])
	assert.Contains(t, checks[4].Remediation, "polkit.addRule")
}

// TestDoctorCommand_DockerPermissionDenied checks that doctor says why it
// cannot reach Docker, escaped, and how to fix it.
func TestDoctorCommand_DockerPermissionDenied(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "permission denied while trying to connect to the Docker daemon socket\x1b]0;x\x07\n", testutil.ExitCode1(t))

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, nil), t)
	require.EqualError(t, err, "checks failed: Docker Engine")
	assert.Contains(t, out, "✗ Docker Engine: not reachable: permission denied while trying to connect to the Docker daemon socket\\x1b]0;x\\x07\n"+
		"\nAdd your user to the docker group, then log in again (or run newgrp docker):\n\nsudo usermod -aG docker $USER\n\n")
}

func TestDoctorCommand_InvalidOutput(t *testing.T) {
	var m mock.Executor

	out, err := executeDoctor(hermeticDoctorCtx(t, &m, nil), t, "-o", "yaml")
	require.EqualError(t, err, `invalid --output value "yaml": want human or json`)
	assert.Empty(t, out)
	assert.Empty(t, m.Calls, "no check runs")
}
