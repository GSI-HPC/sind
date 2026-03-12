// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// NodeHealth holds the health status of a single node.
type NodeHealth struct {
	Container string          // container state: "running", "exited", etc.
	IP        string          // container IP address
	Munge     bool            // munge service healthy
	SSHD      bool            // sshd accepting connections
	Services  map[string]bool // role-specific services (e.g., "slurmctld", "slurmd")
}

// GetNodeHealth checks the health of a single node container.
// If the container is not running, remaining checks are skipped and
// default to false. The role determines which Slurm services are checked.
func GetNodeHealth(ctx context.Context, client *docker.Client, containerName string, role string) (*NodeHealth, error) {
	name := docker.ContainerName(containerName)

	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	health := &NodeHealth{
		Container: info.Status,
		IP:        firstIP(info.IPs),
		Services:  make(map[string]bool),
	}

	// If container is not running, skip all service checks.
	if info.Status != "running" {
		for _, svc := range roleServices(role) {
			health.Services[svc] = false
		}
		return health, nil
	}

	health.Munge = probe.MungeReady(ctx, client, name) == nil
	health.SSHD = probe.SSHDReady(ctx, client, name) == nil

	for _, svc := range roleServices(role) {
		var check probe.Func
		switch svc {
		case "slurmctld":
			check = probe.SlurmctldReady
		case "slurmd":
			check = probe.SlurmdReady
		}
		health.Services[svc] = check(ctx, client, name) == nil
	}

	return health, nil
}

// roleServices returns the Slurm service names for the given role.
func roleServices(role string) []string {
	switch role {
	case "controller":
		return []string{"slurmctld"}
	case "compute":
		return []string{"slurmd"}
	default:
		return nil
	}
}

// firstIP returns the first IP address from the network map.
// When multiple networks are present, the result is non-deterministic
// but always non-empty if any network has an IP.
func firstIP(ips map[docker.NetworkName]string) string {
	for _, ip := range ips {
		if ip != "" {
			return ip
		}
	}
	return ""
}
