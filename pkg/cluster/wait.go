// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"golang.org/x/sync/errgroup"
)

// WaitNodes waits until nodes of a cluster that exist, by their short
// names, each once, are ready, as Create and WorkerAdd wait for the nodes
// they make: each node's container runs, systemd has booted, and the
// node's services are ready (sshd, munge but with identity clientIds, and
// where sind manages Slurm the node's Slurm daemon, see nodeServices);
// then slurmctld has the registration of each managed worker among nodes,
// as sinfo on the controller in control tells (probe.WorkersRegistered).
// The power commands that start nodes call it with --wait.
//
// wait bounds the whole wait; zero or less sets no limit. When the limit
// ends it, the error wraps ErrNotReady. A container that exits, or a unit
// that fails, ends the wait at once, as in Create. Managed workers need a
// running controller to register with: without one the wait fails at
// once. interval is the delay between probe rounds, as for Create.
//
// The wait is the progress step "readiness", with a target for each node
// and, after them, the wait for the workers' registration.
func WaitNodes(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string, wait, interval time.Duration) (err error) {
	if len(shortNames) == 0 {
		return nil
	}
	containers, err := existingClusterContainers(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}
	byName := make(map[docker.ContainerName]docker.ContainerListEntry, len(containers))
	for _, c := range containers {
		byName[c.Name] = c
	}
	shortNames = uniqueNames(shortNames)
	nodes := make([]docker.ContainerListEntry, len(shortNames))
	var workers []string
	for i, name := range shortNames {
		c, ok := byName[ContainerName(realm, clusterName, name)]
		if !ok {
			return errorWith(ErrNodeNotFound, "node %q not found in cluster %q", name, clusterName)
		}
		nodes[i] = c
		if managedWorkerLabels(c.Labels) {
			workers = append(workers, name)
		}
	}
	var controller docker.ContainerListEntry
	if len(workers) > 0 {
		var ok bool
		controller, ok = findController(containers, realm, clusterName)
		if !ok || controller.State != docker.StateRunning {
			return fmt.Errorf("no controller of cluster %q runs: the workers %s cannot register with slurmctld", clusterName, strings.Join(workers, ", "))
		}
	}

	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, wait, errWaitExpired)
		defer cancel()
	}
	defer func() {
		if err != nil && errors.Is(context.Cause(ctx), errWaitExpired) {
			err = fmt.Errorf("nodes of cluster %q %w within %s: %w", clusterName, ErrNotReady, wait, err)
		}
	}()

	sctx, end := startStep(ctx, "readiness", progress.Flagged(progress.FlagFold), progress.Total(len(nodes)))
	defer func() { end(err) }()
	g, gctx := errgroup.WithContext(sctx)
	ctxs, targets := startTargets(gctx, shortNames, func(i int) string { return nodes[i].Labels[LabelRole] })
	for i, c := range nodes {
		g.Go(func() (err error) {
			targets[i].Run()
			defer func() { endSpan(gctx, targets[i], err) }()
			probes := nodeProbes(config.Role(c.Labels[LabelRole]), c.Labels)
			if err := probe.UntilReady(ctxs[i], client, c.Name, probes, interval); err != nil {
				return fmt.Errorf("node %s: %w", shortNames[i], err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return waitRegistered(sctx, client, controller.Name, workers, interval)
}

// managedWorkerLabels reports whether the labels are those of a worker on
// which sind manages slurmd.
func managedWorkerLabels(labels docker.Labels) bool {
	return config.Role(labels[LabelRole]) == config.RoleWorker && IsManaged(labels)
}

// nodeProbes returns the readiness probes of a node that exists, from its
// role and labels: its container and systemd, then the services that sind
// checks on it (nodeServices) and waits for.
func nodeProbes(role config.Role, labels docker.Labels) []probe.Probe {
	probes := []probe.Probe{
		{Name: "container", Check: probe.ContainerRunning},
		{Name: "systemd", Check: probe.SystemdReady},
	}
	for _, svc := range nodeServices(role, labels) {
		if p := probe.ForService(svc); p.Check != nil {
			probes = append(probes, p)
		}
	}
	return probes
}
