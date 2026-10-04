// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
)

// DaemonLocation is where the Docker daemon runs, as far as the machine
// that runs the docker CLI can tell.
type DaemonLocation struct {
	// Endpoint is the address at which the docker CLI reaches the daemon.
	Endpoint docker.Endpoint
	// Elsewhere says why the daemon does not run on this machine, empty
	// when it does.
	Elsewhere string
}

// Local reports whether the daemon runs on this machine.
func (l DaemonLocation) Local() bool {
	return l.Elsewhere == ""
}

// kernelReleasePath is where the kernel exposes its release, as uname -r
// prints it.
const kernelReleasePath = "/proc/sys/kernel/osrelease"

// KernelRelease reads the release of the running kernel from fs. ok is
// false when it cannot be read.
func KernelRelease(fs afero.Fs) (release string, ok bool) {
	data, err := afero.ReadFile(fs, kernelReleasePath)
	if err != nil {
		return "", false
	}
	release = strings.TrimSpace(string(data))
	return release, release != ""
}

// LocateDaemon tells whether the Docker daemon runs on this machine, whose
// files fs holds. It does not when the docker CLI reaches it other than
// through a unix socket (see docker.Client.Endpoint), when info is Docker
// Desktop's, whose daemon runs in a VM, or when info names another kernel
// release than this machine runs. A container shares its host's kernel, so
// a CI job in a container that reaches the host's daemon through a mounted
// socket counts as local. info is nil when the daemon is not reachable;
// only the endpoint counts then.
//
// The answer is a heuristic: a unix socket can be forwarded from another
// host that runs the same kernel release, and a TCP endpoint can be this
// machine's own daemon.
func LocateDaemon(ctx context.Context, client *docker.Client, fs afero.Fs, info *docker.DaemonInfo) (DaemonLocation, error) {
	ep, err := client.Endpoint(ctx)
	if err != nil {
		return DaemonLocation{}, err
	}
	loc := DaemonLocation{Endpoint: ep}
	switch {
	case !ep.UnixSocket() && ep.Context == "":
		loc.Elsewhere = fmt.Sprintf("DOCKER_HOST is %s, not a unix socket", ep.Host)
	case !ep.UnixSocket():
		loc.Elsewhere = fmt.Sprintf("docker context %q connects to %s, not a unix socket", ep.Context, ep.Host)
	case info == nil:
	case info.DockerDesktop():
		loc.Elsewhere = "Docker Desktop runs the daemon in a VM"
	default:
		if release, ok := KernelRelease(fs); ok && info.KernelVersion != "" && info.KernelVersion != release {
			loc.Elsewhere = fmt.Sprintf("the daemon runs kernel %s, this machine %s", info.KernelVersion, release)
		}
	}
	return loc, nil
}
