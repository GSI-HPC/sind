// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GSI-HPC/go-clikit/fanout"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// powerConcurrency bounds the docker calls a power command runs at once.
const powerConcurrency = 8

// Power commands act on all their nodes in parallel. A node whose docker
// call fails does not stop the others: the command returns the failures of
// all nodes, joined.

// powerTarget is a node a power command acts on: its container, and the
// name and role a display names its progress target by. The name is the
// node's as a node argument writes it: the short name in the default
// cluster, <short name>.<cluster> in another, so that the nodes of a
// command that spans clusters, worker-0 and worker-0.dev, are told apart.
type powerTarget struct {
	container docker.ContainerName
	node      string
	role      string
}

// PowerShutdown gracefully stops the specified nodes (docker stop).
func PowerShutdown(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "stopping", client.StopContainer)
}

// PowerCut immediately kills the specified nodes (docker kill).
func PowerCut(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error {
	return forEachTarget(ctx, client, realm, clusterName, shortNames, "killing", client.KillContainer)
}

// PowerOn starts the specified stopped nodes (docker start). It first starts
// the realm's mesh DNS and SSH relay if they are stopped, as after a host
// reboot, and afterwards points the started nodes' mesh DNS records at their
// addresses: Docker can give a node another address each time it starts.
func PowerOn(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	targets, err := resolveTargets(ctx, client, meshMgr.Realm, clusterName, shortNames)
	if err != nil {
		return err
	}
	return powerOn(ctx, client, meshMgr, clusterName, targets)
}

// PowerReboot gracefully restarts the specified nodes: it stops them all
// (docker stop), then starts them as PowerOn does.
func PowerReboot(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	return powerRestart(ctx, client, meshMgr, clusterName, shortNames, "stopping", client.StopContainer)
}

// PowerCycle hard-restarts the specified nodes: it kills them all (docker
// kill), then starts them as PowerOn does.
func PowerCycle(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	return powerRestart(ctx, client, meshMgr, clusterName, shortNames, "killing", client.KillContainer)
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
	targets, err := resolveTargets(ctx, client, realm, clusterName, shortNames)
	if err != nil {
		return err
	}
	_, err = forEachContainer(ctx, targets, verb, op)
	return err
}

// powerRestart takes the specified nodes down with op, then starts the ones
// it took down as PowerOn does.
func powerRestart(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string, verb string, op func(context.Context, docker.ContainerName) error) error {
	targets, err := resolveTargets(ctx, client, meshMgr.Realm, clusterName, shortNames)
	if err != nil {
		return err
	}
	down, downErr := forEachContainer(ctx, targets, verb, op)
	return errors.Join(downErr, powerOn(ctx, client, meshMgr, clusterName, down))
}

// powerOn starts the realm's mesh if needed, then the given containers, and
// re-registers the mesh DNS records of the ones that started.
func powerOn(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, targets []powerTarget) error {
	if len(targets) == 0 {
		return nil
	}
	dnsIP, hasDNS, err := meshMgr.StartMesh(ctx)
	if err != nil {
		return fmt.Errorf("starting mesh: %w", err)
	}
	started, startErr := forEachContainer(ctx, targets, "starting", client.StartContainer)
	if !hasDNS {
		return startErr
	}
	containers := make([]docker.ContainerName, len(started))
	for i, t := range started {
		containers[i] = t.container
	}
	return errors.Join(startErr, refreshDNSRecords(ctx, client, meshMgr, clusterName, containers, dnsIP))
}

// refreshDNSRecords points the mesh DNS records of the given running nodes
// at their current cluster network addresses, and warns about nodes that
// resolve through another mesh DNS address than dnsIP.
func refreshDNSRecords(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, containers []docker.ContainerName, dnsIP string) error {
	if len(containers) == 0 {
		return nil
	}
	infos, err := client.InspectContainers(ctx, containers...)
	if err != nil {
		return fmt.Errorf("inspecting started nodes: %w", err)
	}
	meshMgr.WarnStaleDNS(ctx, dnsIP, infos...)

	netName := NetworkName(meshMgr.Realm, clusterName)
	prefix := ContainerPrefix(meshMgr.Realm, clusterName)
	var records []mesh.DNSRecord
	for _, info := range infos {
		ip := info.IPs[netName]
		if ip == "" {
			continue
		}
		shortName := strings.TrimPrefix(string(info.Name), prefix)
		records = append(records, mesh.DNSRecord{Hostname: DNSName(shortName, clusterName, meshMgr.Realm), IP: ip})
	}
	if err := meshMgr.AddDNSRecords(ctx, records); err != nil {
		return fmt.Errorf("updating DNS records: %w", err)
	}
	return nil
}

// forEachContainer applies op to the container of every target in
// parallel, at most powerConcurrency at a time, as the progress step verb
// with a target for each node (fanout.Map). It returns the targets op
// succeeded on, and the failures, each as "<verb> <container>: <error>",
// joined in the order of targets. Once ctx has ended no further container
// is tried, and those left out fail with the context's error; a panic in
// op becomes its node's error, with its stack where panicLog says.
func forEachContainer(ctx context.Context, targets []powerTarget, verb string, op func(context.Context, docker.ContainerName) error) ([]powerTarget, error) {
	containers := make(map[string]docker.ContainerName, len(targets))
	for _, t := range targets {
		containers[t.node] = t.container
	}
	outcomes, err := fanout.Map(ctx, targets, fanout.MapOptions[powerTarget]{
		Step:     verb,
		Limit:    powerConcurrency,
		Program:  program,
		PanicLog: panicLog(ctx),
		Describe: func(t powerTarget) fanout.Item {
			return fanout.Item{Node: t.node, Role: t.role}
		},
		Summarize: joinFailures(func(f fanout.Failed) error {
			return fmt.Errorf("%s %s: %w", verb, containers[f.Name], f.Err)
		}),
	}, func(ctx context.Context, t powerTarget) (struct{}, error) {
		return struct{}{}, op(ctx, t.container)
	})
	var done []powerTarget
	for i, o := range outcomes {
		if o.Err == nil {
			done = append(done, targets[i])
		}
	}
	return done, err
}

// resolveTargets validates that all shortNames exist in the cluster and returns
// their Docker container names, with their names (see powerTarget) and
// roles. Returns nil without error for empty shortNames.
func resolveTargets(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) ([]powerTarget, error) {
	if len(shortNames) == 0 {
		return nil, nil
	}

	entries, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	existing := make(map[docker.ContainerName]docker.ContainerListEntry, len(entries))
	for _, e := range entries {
		existing[e.Name] = e
	}

	suffix := ""
	if clusterName != config.DefaultClusterName {
		suffix = "." + clusterName
	}
	targets := make([]powerTarget, 0, len(shortNames))
	for _, name := range shortNames {
		cn := ContainerName(realm, clusterName, name)
		e, ok := existing[cn]
		if !ok {
			return nil, errorWith(ErrNodeNotFound, "node %q not found in cluster %q", name, clusterName)
		}
		targets = append(targets, powerTarget{container: cn, node: name + suffix, role: e.Labels[LabelRole]})
	}
	return targets, nil
}
