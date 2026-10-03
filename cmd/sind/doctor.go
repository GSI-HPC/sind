// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/GSI-HPC/sind/internal/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/doctor"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
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

// nsdelegateRemediation is shown when cgroup2 is mounted at mountPath
// without nsdelegate.
func nsdelegateRemediation(mountPath string) string {
	return "Enable nsdelegate temporarily:\n" +
		"\n" +
		"sudo mount -o remount,nsdelegate " + mountPath + "\n" +
		"\n" +
		"Enable nsdelegate on boot (systemd):\n" +
		"\n" +
		"sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d\n" +
		`echo -e '[Mount]\nOptions=nsdelegate' \` + "\n" +
		"  | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf\n" +
		"sudo systemctl daemon-reload"
}

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

// remoteDockerHost reports whether DOCKER_HOST names a daemon that is not
// reached through a local socket, whose host limits this machine's /proc
// does not show.
func remoteDockerHost() bool {
	host := os.Getenv("DOCKER_HOST")
	return host != "" && !strings.HasPrefix(host, "unix://")
}

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

	// Check cgroup2 with nsdelegate: the cgroup version containers run on
	// comes from the daemon, the nsdelegate mount option from this host's
	// /proc/mounts, which must show the unified hierarchy.
	log := sindlog.From(ctx)
	log.Log(ctx, sindlog.LevelTrace, "reading /proc/mounts for cgroup2 info")
	mountPath, hasV2, hasNsd := doctor.CgroupInfo(fs)
	log.Log(ctx, sindlog.LevelTrace, "cgroup2 check", "mountPath", mountPath, "v2", hasV2, "nsdelegate", hasNsd)
	switch {
	case info != nil && info.CgroupVersion != "" && info.CgroupVersion != "2":
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail:      fmt.Sprintf("Docker runs containers on cgroup v%s (sind requires cgroupv2)", info.CgroupVersion),
			Remediation: unifiedRemediation})
		failures = append(failures, "cgroup")
	case !hasV2 && mountPath != "":
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail:      fmt.Sprintf("hybrid hierarchy: cgroup2 is mounted at %s, not %s (sind requires cgroupv2)", mountPath, doctor.CgroupRoot),
			Remediation: unifiedRemediation})
		failures = append(failures, "cgroup")
	case !hasV2:
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail: "not mounted (sind requires cgroupv2)", Remediation: unifiedRemediation})
		failures = append(failures, "cgroup")
	case !hasNsd:
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail: "nsdelegate not found", Remediation: nsdelegateRemediation(mountPath)})
		failures = append(failures, "cgroup-nsdelegate")
	default:
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkOK,
			Detail: fmt.Sprintf("nsdelegate enabled (%s)", mountPath)})
	}

	// Advisory: enough inotify instances for large clusters. The limit is
	// the Docker host's, so the check is left out for a remote daemon.
	if n, ok := doctor.InotifyInstances(fs); ok && !remoteDockerHost() {
		inotify := doctorCheck{Name: "inotify", Status: checkOK,
			Detail: fmt.Sprintf("max_user_instances %d (>= %d)", n, doctor.MinInotifyInstances)}
		if n < doctor.MinInotifyInstances {
			inotify = doctorCheck{Name: "inotify", Status: checkWarning,
				Detail:      fmt.Sprintf("max_user_instances %d (clusters of 10 or more nodes need %d; optional)", n, doctor.MinInotifyInstances),
				Remediation: inotifyRemediation}
		}
		checks = append(checks, inotify)
	}

	// Advisory: host DNS resolution via systemd-resolved.
	if mgr.ResolvedActive(ctx) {
		if mgr.DNSPolkitAuthorized(ctx) {
			checks = append(checks, doctorCheck{Name: "DNS policy", Status: checkOK,
				Detail: "host resolution available"})
		} else {
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

// printChecks writes one line per check, each failed or warning check
// followed by its remediation between blank lines. A detail can quote what
// docker wrote, so it is escaped.
func printChecks(w io.Writer, checks []doctorCheck) {
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "%s %s: %s\n", checkmark(c.Status == checkOK), c.Name, termtext.EscapeText(c.Detail))
		if c.Remediation != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n\n", c.Remediation)
		}
	}
}
