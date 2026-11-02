// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"context"
	"fmt"

	clidoctor "github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
)

// Deps are the host handles Run checks through.
type Deps struct {
	// Client asks the Docker daemon.
	Client *docker.Client
	// Fs is the file system the checks that read this machine use
	// (/proc/mounts, /proc/sys, the Docker context files).
	Fs afero.Fs
	// Mesh answers the host DNS checks (systemd-resolved, polkit).
	Mesh *mesh.Manager
}

// Run executes the host prerequisite checks in the order `sind doctor`
// prints them and returns one Check per check that applies: the Docker
// Engine version, the daemon's mode, where it runs, cgroupv2, the inotify
// limit and host DNS resolution. A failed check is reported in the result,
// never as an error.
func Run(ctx context.Context, d Deps) []clidoctor.Check {
	client, fs, mgr := d.Client, d.Fs, d.Mesh
	var checks []clidoctor.Check

	// Check Docker Engine version.
	engine := clidoctor.Check{Name: "Docker Engine", Status: clidoctor.StatusFailed}
	info, err := client.Info(ctx)
	if err != nil {
		engine.Detail, engine.Remediation = DockerUnreachable(err)
	} else if vErr := CheckDockerVersion(info.ServerVersion); vErr != nil {
		engine.Detail = vErr.Error()
	} else {
		engine.Status = clidoctor.StatusOK
		engine.Detail = fmt.Sprintf("%s (>= %d.0)", info.ServerVersion, MinDockerMajor)
	}
	checks = append(checks, engine)

	// Check that the daemon can start sind's nodes: not rootless and
	// without userns-remap, which refuse writable cgroups.
	if info != nil {
		daemon := clidoctor.Check{Name: "Docker daemon", Status: clidoctor.StatusOK, Detail: "rootful, no userns-remap"}
		switch {
		case info.HasSecurityOption("rootless"):
			daemon = clidoctor.Check{Name: "Docker daemon", Status: clidoctor.StatusFailed,
				Detail: "rootless mode (sind needs a rootful daemon)", Remediation: rootlessRemediation}
		case info.HasSecurityOption("userns"):
			daemon = clidoctor.Check{Name: "Docker daemon", Status: clidoctor.StatusFailed,
				Detail: "userns-remap enabled (sind needs a daemon without it)", Remediation: usernsRemediation}
		}
		checks = append(checks, daemon)
	}

	// Where the daemon runs: the checks below that read this machine
	// describe the Docker host only when the daemon runs here.
	log := sindlog.From(ctx)
	loc, locErr := LocateDaemon(ctx, client, fs, info)
	local := locErr == nil && loc.Local()
	log.Log(ctx, sindlog.LevelTrace, "docker host", "endpoint", loc.Endpoint.Host, "context", loc.Endpoint.Context, "local", local)
	if info != nil {
		checks = append(checks, hostCheck(loc, locErr))
	}

	checks = append(checks, cgroupCheck(ctx, client, fs, info, local))

	// Advisory: enough inotify instances for large clusters. The limit is
	// the Docker host's, so the check is left out for a daemon elsewhere.
	if n, ok := InotifyInstances(fs); ok && local {
		inotify := clidoctor.Check{Name: "inotify", Status: clidoctor.StatusOK,
			Detail: fmt.Sprintf("max_user_instances %d (>= %d)", n, MinInotifyInstances)}
		if n < MinInotifyInstances {
			inotify = clidoctor.Check{Name: "inotify", Status: clidoctor.StatusWarning,
				Detail:      fmt.Sprintf("max_user_instances %d (clusters of %d or more nodes need %d; optional)", n, InotifyNodes, MinInotifyInstances),
				Remediation: inotifyRemediation}
		}
		checks = append(checks, inotify)
	}

	// Advisory: host DNS resolution via systemd-resolved. It points this
	// host's resolver at the mesh bridge, which a daemon elsewhere has on
	// its own host.
	if mgr.ResolvedActive(ctx) {
		switch {
		case !local:
			checks = append(checks, clidoctor.Check{Name: "DNS policy", Status: clidoctor.StatusWarning,
				Detail: "not available: the Docker daemon does not run on this machine (optional)"})
		case mgr.DNSPolkitAuthorized(ctx):
			checks = append(checks, clidoctor.Check{Name: "DNS policy", Status: clidoctor.StatusOK,
				Detail: "host resolution available"})
		default:
			checks = append(checks, clidoctor.Check{Name: "DNS policy", Status: clidoctor.StatusWarning,
				Detail: "not authorized (optional)", Remediation: dnsPolicyRemediation})
		}
	}
	return checks
}

// hostCheck reports where the Docker daemon runs (LocateDaemon). A
// daemon elsewhere is a warning: sind does not support it, but every
// required check asks the daemon or probes its kernel, so they still hold.
func hostCheck(loc DaemonLocation, err error) clidoctor.Check {
	switch {
	case err != nil:
		return clidoctor.Check{Name: "Docker host", Status: clidoctor.StatusWarning, Detail: "unknown: " + ErrorLine(err)}
	case !loc.Local():
		return clidoctor.Check{Name: "Docker host", Status: clidoctor.StatusWarning,
			Detail: "not this machine: " + loc.Elsewhere + " (unsupported)", Remediation: elsewhereRemediation}
	}
	return clidoctor.Check{Name: "Docker host", Status: clidoctor.StatusOK, Detail: "this machine (" + loc.Endpoint.Host + ")"}
}

// cgroupCheck checks that the Docker host runs the unified cgroup2
// hierarchy with nsdelegate. The cgroup version comes from the daemon (info).
// For a daemon on this machine the cgroup2 mount comes from /proc/mounts in
// fs, as the kernel is the same; for one elsewhere, from a throwaway
// container of the default node image (ProbeCgroupInfo), if the
// daemon has that image: doctor pulls none.
func cgroupCheck(ctx context.Context, client *docker.Client, fs afero.Fs, info *docker.DaemonInfo, local bool) clidoctor.Check {
	log := sindlog.From(ctx)
	if info != nil && info.CgroupVersion != "" && info.CgroupVersion != "2" {
		return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusFailed,
			Detail:      fmt.Sprintf("Docker runs containers on cgroup v%s (sind requires cgroupv2)", info.CgroupVersion),
			Remediation: unifiedRemediation}
	}
	var mountPath, where string
	var hasV2, hasNsd bool
	switch {
	case local:
		log.Log(ctx, sindlog.LevelTrace, "reading /proc/mounts for cgroup2 info")
		mountPath, hasV2, hasNsd = CgroupInfo(fs)
	case info == nil:
		return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusWarning, Detail: "nsdelegate not checked: Docker is not reachable"}
	default:
		image := config.DefaultImage
		_, present, err := client.ImageLabels(ctx, image)
		if err == nil && !present {
			return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusWarning,
				Detail:      fmt.Sprintf("nsdelegate not checked: the Docker host has no %s to probe it with (sind create checks it)", image),
				Remediation: "Pull the node image, then run sind doctor again:\n\ndocker pull " + image}
		}
		if err == nil {
			mountPath, hasV2, hasNsd, err = ProbeCgroupInfo(ctx, client, image)
		}
		if err != nil {
			return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusFailed, Detail: "nsdelegate not checked: " + ErrorLine(err)}
		}
		where = ", in a container"
	}
	log.Log(ctx, sindlog.LevelTrace, "cgroup2 check", "mountPath", mountPath, "v2", hasV2, "nsdelegate", hasNsd, "local", local)
	switch {
	case !hasV2 && mountPath != "":
		return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusFailed,
			Detail:      fmt.Sprintf("hybrid hierarchy: cgroup2 is mounted at %s, not %s (sind requires cgroupv2)", mountPath, CgroupRoot),
			Remediation: unifiedRemediation}
	case !hasV2:
		return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusFailed,
			Detail: "not mounted (sind requires cgroupv2)", Remediation: unifiedRemediation}
	case !hasNsd:
		return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusFailed,
			Detail: "nsdelegate not found", Remediation: NsdelegateRemediation(mountPath)}
	}
	return clidoctor.Check{Name: "cgroupv2", Status: clidoctor.StatusOK, Detail: fmt.Sprintf("nsdelegate enabled (%s%s)", mountPath, where)}
}

// unifiedRemediation is shown when the host does not run the unified
// cgroup2 hierarchy: cgroup v1, or systemd's hybrid mode.
const unifiedRemediation = `Boot with the unified cgroup hierarchy: add
systemd.unified_cgroup_hierarchy=1 to the kernel command line (for GRUB,
GRUB_CMDLINE_LINUX in /etc/default/grub), regenerate the boot loader
configuration and reboot.`

// inotifyRemediation is shown when fs.inotify.max_user_instances is below
// MinInotifyInstances.
const inotifyRemediation = `Raise the limit now:

sudo sysctl fs.inotify.max_user_instances=1024

Keep it across reboots:

echo fs.inotify.max_user_instances=1024 | sudo tee /etc/sysctl.d/99-sind.conf`

// elsewhereRemediation is shown when the Docker daemon does not run on
// this machine.
const elsewhereRemediation = `sind expects to run on the Docker host: --data paths, host DNS for the
*.sind names and the inotify check refer to this machine, the nodes to the
daemon's. Run sind on the Docker host, or point the docker CLI at a daemon
on this machine:

unset DOCKER_HOST DOCKER_CONTEXT
docker context use default`

// rootlessRemediation is shown for a Docker daemon in rootless mode.
const rootlessRemediation = `sind starts its nodes with --security-opt writable-cgroups=true, which
Docker refuses in rootless mode. Point the docker CLI at a rootful daemon,
such as the system one:

unset DOCKER_HOST
docker context use default`

// usernsRemediation is shown for a Docker daemon with userns-remap.
const usernsRemediation = `sind starts its nodes with --security-opt writable-cgroups=true, which
Docker refuses with userns-remap. Remove "userns-remap" from
/etc/docker/daemon.json and restart the daemon:

sudo systemctl restart docker`

// dnsPolicyRemediation is shown when polkit does not allow docker group
// members to configure host DNS resolution through systemd-resolved.
const dnsPolicyRemediation = `Install a polkit rule to enable host DNS resolution for *.sind.
Choose the profile that matches your environment:

Desktop — allows docker group members to configure DNS from local
sessions only (direct keyboard/display access, not SSH):

sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules <<'RULES'
polkit.addRule(function(action, subject) {
    if (["org.freedesktop.resolve1.set-dns-servers",
         "org.freedesktop.resolve1.set-domains",
         "org.freedesktop.resolve1.revert"].indexOf(action.id) >= 0 &&
        subject.isInGroup("docker") &&
        subject.active && subject.local) {
        return polkit.Result.YES;
    }
});
RULES

Server — allows docker group members to configure DNS from any
active session, including SSH:

sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules <<'RULES'
polkit.addRule(function(action, subject) {
    if (["org.freedesktop.resolve1.set-dns-servers",
         "org.freedesktop.resolve1.set-domains",
         "org.freedesktop.resolve1.revert"].indexOf(action.id) >= 0 &&
        subject.isInGroup("docker") &&
        subject.active) {
        return polkit.Result.YES;
    }
});
RULES`
