// SPDX-License-Identifier: LGPL-3.0-or-later

// Package doctor provides host prerequisite checks for sind.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
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

// MinInotifyInstances is the fs.inotify.max_user_instances that clusters
// of 10 or more nodes need. The systemd and journald of every node take
// inotify instances of the host's root user, and the kernel's default of
// 128 runs out first.
const MinInotifyInstances = 1024

// inotifyInstancesPath is where the kernel exposes
// fs.inotify.max_user_instances.
const inotifyInstancesPath = "/proc/sys/fs/inotify/max_user_instances"

// InotifyInstances reads fs.inotify.max_user_instances from fs. ok is false
// when it cannot be read or parsed.
func InotifyInstances(fs afero.Fs) (n int, ok bool) {
	data, err := afero.ReadFile(fs, inotifyInstancesPath)
	if err != nil {
		return 0, false
	}
	n, err = strconv.Atoi(strings.TrimSpace(string(data)))
	return n, err == nil
}

// CgroupRoot is where the unified cgroup2 hierarchy that sind needs is
// mounted. A systemd host in hybrid mode mounts cgroup2 elsewhere
// (/sys/fs/cgroup/unified) and runs containers on cgroup v1.
const CgroupRoot = "/sys/fs/cgroup"

// CgroupInfo reads /proc/mounts from fs. hasV2 reports whether cgroup2 is
// mounted at CgroupRoot, the unified hierarchy, and hasNsdelegate whether
// that mount has the nsdelegate option. mountPath is CgroupRoot then, or
// where else cgroup2 is mounted (a hybrid host), or empty.
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
		if len(fields) < 4 || fields[2] != "cgroup2" {
			continue
		}
		if fields[1] == CgroupRoot {
			return CgroupRoot, true, slices.Contains(strings.Split(fields[3], ","), "nsdelegate")
		}
		if mountPath == "" {
			mountPath = fields[1]
		}
	}
	return mountPath, false, false
}

// ProbeCgroupInfo is CgroupInfo for the kernel the Docker daemon runs on,
// wherever that is: it reads the mount table in a throwaway container of
// image, without a network and with cat as the entrypoint, as a custom
// node image may set one. On a cgroup v2 daemon a container has cgroup2
// mounted at CgroupRoot, and the kernel shows nsdelegate, an option of the
// whole cgroup2 hierarchy, in every cgroup2 mount, in a container's cgroup
// namespace too. docker run pulls image if the daemon does not have it.
func ProbeCgroupInfo(ctx context.Context, client *docker.Client, image string) (mountPath string, hasV2, hasNsdelegate bool, err error) {
	mounts, err := client.RunEphemeralWith(ctx, []string{"--network", "none", "--entrypoint", "cat"}, image, "/proc/self/mounts")
	if err != nil {
		return "", false, false, fmt.Errorf("reading the mount table in a container of %s: %w", image, err)
	}
	mountPath, hasV2, hasNsdelegate = parseCgroupInfo(mounts)
	return mountPath, hasV2, hasNsdelegate, nil
}

// NsdelegateRemediation returns the commands that remount cgroup2 at
// mountPath with nsdelegate on the Docker host, and keep the option across
// reboots there.
func NsdelegateRemediation(mountPath string) string {
	return "Enable nsdelegate on the Docker host temporarily:\n" +
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
