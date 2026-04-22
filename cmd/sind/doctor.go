// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/doctor"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check system prerequisites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd)
		},
	}
}

func runDoctor(cmd *cobra.Command) error {
	ctx := cmd.Context()
	fs := fsFrom(ctx)
	client := clientFrom(ctx)
	realm := realmFromFlag(cmd)
	mgr := meshMgrFrom(ctx, client, realm)
	var failures []string

	// Check Docker Engine version.
	version, err := client.ServerVersion(ctx)
	if err != nil {
		printResult(cmd, false, "Docker Engine: not reachable")
		failures = append(failures, "docker")
	} else if vErr := doctor.CheckDockerVersion(version); vErr != nil {
		printResult(cmd, false, "Docker Engine: %s", vErr)
		failures = append(failures, "docker")
	} else {
		printResult(cmd, true, "Docker Engine: %s (>= %d.0)", version, doctor.MinDockerMajor)
	}

	// Check cgroup2 with nsdelegate.
	log := sindlog.From(ctx)
	log.Log(ctx, sindlog.LevelTrace, "reading /proc/mounts for cgroup2 info")
	mountPath, hasV2, hasNsd := doctor.CgroupInfo(fs)
	log.Log(ctx, sindlog.LevelTrace, "cgroup2 check", "mountPath", mountPath, "v2", hasV2, "nsdelegate", hasNsd)
	if !hasV2 {
		printResult(cmd, false, "cgroupv2: not mounted (sind requires cgroupv2)")
		failures = append(failures, "cgroup")
	} else if !hasNsd {
		printResult(cmd, false, "cgroupv2: nsdelegate not found")
		cmd.Println()
		cmd.Println("Enable nsdelegate temporarily:")
		cmd.Println()
		cmd.Printf("sudo mount -o remount,nsdelegate %s\n", mountPath)
		cmd.Println()
		cmd.Println("Enable nsdelegate on boot (systemd):")
		cmd.Println()
		cmd.Println("sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d")
		cmd.Println("echo -e '[Mount]\\nOptions=nsdelegate' \\")
		cmd.Println("  | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf")
		cmd.Println("sudo systemctl daemon-reload")
		failures = append(failures, "cgroup-nsdelegate")
	} else {
		printResult(cmd, true, "cgroupv2: nsdelegate enabled (%s)", mountPath)
	}

	// Advisory: host DNS resolution via systemd-resolved.
	if mgr.ResolvedActive(ctx) {
		if mgr.DNSPolkitAuthorized(ctx) {
			printResult(cmd, true, "DNS policy: host resolution available")
		} else {
			printResult(cmd, false, "DNS policy: not authorized (optional)")
			cmd.Println()
			cmd.Println("Install a polkit rule to enable host DNS resolution for *.sind.")
			cmd.Println("Choose the profile that matches your environment:")
			cmd.Println()
			cmd.Println("Desktop — allows docker group members to configure DNS from local")
			cmd.Println("sessions only (direct keyboard/display access, not SSH):")
			cmd.Println()
			cmd.Println("sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules <<'RULES'")
			cmd.Println("polkit.addRule(function(action, subject) {")
			cmd.Println("    if ([\"org.freedesktop.resolve1.set-dns-servers\",")
			cmd.Println("         \"org.freedesktop.resolve1.set-domains\",")
			cmd.Println("         \"org.freedesktop.resolve1.revert\"].indexOf(action.id) >= 0 &&")
			cmd.Println("        subject.isInGroup(\"docker\") &&")
			cmd.Println("        subject.active && subject.local) {")
			cmd.Println("        return polkit.Result.YES;")
			cmd.Println("    }")
			cmd.Println("});")
			cmd.Println("RULES")
			cmd.Println()
			cmd.Println("Server — allows docker group members to configure DNS from any")
			cmd.Println("active session, including SSH:")
			cmd.Println()
			cmd.Println("sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules <<'RULES'")
			cmd.Println("polkit.addRule(function(action, subject) {")
			cmd.Println("    if ([\"org.freedesktop.resolve1.set-dns-servers\",")
			cmd.Println("         \"org.freedesktop.resolve1.set-domains\",")
			cmd.Println("         \"org.freedesktop.resolve1.revert\"].indexOf(action.id) >= 0 &&")
			cmd.Println("        subject.isInGroup(\"docker\") &&")
			cmd.Println("        subject.active) {")
			cmd.Println("        return polkit.Result.YES;")
			cmd.Println("    }")
			cmd.Println("});")
			cmd.Println("RULES")
			cmd.Println()
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("checks failed: %s", strings.Join(failures, ", "))
	}
	return nil
}

func printResult(cmd *cobra.Command, ok bool, format string, args ...any) {
	cmd.Printf("%s %s\n", checkmark(ok), fmt.Sprintf(format, args...))
}
