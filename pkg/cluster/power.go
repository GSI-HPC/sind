// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// PowerShutdown gracefully stops the specified nodes (docker stop).
func PowerShutdown(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "stopping", client.StopContainer)
}

// PowerCut immediately kills the specified nodes (docker kill).
func PowerCut(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "killing", client.KillContainer)
}

// PowerOn starts the specified stopped nodes (docker start).
func PowerOn(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "starting", client.StartContainer)
}

// PowerReboot gracefully restarts the specified nodes (docker stop + start).
func PowerReboot(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTargetPair(ctx, client, realm, clusterName, shortNames,
		"stopping", client.StopContainer, "starting", client.StartContainer)
}

// PowerCycle hard-restarts the specified nodes (docker kill + start).
func PowerCycle(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTargetPair(ctx, client, realm, clusterName, shortNames,
		"killing", client.KillContainer, "starting", client.StartContainer)
}

// PowerFreeze suspends all processes in the specified nodes (docker pause).
// The containers remain running but are completely unresponsive.
func PowerFreeze(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "pausing", client.PauseContainer)
}

// PowerUnfreeze resumes the specified frozen nodes (docker unpause).
func PowerUnfreeze(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "unpausing", client.UnpauseContainer)
}

// forEachTarget resolves node names, then applies op to each container.
func forEachTarget(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string, verb string, op func(context.Context, docker.ContainerName) error) error {
	containers, err := resolveTargets(ctx, client, realm, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := op(ctx, name); err != nil {
			return fmt.Errorf("%s %s: %w", verb, name, err)
		}
	}
	return nil
}

// forEachTargetPair resolves node names, then applies two operations per container.
func forEachTargetPair(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string, verb1 string, op1 func(context.Context, docker.ContainerName) error, verb2 string, op2 func(context.Context, docker.ContainerName) error) error {
	containers, err := resolveTargets(ctx, client, realm, clusterName, shortNames)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := op1(ctx, name); err != nil {
			return fmt.Errorf("%s %s: %w", verb1, name, err)
		}
		if err := op2(ctx, name); err != nil {
			return fmt.Errorf("%s %s: %w", verb2, name, err)
		}
	}
	return nil
}

// resolveTargets validates that all shortNames exist in the cluster and returns
// their Docker container names. Returns nil without error for empty shortNames.
func resolveTargets(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) ([]docker.ContainerName, error) {
	if len(shortNames) == 0 {
		return nil, nil
	}

	entries, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	existing := make(map[docker.ContainerName]bool, len(entries))
	for _, e := range entries {
		existing[e.Name] = true
	}

	targets := make([]docker.ContainerName, 0, len(shortNames))
	for _, name := range shortNames {
		cn := ContainerName(realm, clusterName, name)
		if !existing[cn] {
			return nil, fmt.Errorf("node %q not found in cluster %q", name, clusterName)
		}
		targets = append(targets, cn)
	}
	return targets, nil
}
