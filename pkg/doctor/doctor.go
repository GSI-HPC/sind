// SPDX-License-Identifier: LGPL-3.0-or-later

// Package doctor provides host prerequisite checks for sind.
package doctor

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/spf13/afero"
)

// MinDockerMajor is the minimum required Docker Engine major version.
const MinDockerMajor = 28

// The remediations for the common reasons the docker CLI cannot reach the
// daemon.
const (
	dockerInstallRemediation = "Install Docker Engine 28 or later: https://docs.docker.com/engine/install/"

	dockerPermissionRemediation = "Add your user to the docker group, then log in again (or run newgrp docker):\n" +
		"\n" +
		"sudo usermod -aG docker $USER"

	dockerStartRemediation = "Start the Docker daemon:\n" +
		"\n" +
		"sudo systemctl start docker"
)

// DockerUnreachable describes why the docker CLI could not reach the
// daemon: the detail "not reachable: " followed by the first line of the
// error, and the commands that fix it when the cause is a missing docker
// CLI, a user outside the docker group or a daemon that is not running.
func DockerUnreachable(err error) (detail, remediation string) {
	if errors.Is(err, exec.ErrNotFound) {
		return "not reachable: docker CLI not found in PATH", dockerInstallRemediation
	}
	msg := err.Error()
	if exitErr, ok := errors.AsType[*cmdexec.ExitError](err); ok && strings.TrimSpace(exitErr.Stderr) != "" {
		msg = exitErr.Stderr
	}
	msg, _, _ = strings.Cut(strings.TrimSpace(msg), "\n")
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "permission denied"):
		remediation = dockerPermissionRemediation
	case strings.Contains(lower, "cannot connect to the docker daemon"):
		remediation = dockerStartRemediation
	}
	return "not reachable: " + strings.TrimSpace(msg), remediation
}

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

// CgroupInfo reads /proc/mounts from fs and returns the cgroup2 mount path,
// whether cgroup2 is mounted at all, and whether nsdelegate is enabled.
func CgroupInfo(fs afero.Fs) (mountPath string, hasV2, hasNsdelegate bool) {
	data, err := afero.ReadFile(fs, "/proc/mounts")
	if err != nil {
		return "", false, false
	}
	return parseCgroupInfo(string(data))
}

// parseCgroupInfo extracts cgroup2 info from mount table content.
func parseCgroupInfo(mounts string) (mountPath string, hasV2, hasNsdelegate bool) {
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[2] == "cgroup2" {
			return fields[1], true, strings.Contains(fields[3], "nsdelegate")
		}
	}
	return "", false, false
}
