// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultContext is the `docker context inspect --format '{{json .}}'`
// output of the default context without DOCKER_HOST.
const defaultContext = `{"Name":"default","Metadata":{},"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock","SkipTLSVerify":false}}}`

// kernelFs returns a file system whose kernel release is release.
func kernelFs(t *testing.T, release string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/proc/sys/kernel/osrelease", []byte(release+"\n"), 0o644))
	return fs
}

// TestLocateDaemonLifecycle asks a real daemon in integration mode, which
// runs on the machine the tests run on, as on the CI runners.
func TestLocateDaemonLifecycle(t *testing.T) {
	c, rec := testutil.NewClient(t)
	fs := afero.NewOsFs()
	if !rec.IsIntegration() {
		t.Setenv("DOCKER_HOST", "")
		rec.AddResult(`{"ServerVersion":"29.1.0","CgroupVersion":"2","OperatingSystem":"Ubuntu 24.04.3 LTS","KernelVersion":"6.8.0-1017-azure"}`, "", nil)
		rec.AddResult(defaultContext, "", nil)
		fs = kernelFs(t, "6.8.0-1017-azure")
	}

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	loc, err := LocateDaemon(t.Context(), c, fs, info)
	require.NoError(t, err)
	assert.True(t, loc.Local(), loc.Elsewhere)
	assert.True(t, loc.Endpoint.UnixSocket(), loc.Endpoint.Host)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestLocateDaemon(t *testing.T) {
	local := &docker.DaemonInfo{ServerVersion: "29.1.0", OperatingSystem: "Ubuntu 24.04.3 LTS", KernelVersion: "6.8.0-45-generic"}
	tests := []struct {
		name       string
		dockerHost string
		context    string // docker context inspect output, without DOCKER_HOST
		info       *docker.DaemonInfo
		fs         afero.Fs
		want       DaemonLocation
	}{
		{
			name: "default context", context: defaultContext, info: local, fs: kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///var/run/docker.sock", Context: "default"}},
		},
		{
			name: "DOCKER_HOST unix socket", dockerHost: "unix:///run/docker.sock", info: local, fs: kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///run/docker.sock"}},
		},
		{
			name: "DOCKER_HOST tcp", dockerHost: "tcp://build-host:2376", info: local, fs: kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "tcp://build-host:2376"},
				Elsewhere: "DOCKER_HOST is tcp://build-host:2376, not a unix socket"},
		},
		{
			name: "remote context", context: `{"Name":"build","Endpoints":{"docker":{"Host":"ssh://ci@build-host"}}}`, info: local,
			fs: kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "ssh://ci@build-host", Context: "build"},
				Elsewhere: `docker context "build" connects to ssh://ci@build-host, not a unix socket`},
		},
		{
			name: "remote and not reachable", dockerHost: "ssh://ci@build-host", fs: afero.NewMemMapFs(),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "ssh://ci@build-host"},
				Elsewhere: "DOCKER_HOST is ssh://ci@build-host, not a unix socket"},
		},
		{
			name:    "Docker Desktop",
			context: `{"Name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/me/.docker/desktop/docker.sock"}}}`,
			info:    &docker.DaemonInfo{OperatingSystem: "Docker Desktop", Name: "docker-desktop", KernelVersion: "6.10.14-linuxkit"},
			fs:      kernelFs(t, "6.10.14-linuxkit"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///home/me/.docker/desktop/docker.sock", Context: "desktop-linux"},
				Elsewhere: "Docker Desktop runs the daemon in a VM"},
		},
		{
			name: "another kernel", dockerHost: "unix:///tmp/forwarded.sock",
			info: &docker.DaemonInfo{OperatingSystem: "Debian GNU/Linux 13 (trixie)", KernelVersion: "6.12.48+deb13-amd64"},
			fs:   kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///tmp/forwarded.sock"},
				Elsewhere: "the daemon runs kernel 6.12.48+deb13-amd64, this machine 6.8.0-45-generic"},
		},
		{
			name: "kernel release unknown here", context: defaultContext, info: local, fs: afero.NewMemMapFs(),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///var/run/docker.sock", Context: "default"}},
		},
		{
			name: "kernel release unknown to docker", context: defaultContext, info: &docker.DaemonInfo{ServerVersion: "29.1.0"},
			fs:   kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///var/run/docker.sock", Context: "default"}},
		},
		{
			name: "not reachable", context: defaultContext, fs: kernelFs(t, "6.8.0-45-generic"),
			want: DaemonLocation{Endpoint: docker.Endpoint{Host: "unix:///var/run/docker.sock", Context: "default"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", tt.dockerHost)
			var m mock.Executor
			if tt.context != "" {
				m.AddResult(tt.context, "", nil)
			}

			loc, err := LocateDaemon(t.Context(), docker.NewClient(&m), tt.fs, tt.info)
			require.NoError(t, err)
			assert.Equal(t, tt.want, loc)
			assert.Equal(t, tt.want.Elsewhere == "", loc.Local())
			if tt.dockerHost != "" {
				assert.Empty(t, m.Calls, "DOCKER_HOST needs no docker context")
			}
		})
	}
}

func TestLocateDaemon_EndpointError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	var m mock.Executor
	m.AddResult("", "context \"gone\": context not found\n", fmt.Errorf("exit status 1"))

	_, err := LocateDaemon(t.Context(), docker.NewClient(&m), afero.NewMemMapFs(), nil)
	require.ErrorContains(t, err, "inspecting the docker context")
}

func TestKernelRelease(t *testing.T) {
	_, ok := KernelRelease(afero.NewMemMapFs())
	assert.False(t, ok, "missing file")

	release, ok := KernelRelease(kernelFs(t, "6.8.0-45-generic"))
	assert.True(t, ok)
	assert.Equal(t, "6.8.0-45-generic", release)

	_, ok = KernelRelease(kernelFs(t, " "))
	assert.False(t, ok, "empty")
}
