// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
	client := clientFrom(ctx)
	realm := realmFromFlag(cmd)
	mgr := meshMgrFrom(ctx, client, realm)
	var failed bool

	// Check Docker Engine version.
	version, err := client.ServerVersion(ctx)
	if err != nil {
		printResult(cmd, false, "Docker Engine: not reachable")
		failed = true
	} else {
		major, _, parseErr := parseVersion(version)
		if parseErr != nil {
			printResult(cmd, false, "Docker Engine: %s (unable to parse version)", version)
			failed = true
		} else if major < 28 {
			printResult(cmd, false, "Docker Engine: %s (requires >= 28.0)", version)
			failed = true
		} else {
			printResult(cmd, true, "Docker Engine: %s (>= 28.0)", version)
		}
	}

	// Check cgroup2 with nsdelegate.
	log := sindlog.From(ctx)
	log.Log(ctx, sindlog.LevelTrace, "reading /proc/mounts for cgroup2 info")
	mountPath, hasV2, hasNsd := cgroupInfo()
	log.Log(ctx, sindlog.LevelTrace, "cgroup2 check", "mountPath", mountPath, "v2", hasV2, "nsdelegate", hasNsd)
	if !hasV2 {
		printResult(cmd, false, "cgroup: v2 not mounted (sind requires cgroupv2)")
		failed = true
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
		failed = true
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
			cmd.Println("Install a polkit rule to enable host DNS resolution for *.sind:")
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
		}
	}

	if failed {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

func printResult(cmd *cobra.Command, ok bool, format string, args ...any) {
	cmd.Printf("%s %s\n", checkmark(ok), fmt.Sprintf(format, args...))
}

func parseVersion(s string) (major, minor int, err error) {
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid version: %s", s)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return major, minor, nil
}

// cgroupInfo reads /proc/mounts and returns the cgroup2 mount path,
// whether cgroup2 is mounted at all, and whether nsdelegate is enabled.
func cgroupInfo() (mountPath string, hasV2, hasNsdelegate bool) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", false, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[2] == "cgroup2" {
			return fields[1], true, strings.Contains(fields[3], "nsdelegate")
		}
	}
	return "", false, false
}
