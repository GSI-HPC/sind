// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// PowerShutdown gracefully stops the specified nodes (docker stop).
func PowerShutdown(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.StopContainer(ctx, name); err != nil {
			return fmt.Errorf("stopping %s: %w", name, err)
		}
	}
	return nil
}

// PowerCut immediately kills the specified nodes (docker kill).
func PowerCut(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.KillContainer(ctx, name); err != nil {
			return fmt.Errorf("killing %s: %w", name, err)
		}
	}
	return nil
}

// PowerOn starts the specified stopped nodes (docker start).
func PowerOn(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.StartContainer(ctx, name); err != nil {
			return fmt.Errorf("starting %s: %w", name, err)
		}
	}
	return nil
}

// PowerReboot gracefully restarts the specified nodes (docker stop + start).
func PowerReboot(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.StopContainer(ctx, name); err != nil {
			return fmt.Errorf("stopping %s: %w", name, err)
		}
		if err := client.StartContainer(ctx, name); err != nil {
			return fmt.Errorf("starting %s: %w", name, err)
		}
	}
	return nil
}

// PowerCycle hard-restarts the specified nodes (docker kill + start).
func PowerCycle(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.KillContainer(ctx, name); err != nil {
			return fmt.Errorf("killing %s: %w", name, err)
		}
		if err := client.StartContainer(ctx, name); err != nil {
			return fmt.Errorf("starting %s: %w", name, err)
		}
	}
	return nil
}

// PowerFreeze suspends all processes in the specified nodes (docker pause).
// The containers remain running but are completely unresponsive.
func PowerFreeze(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.PauseContainer(ctx, name); err != nil {
			return fmt.Errorf("pausing %s: %w", name, err)
		}
	}
	return nil
}

// PowerUnfreeze resumes the specified frozen nodes (docker unpause).
func PowerUnfreeze(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) error {
	containers, err := resolveTargets(ctx, client, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := client.UnpauseContainer(ctx, name); err != nil {
			return fmt.Errorf("unpausing %s: %w", name, err)
		}
	}
	return nil
}

// resolveTargets validates that all shortNames exist in the cluster and returns
// their Docker container names. Returns nil without error for empty shortNames.
func resolveTargets(ctx context.Context, client *docker.Client, clusterName string, shortNames []string) ([]docker.ContainerName, error) {
	if len(shortNames) == 0 {
		return nil, nil
	}

	entries, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing cluster containers: %w", err)
	}

	existing := make(map[docker.ContainerName]bool, len(entries))
	for _, e := range entries {
		existing[e.Name] = true
	}

	targets := make([]docker.ContainerName, 0, len(shortNames))
	for _, name := range shortNames {
		cn := ContainerName(clusterName, name)
		if !existing[cn] {
			return nil, fmt.Errorf("node %q not found in cluster %q", name, clusterName)
		}
		targets = append(targets, cn)
	}
	return targets, nil
}
