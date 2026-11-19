// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// DaemonInfo is the part of `docker info` that sind checks: facts about the
// daemon, which need not run on the machine sind runs on, and the version
// of the docker CLI that asked.
type DaemonInfo struct {
	// ServerVersion is the Docker Engine version, e.g. "28.1.1".
	ServerVersion string `json:"ServerVersion"`
	// CgroupVersion is the cgroup version containers run on, "1" or "2".
	CgroupVersion string `json:"CgroupVersion"`
	// CgroupDriver is how the daemon places containers in cgroups:
	// "systemd" (/system.slice/docker-<id>.scope) or "cgroupfs"
	// (/docker/<id>).
	CgroupDriver string `json:"CgroupDriver"`
	// SecurityOptions lists the daemon's security features, one entry per
	// feature, e.g. "name=seccomp,profile=builtin", "name=rootless" or
	// "name=userns".
	SecurityOptions []string `json:"SecurityOptions"`
	// OperatingSystem is the daemon host's operating system, e.g.
	// "Ubuntu 24.04.3 LTS", or "Docker Desktop" in Docker Desktop's VM.
	OperatingSystem string `json:"OperatingSystem"`
	// Name is the daemon host's hostname, "docker-desktop" in Docker
	// Desktop's VM.
	Name string `json:"Name"`
	// KernelVersion is the release of the kernel the daemon runs on, as
	// uname -r prints it, e.g. "6.8.0-1017-azure".
	KernelVersion string `json:"KernelVersion"`
	// MemTotal is the memory of the daemon host in bytes, and NCPU its
	// CPUs: what all containers share.
	MemTotal int64 `json:"MemTotal"`
	NCPU     int   `json:"NCPU"`
	// ClientVersion is the version of the docker CLI, e.g. "28.1.1", from
	// the output's ClientInfo: the CLI parses the options of docker create
	// itself, so an old one refuses some that the Engine knows.
	ClientVersion string `json:"-"`
}

// Info runs `docker info` and returns what it says about the daemon.
//
// With --format the docker CLI reports a daemon it cannot reach in the
// output's ServerErrors, and may still exit 0 (up to Docker 28.0). Info
// returns the first entry that says that the daemon could not be reached
// as an *UnreachableError, as a failed command would, and else the first
// entry as a plain error.
func (c *Client) Info(ctx context.Context) (*DaemonInfo, error) {
	stdout, _, err := c.run(ctx, "info", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var out struct {
		DaemonInfo
		ServerErrors []string `json:"ServerErrors"`
		ClientInfo   struct {
			Version string `json:"Version"`
		} `json:"ClientInfo"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		return nil, fmt.Errorf("parsing docker info: %w", err)
	}
	for _, msg := range out.ServerErrors {
		if reason, ok := unreachableReason(msg); ok {
			return nil, &UnreachableError{Err: errors.New(msg), Reason: reason}
		}
	}
	if len(out.ServerErrors) > 0 {
		return nil, errors.New(out.ServerErrors[0])
	}
	if out.ServerVersion == "" {
		return nil, errors.New("docker info reported no server version")
	}
	out.ClientVersion = out.ClientInfo.Version
	return &out.DaemonInfo, nil
}

// HasSecurityOption reports whether the daemon lists the security feature
// name among its SecurityOptions, e.g. "rootless" or "userns".
func (i *DaemonInfo) HasSecurityOption(name string) bool {
	return slices.ContainsFunc(i.SecurityOptions, func(opt string) bool {
		return slices.Contains(strings.Split(opt, ","), "name="+name)
	})
}

// DockerDesktop reports whether the daemon is Docker Desktop's, which runs
// in a VM with a kernel of its own, not on the machine of the docker CLI.
func (i *DaemonInfo) DockerDesktop() bool {
	return i.OperatingSystem == "Docker Desktop" || i.Name == "docker-desktop"
}
