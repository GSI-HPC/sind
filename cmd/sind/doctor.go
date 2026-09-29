// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"strings"

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
	version, err := client.ServerVersion(ctx)
	if err != nil {
		engine.Detail = "not reachable"
	} else if vErr := doctor.CheckDockerVersion(version); vErr != nil {
		engine.Detail = vErr.Error()
	} else {
		engine.Status = checkOK
		engine.Detail = fmt.Sprintf("%s (>= %d.0)", version, doctor.MinDockerMajor)
	}
	if engine.Status == checkFailed {
		failures = append(failures, "docker")
	}
	checks = append(checks, engine)

	// Check cgroup2 with nsdelegate.
	log := sindlog.From(ctx)
	log.Log(ctx, sindlog.LevelTrace, "reading /proc/mounts for cgroup2 info")
	mountPath, hasV2, hasNsd := doctor.CgroupInfo(fs)
	log.Log(ctx, sindlog.LevelTrace, "cgroup2 check", "mountPath", mountPath, "v2", hasV2, "nsdelegate", hasNsd)
	switch {
	case !hasV2:
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail: "not mounted (sind requires cgroupv2)"})
		failures = append(failures, "cgroup")
	case !hasNsd:
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkFailed,
			Detail: "nsdelegate not found", Remediation: nsdelegateRemediation(mountPath)})
		failures = append(failures, "cgroup-nsdelegate")
	default:
		checks = append(checks, doctorCheck{Name: "cgroupv2", Status: checkOK,
			Detail: fmt.Sprintf("nsdelegate enabled (%s)", mountPath)})
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
// followed by its remediation between blank lines.
func printChecks(w io.Writer, checks []doctorCheck) {
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "%s %s: %s\n", checkmark(c.Status == checkOK), c.Name, c.Detail)
		if c.Remediation != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n\n", c.Remediation)
		}
	}
}
