// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		major, minor, err := ParseVersion(tt.input)
		require.NoError(t, err, tt.input)
		assert.Equal(t, tt.major, major, tt.input)
		assert.Equal(t, tt.minor, minor, tt.input)
	}
}

func TestParseVersion_Invalid(t *testing.T) {
	_, _, err := ParseVersion("bogus")
	assert.Error(t, err)

	_, _, err = ParseVersion("28")
	assert.Error(t, err)

	_, _, err = ParseVersion("x.0.0")
	assert.Error(t, err)

	_, _, err = ParseVersion("28.y.0")
	assert.Error(t, err)
}

func TestCheckDockerVersion(t *testing.T) {
	assert.NoError(t, CheckDockerVersion("28.0.0"))
	assert.NoError(t, CheckDockerVersion("29.3.1"))
	assert.NoError(t, CheckDockerVersion("28.0.0-beta.1"))
}

func TestCheckDockerVersion_TooOld(t *testing.T) {
	err := CheckDockerVersion("27.5.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires >= 28.0")
}

func TestCheckDockerVersion_Invalid(t *testing.T) {
	err := CheckDockerVersion("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse version")
}

func TestCgroupInfo_MemFs(t *testing.T) {
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/proc/mounts",
		[]byte("cgroup2 /sys/fs/cgroup cgroup2 rw,nsdelegate 0 0\n"), 0o644))

	mountPath, hasV2, hasNsd := CgroupInfo(fs)
	assert.Equal(t, "/sys/fs/cgroup", mountPath)
	assert.True(t, hasV2)
	assert.True(t, hasNsd)
}

func TestCgroupInfo_ReadError(t *testing.T) {
	path, hasV2, hasNsd := CgroupInfo(afero.NewMemMapFs())
	assert.Empty(t, path)
	assert.False(t, hasV2)
	assert.False(t, hasNsd)
}

func TestParseCgroupInfo_WithNsdelegate(t *testing.T) {
	mounts := "cgroup2 /sys/fs/cgroup cgroup2 rw,nosuid,nodev,noexec,relatime,nsdelegate,memory_recursiveprot 0 0\n"
	path, hasV2, hasNsd := parseCgroupInfo(mounts)
	assert.Equal(t, "/sys/fs/cgroup", path)
	assert.True(t, hasV2)
	assert.True(t, hasNsd)
}

func TestParseCgroupInfo_WithoutNsdelegate(t *testing.T) {
	mounts := "cgroup2 /sys/fs/cgroup cgroup2 rw,nosuid,nodev,noexec,relatime 0 0\n"
	path, hasV2, hasNsd := parseCgroupInfo(mounts)
	assert.Equal(t, "/sys/fs/cgroup", path)
	assert.True(t, hasV2)
	assert.False(t, hasNsd)
}

func TestParseCgroupInfo_NoCgroup2(t *testing.T) {
	mounts := "tmpfs /tmp tmpfs rw,nosuid,nodev 0 0\n"
	path, hasV2, hasNsd := parseCgroupInfo(mounts)
	assert.Empty(t, path)
	assert.False(t, hasV2)
	assert.False(t, hasNsd)
}

// TestParseCgroupInfo_Hybrid checks that a systemd host in hybrid mode,
// whose cgroup2 mount at /sys/fs/cgroup/unified has nsdelegate, does not
// pass: Docker runs containers on cgroup v1 there.
func TestParseCgroupInfo_Hybrid(t *testing.T) {
	mounts := "tmpfs /sys/fs/cgroup tmpfs ro,nosuid,nodev,noexec,mode=755 0 0\n" +
		"cgroup2 /sys/fs/cgroup/unified cgroup2 rw,nosuid,nodev,noexec,relatime,nsdelegate 0 0\n" +
		"cgroup /sys/fs/cgroup/systemd cgroup rw,nosuid,nodev,noexec,relatime,xattr,name=systemd 0 0\n" +
		"cgroup /sys/fs/cgroup/memory cgroup rw,nosuid,nodev,noexec,relatime,memory 0 0\n"
	path, hasV2, hasNsd := parseCgroupInfo(mounts)
	assert.Equal(t, "/sys/fs/cgroup/unified", path)
	assert.False(t, hasV2)
	assert.False(t, hasNsd)
}

// TestParseCgroupInfo_RootAfterOther checks that the mount at
// /sys/fs/cgroup counts wherever it is in the table, and that an option
// that only contains "nsdelegate" is not taken for it.
func TestParseCgroupInfo_RootAfterOther(t *testing.T) {
	mounts := "cgroup2 /run/other cgroup2 rw,nsdelegate 0 0\n" +
		"cgroup2 /sys/fs/cgroup cgroup2 rw,nonsdelegate 0 0\n"
	path, hasV2, hasNsd := parseCgroupInfo(mounts)
	assert.Equal(t, "/sys/fs/cgroup", path)
	assert.True(t, hasV2)
	assert.False(t, hasNsd)
}

func TestParseCgroupInfo_Empty(t *testing.T) {
	path, hasV2, hasNsd := parseCgroupInfo("")
	assert.Empty(t, path)
	assert.False(t, hasV2)
	assert.False(t, hasNsd)
}

func TestInotifyInstances(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, ok := InotifyInstances(fs)
	assert.False(t, ok, "missing file")

	require.NoError(t, afero.WriteFile(fs, "/proc/sys/fs/inotify/max_user_instances", []byte("128\n"), 0o644))
	n, ok := InotifyInstances(fs)
	assert.True(t, ok)
	assert.Equal(t, 128, n)

	require.NoError(t, afero.WriteFile(fs, "/proc/sys/fs/inotify/max_user_instances", []byte("lots\n"), 0o644))
	_, ok = InotifyInstances(fs)
	assert.False(t, ok, "not a number")
}

// exitError returns the error of a docker command that exited 1 after
// writing stderr.
func exitError(t *testing.T, stderr string) error {
	t.Helper()
	var exitErr *exec.ExitError
	require.ErrorAs(t, exec.Command("sh", "-c", "exit 1").Run(), &exitErr)
	return &cmdexec.ExitError{Err: exitErr, Stderr: stderr}
}

func TestDockerUnreachable(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		detail      string
		remediation string
	}{
		{"no docker CLI", &exec.Error{Name: "docker", Err: exec.ErrNotFound},
			"not reachable: docker CLI not found in PATH", dockerInstallRemediation},
		{"permission denied", exitError(t, "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: connect: permission denied\n"),
			"not reachable: permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: connect: permission denied",
			dockerPermissionRemediation},
		{"daemon down", errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"),
			"not reachable: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			dockerStartRemediation},
		{"first line only", exitError(t, "\n  error during connect: Get \"http://x/v1.47/info\": EOF\nmore\n"),
			`not reachable: error during connect: Get "http://x/v1.47/info": EOF`, ""},
		{"no stderr", exitError(t, " \n"), "not reachable: exit status 1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail, remediation := DockerUnreachable(tt.err)
			assert.Equal(t, tt.detail, detail)
			assert.Equal(t, tt.remediation, remediation)
		})
	}
}

// probeImage is the image the probe tests run: the image under test in CI.
func probeImage() string {
	if image := os.Getenv("SIND_TEST_IMAGE"); image != "" {
		return image
	}
	return "ghcr.io/gsi-hpc/sind-node:latest"
}

// TestProbeCgroupInfoLifecycle probes a real daemon in integration mode,
// which runs on the machine the tests run on: the container shows the
// nsdelegate option of this machine's own cgroup2 mount, which the CI
// runners mount with it.
func TestProbeCgroupInfoLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	wantNsd := true
	if rec.IsIntegration() {
		_, _, wantNsd = CgroupInfo(afero.NewOsFs())
	} else {
		rec.AddResult("overlay / overlay rw,relatime,lowerdir=/l,upperdir=/u,workdir=/w 0 0\n"+
			"proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0\n"+
			"cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime,nsdelegate,memory_recursiveprot 0 0\n", "", nil)
	}

	mountPath, hasV2, hasNsd, err := ProbeCgroupInfo(t.Context(), c, probeImage())
	require.NoError(t, err)
	assert.Equal(t, CgroupRoot, mountPath)
	assert.True(t, hasV2)
	assert.Equal(t, wantNsd, hasNsd, "the probe sees the nsdelegate of this machine's kernel")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestProbeCgroupInfo(t *testing.T) {
	var m mock.Executor
	m.AddResult("cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime 0 0\n", "", nil)

	mountPath, hasV2, hasNsd, err := ProbeCgroupInfo(t.Context(), docker.NewClient(&m), "img:1")
	require.NoError(t, err)
	assert.Equal(t, CgroupRoot, mountPath)
	assert.True(t, hasV2)
	assert.False(t, hasNsd)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", "--network", "none", "--entrypoint", "cat", "img:1", "/proc/self/mounts"}, m.Calls[0].Args)
}

func TestProbeCgroupInfo_RunError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "docker: Error response from daemon: pull access denied for img\n", fmt.Errorf("exit status 125"))

	_, _, _, err := ProbeCgroupInfo(t.Context(), docker.NewClient(&m), "img:1")
	require.ErrorContains(t, err, "reading the mount table in a container of img:1: exit status 125")
}

func TestNsdelegateRemediation(t *testing.T) {
	assert.Equal(t, `Enable nsdelegate on the Docker host temporarily:

sudo mount -o remount,nsdelegate /sys/fs/cgroup

Enable nsdelegate on boot (systemd):

sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d
echo -e '[Mount]\nOptions=nsdelegate' \
  | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf
sudo systemctl daemon-reload`, NsdelegateRemediation(CgroupRoot))
}
