// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// doctorCheck is the result of one host prerequisite check, in the form
// `sind doctor -o json` prints it: clusterctl's check model, plus the
// commands that fix a failed check.
type doctorCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// The statuses a check can report. A failed advisory check is a warning,
// which does not affect the exit status.
const (
	checkOK      = "ok"
	checkWarning = "warning"
	checkFailed  = "failed"
)

// unifiedRemediation is shown when the host does not run the unified
// cgroup2 hierarchy: cgroup v1, or systemd's hybrid mode.
const unifiedRemediation = `Boot with the unified cgroup hierarchy: add
systemd.unified_cgroup_hierarchy=1 to the kernel command line (for GRUB,
GRUB_CMDLINE_LINUX in /etc/default/grub), regenerate the boot loader
configuration and reboot.`

// inotifyRemediation is shown when fs.inotify.max_user_instances is below
// doctor.MinInotifyInstances.
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

func newDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system prerequisites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd)
		},
	}

	cmd.Flags().StringP("output", "o", "human", "output format (human|json)")

	return cmd
}

func runDoctor(cmd *cobra.Command) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	ctx := cmd.Context()
	fs := fsFrom(ctx)
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	mgr := meshMgrFrom(ctx, client, realm)
	var checks []doctorCheck
	var failures []string

	// Check Docker Engine version.
	engine := doctorCheck{Name: "Docker Engine", Status: checkFailed}
	info, err := client.Info(ctx)
	if err != nil {
		engine.Detail, engine.Remediation = doctor.DockerUnreachable(err)
	} else if vErr := doctor.CheckDockerVersion(info.ServerVersion); vErr != nil {
		engine.Detail = vErr.Error()
	} else {
		engine.Status = checkOK
		engine.Detail = fmt.Sprintf("%s (>= %d.0)", info.ServerVersion, doctor.MinDockerMajor)
	}
	if engine.Status == checkFailed {
		failures = append(failures, "docker")
	}
	checks = append(checks, engine)

	// Check that the daemon can start sind's nodes: not rootless and
	// without userns-remap, which refuse writable cgroups.
	if info != nil {
		daemon := doctorCheck{Name: "Docker daemon", Status: checkOK, Detail: "rootful, no userns-remap"}
		switch err := cluster.DaemonSupport(info); {
		case errors.Is(err, cluster.ErrRootlessDaemon):
			daemon = doctorCheck{Name: "Docker daemon", Status: checkFailed,
				Detail: "rootless mode (sind needs a rootful daemon)", Remediation: rootlessRemediation}
		case errors.Is(err, cluster.ErrUsernsRemap):
			daemon = doctorCheck{Name: "Docker daemon", Status: checkFailed,
				Detail: "userns-remap enabled (sind needs a daemon without it)", Remediation: usernsRemediation}
		}
		if daemon.Status == checkFailed {
			failures = append(failures, "docker-daemon")
		}
		checks = append(checks, daemon)
	}

	// Where the daemon runs: the checks below that read this machine
	// describe the Docker host only when the daemon runs here.
	log := sindlog.From(ctx)
	loc, locErr := doctor.LocateDaemon(ctx, client, fs, info)
	local := locErr == nil && loc.Local()
	log.Log(ctx, sindlog.LevelTrace, "docker host", "endpoint", loc.Endpoint.Host, "context", loc.Endpoint.Context, "local", local)
	if info != nil {
		checks = append(checks, hostCheck(loc, locErr))
	}

	cgroup, failure := cgroupCheck(ctx, client, fs, info, local)
	if failure != "" {
		failures = append(failures, failure)
	}
	checks = append(checks, cgroup)

	// Advisory: enough inotify instances for large clusters. The limit is
	// the Docker host's, so the check is left out for a daemon elsewhere.
	if n, ok := doctor.InotifyInstances(fs); ok && local {
		inotify := doctorCheck{Name: "inotify", Status: checkOK,
			Detail: fmt.Sprintf("max_user_instances %d (>= %d)", n, doctor.MinInotifyInstances)}
		if n < doctor.MinInotifyInstances {
			inotify = doctorCheck{Name: "inotify", Status: checkWarning,
				Detail:      fmt.Sprintf("max_user_instances %d (clusters of 10 or more nodes need %d; optional)", n, doctor.MinInotifyInstances),
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
			checks = append(checks, doctorCheck{Name: "DNS policy", Status: checkWarning,
				Detail: "not available: the Docker daemon does not run on this machine (optional)"})
		case mgr.DNSPolkitAuthorized(ctx):
			checks = append(checks, doctorCheck{Name: "DNS policy", Status: checkOK,
				Detail: "host resolution available"})
		default:
			checks = append(checks, doctorCheck{Name: "DNS policy", Status: checkWarning,
				Detail: "not authorized (optional)", Remediation: dnsPolicyRemediation})
		}
	}

	if isJSONOutput(cmd) {
		if err := writeJSON(cmd.OutOrStdout(), checks); err != nil {
			return err
		}
	} else {
		printChecks(cmd.OutOrStdout(), checks)
	}
	if len(failures) > 0 {
		return fmt.Errorf("checks failed: %s", strings.Join(failures, ", "))
	}
	return nil
}

// hostCheck reports where the Docker daemon runs (doctor.LocateDaemon). A
// daemon elsewhere is a warning: sind does not support it, but every
// required check asks the daemon or probes its kernel, so they still hold.
func hostCheck(loc doctor.DaemonLocation, err error) doctorCheck {
	switch {
	case err != nil:
		return doctorCheck{Name: "Docker host", Status: checkWarning, Detail: "unknown: " + doctor.ErrorLine(err)}
	case !loc.Local():
		return doctorCheck{Name: "Docker host", Status: checkWarning,
			Detail: "not this machine: " + loc.Elsewhere + " (unsupported)", Remediation: elsewhereRemediation}
	}
	return doctorCheck{Name: "Docker host", Status: checkOK, Detail: "this machine (" + loc.Endpoint.Host + ")"}
}

// cgroupCheck checks that the Docker host runs the unified cgroup2
// hierarchy with nsdelegate, and returns the check and, when it failed,
// the name of the failure. The cgroup version comes from the daemon (info).
// For a daemon on this machine the cgroup2 mount comes from /proc/mounts in
// fs, as the kernel is the same; for one elsewhere, from a throwaway
// container of the default node image (doctor.ProbeCgroupInfo), if the
// daemon has that image: doctor pulls none.
func cgroupCheck(ctx context.Context, client *docker.Client, fs afero.Fs, info *docker.DaemonInfo, local bool) (doctorCheck, string) {
	log := sindlog.From(ctx)
	if info != nil && info.CgroupVersion != "" && info.CgroupVersion != "2" {
		return doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail:      fmt.Sprintf("Docker runs containers on cgroup v%s (sind requires cgroupv2)", info.CgroupVersion),
			Remediation: unifiedRemediation}, "cgroup"
	}
	var mountPath, where string
	var hasV2, hasNsd bool
	switch {
	case local:
		log.Log(ctx, sindlog.LevelTrace, "reading /proc/mounts for cgroup2 info")
		mountPath, hasV2, hasNsd = doctor.CgroupInfo(fs)
	case info == nil:
		return doctorCheck{Name: "cgroupv2", Status: checkWarning, Detail: "nsdelegate not checked: Docker is not reachable"}, ""
	default:
		image := config.DefaultImage
		_, present, err := client.ImageLabels(ctx, image)
		if err == nil && !present {
			return doctorCheck{Name: "cgroupv2", Status: checkWarning,
				Detail:      fmt.Sprintf("nsdelegate not checked: the Docker host has no %s to probe it with (sind create checks it)", image),
				Remediation: "Pull the node image, then run sind doctor again:\n\ndocker pull " + image}, ""
		}
		if err == nil {
			mountPath, hasV2, hasNsd, err = doctor.ProbeCgroupInfo(ctx, client, image)
		}
		if err != nil {
			return doctorCheck{Name: "cgroupv2", Status: checkFailed, Detail: "nsdelegate not checked: " + doctor.ErrorLine(err)}, "cgroup"
		}
		where = ", in a container"
	}
	log.Log(ctx, sindlog.LevelTrace, "cgroup2 check", "mountPath", mountPath, "v2", hasV2, "nsdelegate", hasNsd, "local", local)
	switch {
	case !hasV2 && mountPath != "":
		return doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail:      fmt.Sprintf("hybrid hierarchy: cgroup2 is mounted at %s, not %s (sind requires cgroupv2)", mountPath, doctor.CgroupRoot),
			Remediation: unifiedRemediation}, "cgroup"
	case !hasV2:
		return doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail: "not mounted (sind requires cgroupv2)", Remediation: unifiedRemediation}, "cgroup"
	case !hasNsd:
		return doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail: "nsdelegate not found", Remediation: doctor.NsdelegateRemediation(mountPath)}, "cgroup-nsdelegate"
	}
	return doctorCheck{Name: "cgroupv2", Status: checkOK, Detail: fmt.Sprintf("nsdelegate enabled (%s%s)", mountPath, where)}, ""
}

// printChecks writes one line per check, each failed or warning check
// followed by its remediation between blank lines. A detail can quote what
// docker wrote, so it is escaped.
func printChecks(w io.Writer, checks []doctorCheck) {
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "%s %s: %s\n", checkmark(c.Status == checkOK), c.Name, termtext.EscapeLines(c.Detail))
		if c.Remediation != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n\n", c.Remediation)
		}
	}
}
