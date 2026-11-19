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
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/ssh"
	"golang.org/x/sync/errgroup"
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
// It adds the host keys of started nodes that the relay's known_hosts
// lacks (see addMissingHostKeys).
// A stopped monitoring stack of the realm starts with the nodes; one that
// cannot start or get ready fails the call when the cluster is monitored,
// and is a warning (meshMgr.Warn) when it is not.
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
// re-registers the mesh DNS records of the ones that started. Last it
// starts the realm's monitoring stack if it is stopped, and re-registers
// its DNS names (see startMonitoring); container IDs stay the same, so
// Alloy's configuration does too.
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
	dnsErr := refreshDNSRecords(ctx, client, meshMgr, clusterName, containers, dnsIP)
	addMissingHostKeys(ctx, client, meshMgr, clusterName, containers)
	// The Corefile takes one change at a time: the stack's names after
	// the nodes'.
	var stackErr error
	if _, err := startMonitoring(ctx, client, meshMgr); err != nil {
		stackErr = stackFailure(ctx, client, meshMgr, clusterName, err)
	}
	return errors.Join(startErr, dnsErr, stackErr)
}

// stackFailure returns the error of a power command whose start of the
// realm's monitoring stack failed with err, for a monitored cluster. The
// stack is the realm's, so for a cluster that it does not collect, which
// a stack left partial by an interrupted create cannot keep from running,
// the failure is a warning (meshMgr.Warn), and stackFailure returns nil.
// When sind cannot tell, it returns the error.
func stackFailure(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, err error) error {
	if monitored, lerr := clusterMonitored(ctx, client, meshMgr.Realm, clusterName); lerr == nil && !monitored {
		meshMgr.Warn(ctx, fmt.Sprintf("starting the monitoring stack of realm %q: %v", meshMgr.Realm, err))
		return nil
	}
	return fmt.Errorf("starting the monitoring stack: %w", err)
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

// addMissingHostKeys adds the host keys of the given running nodes that the
// relay's known_hosts has no line of. Only the creates collect host keys
// otherwise, and sind v0.8 to v0.12 rewrote known_hosts in place, so that a
// write cut short could lose the keys of nodes that still run, which
// sind ssh NODE CMD and the exported SSH config then refuse. A node's key
// is read from its sshd's key file (ssh.HostKeyFile), which needs no
// running sshd, unlike the ssh-keyscan of the node's setup: a node that
// was just started may not run it yet. The power command is done by then,
// so failures are warnings (meshMgr.Warn).
func addMissingHostKeys(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, containers []docker.ContainerName) {
	if len(containers) == 0 {
		return
	}
	realm := meshMgr.Realm
	prefix := ContainerPrefix(realm, clusterName)
	byHost := make(map[string]docker.ContainerName, len(containers))
	hostnames := make([]string, len(containers))
	for i, c := range containers {
		hostnames[i] = DNSName(strings.TrimPrefix(string(c), prefix), clusterName, realm)
		byHost[hostnames[i]] = c
	}
	warn := func(err error) {
		meshMgr.Warn(ctx, fmt.Sprintf("not adding the host keys that known_hosts lacks: %v", err))
	}
	missing, err := meshMgr.MissingKnownHosts(ctx, hostnames)
	if err != nil {
		warn(err)
		return
	}
	if len(missing) == 0 {
		return
	}
	// A node whose key cannot be read does not keep the others out.
	keys := make([]string, len(missing))
	errs := make([]error, len(missing))
	var g errgroup.Group
	g.SetLimit(powerConcurrency)
	for i, h := range missing {
		g.Go(func() error {
			out, err := client.ReadFile(ctx, byHost[h], ssh.HostKeyFile)
			if err == nil {
				keys[i], err = ssh.HostKeyFromFile(out)
			}
			if err != nil {
				errs[i] = fmt.Errorf("reading the host key of %s: %w", byHost[h], err)
			}
			return nil
		})
	}
	_ = g.Wait() // the goroutines report through errs
	var entries []mesh.KnownHostEntry
	for i, h := range missing {
		if errs[i] == nil {
			entries = append(entries, mesh.KnownHostEntry{Hostname: h, HostKey: keys[i]})
		}
	}
	if len(entries) > 0 {
		sindlog.From(ctx).InfoContext(ctx, "adding host keys that known_hosts lacks", "nodes", len(entries))
		if err := meshMgr.AddKnownHosts(ctx, entries); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		warn(err)
	}
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
		Describe: func(t powerTarget) fanout.ItemInfo {
			return fanout.ItemInfo{Node: t.node, Role: t.role}
		},
		Summarize: joinFailures(func(f fanout.FailedItem) error {
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

	entries, err := existingClusterContainers(ctx, client, realm, clusterName)
	if err != nil {
		return nil, err
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

// CheckNode fails with an error that wraps ErrClusterNotFound for a cluster
// that has no containers in the realm, and ErrNodeNotFound for a node the
// cluster does not hold. The commands that hand a node to docker, logs and
// ssh, call it first: docker's own error would name an internal container.
func CheckNode(ctx context.Context, client *docker.Client, realm, clusterName, shortName string) error {
	_, err := resolveTargets(ctx, client, realm, clusterName, []string{shortName})
	return err
}
