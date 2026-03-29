// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
	out := cmd.OutOrStdout()
	client := clientFrom(cmd.Context())
	var failed bool

	// Check Docker Engine version.
	version, err := client.ServerVersion(cmd.Context())
	if err != nil {
		printResult(out, false, "Docker Engine: not reachable")
		failed = true
	} else {
		major, _, parseErr := parseVersion(version)
		if parseErr != nil {
			printResult(out, false, "Docker Engine: %s (unable to parse version)", version)
			failed = true
		} else if major < 28 {
			printResult(out, false, "Docker Engine: %s (requires >= 28.0)", version)
			failed = true
		} else {
			printResult(out, true, "Docker Engine: %s (>= 28.0)", version)
		}
	}

	// Check cgroup2 with nsdelegate.
	mountPath, hasV2, hasNsd := cgroupInfo()
	if !hasV2 {
		printResult(out, false, "cgroup: v2 not mounted (sind requires cgroupv2)")
		failed = true
	} else if !hasNsd {
		printResult(out, false, "cgroupv2: nsdelegate not found")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Enable nsdelegate temporarily:")
		fmt.Fprintf(out, "    sudo mount -o remount,nsdelegate %s\n", mountPath)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Enable nsdelegate on boot (systemd):")
		fmt.Fprintln(out, "    sudo mkdir -p /etc/systemd/system/sys-fs-cgroup.mount.d")
		fmt.Fprintln(out, "    echo -e '[Mount]\\nOptions=nsdelegate' \\")
		fmt.Fprintln(out, "      | sudo tee /etc/systemd/system/sys-fs-cgroup.mount.d/nsdelegate.conf")
		fmt.Fprintln(out, "    sudo systemctl daemon-reload")
		failed = true
	} else {
		printResult(out, true, "cgroupv2: nsdelegate enabled (%s)", mountPath)
	}

	if failed {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

func printResult(out io.Writer, ok bool, format string, args ...any) {
	fmt.Fprintf(out, "%s %s\n", checkmark(ok), fmt.Sprintf(format, args...))
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
