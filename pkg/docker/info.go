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
// daemon, which need not run on the machine sind runs on.
type DaemonInfo struct {
	// ServerVersion is the Docker Engine version, e.g. "28.1.1".
	ServerVersion string `json:"ServerVersion"`
	// CgroupVersion is the cgroup version containers run on, "1" or "2".
	CgroupVersion string `json:"CgroupVersion"`
	// SecurityOptions lists the daemon's security features, one entry per
	// feature, e.g. "name=seccomp,profile=builtin", "name=rootless" or
	// "name=userns".
	SecurityOptions []string `json:"SecurityOptions"`
}

// Info runs `docker info` and returns what it says about the daemon.
//
// With --format the docker CLI reports a daemon it cannot reach in the
// output's ServerErrors, and may still exit 0; Info returns the first of
// them as its error.
func (c *Client) Info(ctx context.Context) (*DaemonInfo, error) {
	stdout, _, err := c.run(ctx, "info", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var out struct {
		DaemonInfo
		ServerErrors []string `json:"ServerErrors"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		return nil, fmt.Errorf("parsing docker info: %w", err)
	}
	if len(out.ServerErrors) > 0 {
		return nil, errors.New(out.ServerErrors[0])
	}
	if out.ServerVersion == "" {
		return nil, errors.New("docker info reported no server version")
	}
	return &out.DaemonInfo, nil
}

// HasSecurityOption reports whether the daemon lists the security feature
// name among its SecurityOptions, e.g. "rootless" or "userns".
func (i *DaemonInfo) HasSecurityOption(name string) bool {
	return slices.ContainsFunc(i.SecurityOptions, func(opt string) bool {
		return slices.Contains(strings.Split(opt, ","), "name="+name)
	})
}
