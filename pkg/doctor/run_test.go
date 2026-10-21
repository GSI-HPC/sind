// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validCgroupMounts = "cgroup2 /sys/fs/cgroup cgroup2 rw,nsdelegate 0 0\n"
	localDockerHost   = "unix:///var/run/docker.sock"
)

var localHostCheck = Check{Name: "Docker host", Status: StatusOK, Detail: "this machine (unix:///var/run/docker.sock)"}

// errMeshDisabled stubs out the DNS check: ResolvedActive returns false.
var errMeshDisabled = errors.New("mesh disabled in hermetic doctor test")

// dockerInfo returns the `docker info --format '{{json .}}'` output of a
// rootful cgroup v2 daemon with the given version.
func dockerInfo(version string) string {
	return `{"ServerVersion":"` + version + `","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]}`
}

// resolvedWithPolkit returns a mesh executor for which systemd-resolved is
// running and polkit grants the DNS actions when authorized is true, or
// refuses the first one otherwise.
func resolvedWithPolkit(authorized bool) *mock.Executor {
	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl is-active
	if !authorized {
		sys.AddResult("", "", assert.AnError) // pkcheck refuses
		return &sys
	}
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	return &sys
}

// hermeticDeps builds Deps without touching the host: the daemon runs on
// this machine (DOCKER_HOST is the local socket), /proc/mounts holds
// mounts, and the mesh executor is meshExec, or fails every call when nil.
func hermeticDeps(t *testing.T, dockerExec, meshExec *mock.Executor, mounts string) Deps {
	t.Helper()
	t.Setenv("DOCKER_HOST", localDockerHost)
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/proc/mounts", []byte(mounts), 0o644))
	if meshExec == nil {
		meshExec = &mock.Executor{OnCall: func(_ []string, _ string) mock.Result {
			return mock.Result{Err: errMeshDisabled}
		}}
	}
	client := docker.NewClient(dockerExec)
	mgr := mesh.NewManager(client, mesh.DefaultRealm)
	mgr.Exec = meshExec
	return Deps{Client: client, Fs: fs, Mesh: mgr}
}

func TestRun_AllPass(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("29.0.0"), "", nil)

	checks := Run(context.Background(), hermeticDeps(t, &m, resolvedWithPolkit(true), validCgroupMounts))
	assert.Equal(t, []Check{
		{Name: "Docker Engine", Status: StatusOK, Detail: "29.0.0 (>= 28.0)"},
		{Name: "Docker daemon", Status: StatusOK, Detail: "rootful, no userns-remap"},
		localHostCheck,
		{Name: "cgroupv2", Status: StatusOK, Detail: "nsdelegate enabled (/sys/fs/cgroup)"},
		{Name: "DNS policy", Status: StatusOK, Detail: "host resolution available"},
	}, checks)
}

func TestRun_DockerTooOld(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("27.5.0"), "", nil)

	checks := Run(context.Background(), hermeticDeps(t, &m, nil, validCgroupMounts))
	require.Len(t, checks, 4)
	assert.Equal(t, Check{Name: "Docker Engine", Status: StatusFailed, Detail: "27.5.0 (requires >= 28.0)"}, checks[0])
}

func TestRun_UnparseableVersion(t *testing.T) {
	var m mock.Executor
	m.AddResult(dockerInfo("bogus"), "", nil)

	checks := Run(context.Background(), hermeticDeps(t, &m, nil, validCgroupMounts))
	assert.Equal(t, StatusFailed, checks[0].Status)
	assert.Contains(t, checks[0].Detail, "unable to parse")
}

func TestRun_DockerNotReachable(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n", testutil.ExitCode1(t))

	checks := Run(context.Background(), hermeticDeps(t, &m, nil, "tmpfs /tmp tmpfs rw 0 0\n"))
	assert.Equal(t, []Check{
		{Name: "Docker Engine", Status: StatusFailed,
			Detail:      "not reachable: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			Remediation: "Start the Docker daemon:\n\nsudo systemctl start docker"},
		{Name: "cgroupv2", Status: StatusFailed, Detail: "not mounted (sind requires cgroupv2)", Remediation: unifiedRemediation},
	}, checks)
}

func TestRun_CgroupMounts(t *testing.T) {
	tests := []struct {
		name   string
		mounts string
		want   Check
	}{
		{"missing", "tmpfs /tmp tmpfs rw 0 0\n",
			Check{Name: "cgroupv2", Status: StatusFailed, Detail: "not mounted (sind requires cgroupv2)", Remediation: unifiedRemediation}},
		{"no nsdelegate", "cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n",
			Check{Name: "cgroupv2", Status: StatusFailed, Detail: "nsdelegate not found", Remediation: NsdelegateRemediation("/sys/fs/cgroup")}},
		// A systemd host in hybrid mode fails although its cgroup2 mount
		// has nsdelegate: Docker runs containers on cgroup v1 there.
		{"hybrid", "tmpfs /sys/fs/cgroup tmpfs ro,mode=755 0 0\n" +
			"cgroup2 /sys/fs/cgroup/unified cgroup2 rw,nsdelegate 0 0\n" +
			"cgroup /sys/fs/cgroup/memory cgroup rw,memory 0 0\n",
			Check{Name: "cgroupv2", Status: StatusFailed,
				Detail:      "hybrid hierarchy: cgroup2 is mounted at /sys/fs/cgroup/unified, not /sys/fs/cgroup (sind requires cgroupv2)",
				Remediation: unifiedRemediation}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(dockerInfo("29.0.0"), "", nil)
			checks := Run(context.Background(), hermeticDeps(t, &m, nil, tt.mounts))
			require.Len(t, checks, 4)
			assert.Equal(t, tt.want, checks[3])
		})
	}
}

func TestRun_DNSPolicy(t *testing.T) {
	t.Run("authorized", func(t *testing.T) {
		var m mock.Executor
		m.AddResult(dockerInfo("29.0.0"), "", nil)
		checks := Run(context.Background(), hermeticDeps(t, &m, resolvedWithPolkit(true), validCgroupMounts))
		assert.Equal(t, Check{Name: "DNS policy", Status: StatusOK, Detail: "host resolution available"}, checks[4])
	})
	t.Run("not authorized", func(t *testing.T) {
		var m mock.Executor
		m.AddResult(dockerInfo("29.0.0"), "", nil)
		checks := Run(context.Background(), hermeticDeps(t, &m, resolvedWithPolkit(false), validCgroupMounts))
		assert.Equal(t, Check{Name: "DNS policy", Status: StatusWarning, Detail: "not authorized (optional)",
			Remediation: dnsPolicyRemediation}, checks[4])
		assert.Contains(t, checks[4].Remediation, "subject.active && subject.local) {")
		assert.True(t, strings.HasSuffix(checks[4].Remediation, "RULES"))
	})
	// Doctor does not promise host DNS for a daemon on another host, whose
	// mesh bridge this host's resolver cannot use.
	t.Run("remote daemon", func(t *testing.T) {
		var m mock.Executor
		m.AddResult(dockerInfo("29.0.0"), "", nil)
		m.AddResult("", "Error: No such image: x\n", testutil.ExitCode1(t))
		sys := &mock.Executor{}
		sys.AddResult("", "", nil) // systemctl is-active
		deps := hermeticDeps(t, &m, sys, validCgroupMounts)
		t.Setenv("DOCKER_HOST", "ssh://build-host")
		checks := Run(context.Background(), deps)
		last := checks[len(checks)-1]
		assert.Equal(t, Check{Name: "DNS policy", Status: StatusWarning,
			Detail: "not available: the Docker daemon does not run on this machine (optional)"}, last)
		assert.Len(t, sys.Calls, 1, "polkit is not asked")
	})
}

// TestRun_DaemonMode checks that a daemon in rootless mode or with
// userns-remap, which refuse sind's writable cgroups, fails.
func TestRun_DaemonMode(t *testing.T) {
	tests := []struct {
		option      string
		detail      string
		remediation string
	}{
		{"name=rootless", "rootless mode (sind needs a rootful daemon)", rootlessRemediation},
		{"name=userns", "userns-remap enabled (sind needs a daemon without it)", usernsRemediation},
	}
	for _, tt := range tests {
		t.Run(tt.option, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(`{"ServerVersion":"29.0.0","CgroupVersion":"2","SecurityOptions":["name=seccomp,profile=builtin","`+tt.option+`"]}`, "", nil)
			checks := Run(context.Background(), hermeticDeps(t, &m, nil, validCgroupMounts))
			require.Len(t, checks, 4)
			assert.Equal(t, Check{Name: "Docker daemon", Status: StatusFailed, Detail: tt.detail, Remediation: tt.remediation}, checks[1])
		})
	}
}

// TestRun_DaemonCgroupV1 checks that the cgroup version comes from the
// daemon: a local mount that passes does not make up for a daemon that runs
// containers on cgroup v1.
func TestRun_DaemonCgroupV1(t *testing.T) {
	var m mock.Executor
	m.AddResult(`{"ServerVersion":"29.0.0","CgroupVersion":"1","SecurityOptions":["name=seccomp,profile=builtin"]}`, "", nil)

	checks := Run(context.Background(), hermeticDeps(t, &m, nil, validCgroupMounts))
	require.Len(t, checks, 4)
	assert.Equal(t, Check{Name: "cgroupv2", Status: StatusFailed,
		Detail: "Docker runs containers on cgroup v1 (sind requires cgroupv2)", Remediation: unifiedRemediation}, checks[3])
}

// TestRun_Inotify checks the advisory inotify check: a warning below the
// limit, left out for a remote daemon.
func TestRun_Inotify(t *testing.T) {
	run := func(t *testing.T, limit, dockerHost string) []Check {
		t.Helper()
		var m mock.Executor
		m.AddResult(dockerInfo("29.0.0"), "", nil)
		switch {
		case dockerHost == "":
			m.AddResult(defaultContext, "", nil) // docker context inspect
		case !strings.HasPrefix(dockerHost, "unix://"):
			m.AddResult("", "Error: No such image: x\n", testutil.ExitCode1(t))
		}
		deps := hermeticDeps(t, &m, nil, validCgroupMounts)
		t.Setenv("DOCKER_HOST", dockerHost)
		require.NoError(t, afero.WriteFile(deps.Fs, "/proc/sys/fs/inotify/max_user_instances", []byte(limit), 0o644))
		return Run(context.Background(), deps)
	}

	checks := run(t, "128\n", "")
	require.Len(t, checks, 5)
	assert.Equal(t, Check{Name: "inotify", Status: StatusWarning,
		Detail:      "max_user_instances 128 (clusters of 10 or more nodes need 1024; optional)",
		Remediation: inotifyRemediation}, checks[4])

	checks = run(t, "8192\n", "unix:///run/docker.sock")
	require.Len(t, checks, 5)
	assert.Equal(t, Check{Name: "inotify", Status: StatusOK, Detail: "max_user_instances 8192 (>= 1024)"}, checks[4])

	checks = run(t, "128\n", "tcp://build-host:2376")
	assert.Len(t, checks, 4)
}

// TestRun_DaemonElsewhere checks the checks for a daemon that does not run
// on this machine: a warning that says why, nsdelegate read in a container
// of the default node image when the daemon has it, and no check of this
// machine's inotify limit.
func TestRun_DaemonElsewhere(t *testing.T) {
	const (
		withNsd    = "cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime,nsdelegate 0 0\n"
		withoutNsd = "cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime 0 0\n"
	)
	noImage := mock.Result{Stderr: "Error: No such image: " + config.DefaultImage + "\n", Err: testutil.ExitCode1(t)}
	hasImage := mock.Result{Stdout: "{}\n"}
	elsewhere := func(why string) Check {
		return Check{Name: "Docker host", Status: StatusWarning, Detail: "not this machine: " + why + " (unsupported)",
			Remediation: elsewhereRemediation}
	}
	probed := Check{Name: "cgroupv2", Status: StatusOK, Detail: "nsdelegate enabled (/sys/fs/cgroup, in a container)"}
	tests := []struct {
		name       string
		dockerHost string
		info       string
		kernel     string        // this machine's kernel release, empty for unknown
		results    []mock.Result // after docker info
		wantHost   Check
		wantCgroup Check
	}{
		{
			name: "DOCKER_HOST", dockerHost: "tcp://build-host:2376", info: dockerInfo("29.0.0"),
			results:  []mock.Result{hasImage, {Stdout: withNsd}},
			wantHost: elsewhere("DOCKER_HOST is tcp://build-host:2376, not a unix socket"), wantCgroup: probed,
		},
		{
			name: "remote context without the node image", info: dockerInfo("29.0.0"),
			results:  []mock.Result{{Stdout: `{"Name":"build","Endpoints":{"docker":{"Host":"ssh://ci@build-host"}}}`}, noImage},
			wantHost: elsewhere(`docker context "build" connects to ssh://ci@build-host, not a unix socket`),
			wantCgroup: Check{Name: "cgroupv2", Status: StatusWarning,
				Detail:      "nsdelegate not checked: the Docker host has no " + config.DefaultImage + " to probe it with (sind create checks it)",
				Remediation: "Pull the node image, then run sind doctor again:\n\ndocker pull " + config.DefaultImage},
		},
		{
			name: "Docker Desktop without nsdelegate", dockerHost: localDockerHost,
			info:     `{"ServerVersion":"29.0.0","CgroupVersion":"2","OperatingSystem":"Docker Desktop","Name":"docker-desktop","KernelVersion":"6.10.14-linuxkit"}`,
			kernel:   "6.10.14-linuxkit",
			results:  []mock.Result{hasImage, {Stdout: withoutNsd}},
			wantHost: elsewhere("Docker Desktop runs the daemon in a VM"),
			wantCgroup: Check{Name: "cgroupv2", Status: StatusFailed, Detail: "nsdelegate not found",
				Remediation: NsdelegateRemediation("/sys/fs/cgroup")},
		},
		{
			name: "another kernel, probe fails", dockerHost: "unix:///tmp/forwarded.sock",
			info:   `{"ServerVersion":"29.0.0","CgroupVersion":"2","KernelVersion":"6.12.48+deb13-amd64"}`,
			kernel: "6.8.0-45-generic",
			results: []mock.Result{hasImage,
				{Stderr: "docker: Error response from daemon: failed to create task\n\nRun 'docker run --help'\n", Err: testutil.ExitCode1(t)}},
			wantHost:   elsewhere("the daemon runs kernel 6.12.48+deb13-amd64, this machine 6.8.0-45-generic"),
			wantCgroup: Check{Name: "cgroupv2", Status: StatusFailed, Detail: "nsdelegate not checked: docker: Error response from daemon: failed to create task"},
		},
		{
			name: "image inspect fails", dockerHost: "ssh://ci@build-host", info: dockerInfo("29.0.0"),
			results:    []mock.Result{{Stderr: "error during connect: EOF\n", Err: testutil.ExitCode1(t)}},
			wantHost:   elsewhere("DOCKER_HOST is ssh://ci@build-host, not a unix socket"),
			wantCgroup: Check{Name: "cgroupv2", Status: StatusFailed, Detail: "nsdelegate not checked: error during connect: EOF"},
		},
		{
			name: "context unknown", info: dockerInfo("29.0.0"),
			results:    []mock.Result{{Stderr: "context \"gone\": context not found\n", Err: testutil.ExitCode1(t)}, hasImage, {Stdout: withNsd}},
			wantHost:   Check{Name: "Docker host", Status: StatusWarning, Detail: `unknown: context "gone": context not found`},
			wantCgroup: probed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(tt.info, "", nil)
			for _, r := range tt.results {
				m.AddResult(r.Stdout, r.Stderr, r.Err)
			}
			deps := hermeticDeps(t, &m, nil, validCgroupMounts)
			t.Setenv("DOCKER_HOST", tt.dockerHost)
			require.NoError(t, afero.WriteFile(deps.Fs, "/proc/sys/fs/inotify/max_user_instances", []byte("128\n"), 0o644))
			if tt.kernel != "" {
				require.NoError(t, afero.WriteFile(deps.Fs, "/proc/sys/kernel/osrelease", []byte(tt.kernel+"\n"), 0o644))
			}

			checks := Run(context.Background(), deps)
			require.Len(t, checks, 4, "no inotify check")
			assert.Equal(t, tt.wantHost, checks[2])
			assert.Equal(t, tt.wantCgroup, checks[3])
			assert.Len(t, m.Calls, 1+len(tt.results))
			for _, c := range m.Calls {
				if c.Args[0] == "run" {
					assert.Equal(t, []string{"run", "--rm", "--network", "none", "--entrypoint", "cat", config.DefaultImage, "/proc/self/mounts"}, c.Args)
				}
			}
		})
	}
}

// TestRun_DaemonElsewhereNotReachable checks that Run does not read this
// machine's cgroup2 mount for a daemon elsewhere it cannot reach.
func TestRun_DaemonElsewhereNotReachable(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "error during connect: Get \"http://docker.example/v1.47/info\": dial tcp: lookup docker.example: no such host\n", testutil.ExitCode1(t))

	deps := hermeticDeps(t, &m, nil, validCgroupMounts)
	t.Setenv("DOCKER_HOST", "tcp://docker.example:2375")
	checks := Run(context.Background(), deps)
	require.Len(t, checks, 2)
	assert.Equal(t, Check{Name: "cgroupv2", Status: StatusWarning, Detail: "nsdelegate not checked: Docker is not reachable"}, checks[1])
}
