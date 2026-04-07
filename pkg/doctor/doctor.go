// SPDX-License-Identifier: LGPL-3.0-or-later

// Package doctor provides host prerequisite checks for sind.
package doctor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MinDockerMajor is the minimum required Docker Engine major version.
const MinDockerMajor = 28

// ParseVersion extracts the major and minor version numbers from a Docker
// version string such as "28.0.0" or "29.3.1-beta.1".
func ParseVersion(s string) (major, minor int, err error) {
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

// CheckDockerVersion returns an error if the Docker version string is below
// MinDockerMajor or cannot be parsed.
func CheckDockerVersion(version string) error {
	major, _, err := ParseVersion(version)
	if err != nil {
		return fmt.Errorf("%s (unable to parse version)", version)
	}
	if major < MinDockerMajor {
		return fmt.Errorf("%s (requires >= %d.0)", version, MinDockerMajor)
	}
	return nil
}

// CgroupInfo reads /proc/mounts and returns the cgroup2 mount path,
// whether cgroup2 is mounted at all, and whether nsdelegate is enabled.
func CgroupInfo() (mountPath string, hasV2, hasNsdelegate bool) {
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
