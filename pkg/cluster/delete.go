// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/GSI-HPC/go-clikit/fanout"
	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"golang.org/x/sync/errgroup"
)

// Delete orchestrates the full cluster deletion flow.
//
// The caller holds the realm lock (state.LockRealm) until Delete returns.
//
// Deleting a non-existent cluster is not an error. The function handles
// partial clusters (e.g., from a failed creation) by removing whatever
// resources exist.
//
//	HasOtherClusters?
//	      │
//	deleteClusterResources   deregisters from the mesh and the monitoring stack only if other clusters remain
//	      │
//	    yes → done
//	    no  → cleanupRealm (monitoring stack containers → mesh → stack volumes)
//
// Whether other clusters remain does not depend on this cluster's
// containers, so Delete settles it first: when the mesh goes too, removing
// the nodes' DNS records and known_hosts entries would be wasted work.
//
// The mesh is what makes a container one of the realm's external
// containers (see ExternalContainers). Removing it detaches them, so a
// cluster created later is not connected to them: Delete then names them
// through meshMgr.Warn.
func Delete(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	log.InfoContext(ctx, "deleting cluster", "name", clusterName)

	hasOther, err := HasOtherClusters(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	if err := deleteClusterResources(ctx, client, meshMgr, clusterName, hasOther); err != nil {
		return err
	}

	// Clean up mesh infrastructure if this was the last cluster.
	if hasOther {
		return nil
	}
	log.InfoContext(ctx, "last cluster deleted, cleaning up mesh")
	external, err := ExternalContainers(ctx, client, meshMgr)
	if err != nil {
		log.WarnContext(ctx, "listing external containers failed, continuing", "error", err)
	}
	if err := cleanupRealm(ctx, client, meshMgr); err != nil {
		return err
	}
	warnDetached(ctx, meshMgr, external)
	return nil
}

// warnDetached tells the user, through meshMgr.Warn, that deleting the
// realm's last cluster detached its external containers, which sind then no
// longer knows: it keeps no list of them, only the mesh.
func warnDetached(ctx context.Context, meshMgr *mesh.Manager, external []docker.ContainerName) {
	if len(external) == 0 {
		return
	}
	names := make([]string, len(external))
	for i, n := range external {
		names[i] = string(n)
	}
	list := strings.Join(names, " ")
	meshMgr.Warn(ctx, fmt.Sprintf("deleting the last cluster of realm %s detached %s from its networks: "+
		"a cluster created later is not connected to them; run sind connect container %s after creating it",
		meshMgr.Realm, strings.Join(names, ", "), list))
}

// cleanupRealm removes a realm's shared resources once its last cluster is
// gone: the monitoring stack's containers, which hold endpoints on the mesh
// network, then the mesh, then the stack's volumes and the journal volume,
// which Docker removes only once no container references them.
//
// The parts are the progress steps "monitoring stack", "mesh" and
// "monitoring volumes". The two of the stack are hidden, since a realm
// without a monitoring stack has nothing for them to do.
func cleanupRealm(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
	if err := removeMonitoringContainers(ctx, client, meshMgr.Realm); err != nil {
		return err
	}
	if err := removeMesh(ctx, meshMgr); err != nil {
		return err
	}
	return removeMonitoringVolumes(ctx, client, meshMgr.Realm)
}

// removeMesh removes the realm's mesh (mesh.Manager.CleanupMesh), as the
// progress step "mesh".
func removeMesh(ctx context.Context, meshMgr *mesh.Manager) (err error) {
	ctx, end := startStep(ctx, "mesh")
	defer func() { end(err) }()
	return meshMgr.CleanupMesh(ctx)
}

// deleteAllConcurrency bounds how many clusters DeleteAll deletes at once.
const deleteAllConcurrency = 4

// DeleteAll deletes every cluster of the realm, in parallel, and then the
// realm's mesh. It finds the clusters by the labels of their containers,
// networks and volumes, and removes the mesh even when it finds no cluster:
// a create that was killed can leave a mesh behind without one.
//
// A cluster that fails to delete does not stop the others. The mesh then
// stays for what is left, the clusters that were deleted are deregistered
// from it, and DeleteAll returns the failures, joined. Once ctx has ended
// no further cluster is tried, and those left out fail with the context's
// error, worded as "deleting cluster <name>: listing containers: <err>",
// as when they were tried and failed at their first lookup; a panic while
// deleting a cluster becomes that cluster's error.
//
// The clusters are the progress step "clusters", with a target for each
// (fanout.Map), under which each cluster's removal reports its steps as
// Delete does; the realm's the steps "monitoring stack", "mesh" and
// "monitoring volumes".
func DeleteAll(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	names, err := realmClusterNames(ctx, client, realm)
	if err != nil {
		return err
	}

	// started says which clusters Map started on; it leaves out those it
	// had not started once ctx has ended. Each is written by the worker of
	// its cluster, and read once Map has every worker's result.
	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}
	started := make([]bool, len(names))

	// The mesh goes too, so the clusters skip mesh deregistration, and
	// with it the read-modify-write of the realm's Corefile and
	// known_hosts that must not run concurrently.
	deleted, err := fanout.Map(ctx, names, fanout.MapOptions[string]{
		Step:     "clusters",
		Limit:    deleteAllConcurrency,
		Program:  program,
		PanicLog: panicLog(ctx),
		Summarize: joinFailures(func(f fanout.Failed) error {
			if !started[index[f.Name]] {
				// Worded as when every cluster was tried: one tried once
				// ctx had ended failed at its first lookup.
				return fmt.Errorf("deleting cluster %s: listing containers: %w", f.Name, f.Err)
			}
			return fmt.Errorf("deleting cluster %s: %w", f.Name, f.Err)
		}),
	}, func(ctx context.Context, name string) (*Resources, error) {
		started[index[name]] = true
		ctx = withoutTargets(ctx)
		log.InfoContext(ctx, "deleting cluster", "name", name)
		res, err := ListClusterResources(ctx, client, realm, name)
		if err != nil {
			return nil, err
		}
		return res, removeClusterResources(ctx, client, meshMgr, name, res, false)
	})
	if err != nil {
		var hostnames []string
		for i, d := range deleted {
			if d.Err == nil {
				hostnames = append(hostnames, meshHostnames(realm, names[i], d.Value.Containers)...)
			}
		}
		deregisterHostnames(ctx, meshMgr, hostnames)
		// The clusters went in parallel, without a sync each: one sync
		// takes the deleted ones out of the monitoring stack, if any.
		forgetMonitoredNodes(ctx, client, meshMgr, "")
		return err
	}

	log.InfoContext(ctx, "all clusters deleted, cleaning up mesh")
	return cleanupRealm(ctx, client, meshMgr)
}

// realmClusterNames returns the names of the realm's clusters, sorted:
// those DiscoverClusterNames finds from networks and volumes, and those
// whose containers carry a cluster label. sind before v0.9.0 labelled only
// the containers.
func realmClusterNames(ctx context.Context, client *docker.Client, realm string) ([]string, error) {
	names, err := DiscoverClusterNames(ctx, client, realm)
	if err != nil {
		return nil, err
	}
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	for _, c := range containers {
		names = append(names, c.Labels[LabelCluster])
	}
	return sortedClusterNames(names), nil
}

// deleteClusterResources removes containers, network, and volumes for a
// cluster. With deregister it also removes the nodes' DNS records and
// known_hosts entries; callers that remove the mesh next skip that. Does NOT
// clean up mesh infrastructure — that decision belongs to the caller.
//
// Removing a non-existent cluster is not an error.
//
//	ListClusterResources
//	      │
//	DeregisterMesh        start mesh → DNS ║ known_hosts (with deregister)
//	      │
//	DeleteContainers      stop + rm per container
//	      │
//	DeleteNetwork         rm cluster network
//	      │
//	DeleteVolumes         rm cluster volumes
func deleteClusterResources(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, deregister bool) error {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	res, err := ListClusterResources(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	// Nothing to delete.
	if len(res.Containers) == 0 && !res.NetworkExists && len(res.Volumes) == 0 {
		log.DebugContext(ctx, "no resources found, nothing to delete")
		return nil
	}

	return removeClusterResources(ctx, client, meshMgr, clusterName, res, deregister)
}

// removeClusterResources removes the cluster resources that res lists, and
// with deregister first the nodes' DNS records and known_hosts entries, and
// last, for a monitored cluster, its nodes from the realm's monitoring
// stack and their journals (see forgetMonitoredNodes).
func removeClusterResources(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, res *Resources, deregister bool) error {
	log := sindlog.From(ctx)

	// Remove DNS records and known_hosts entries before deleting containers.
	// Failures are logged inside DeregisterMesh; the teardown continues.
	// The realm's monitoring stack stays too: whether it collects the
	// cluster is told by its containers, so before they go.
	monitored := false
	if deregister {
		DeregisterMesh(ctx, meshMgr, clusterName, res.Containers)
		var err error
		if monitored, err = clusterMonitored(ctx, client, meshMgr.Realm, clusterName); err != nil {
			return err
		}
	}

	log.DebugContext(ctx, "removing containers", "count", len(res.Containers))
	if err := DeleteContainers(ctx, client, res.Containers); err != nil {
		return err
	}

	if res.NetworkExists {
		if err := removeClusterNetwork(ctx, client, res.Network); err != nil {
			return err
		}
	}

	log.DebugContext(ctx, "removing volumes", "count", len(res.Volumes))
	if err := DeleteVolumes(ctx, client, res.Volumes); err != nil {
		return err
	}

	if monitored {
		forgetMonitoredNodes(ctx, client, meshMgr, clusterName)
	}
	return nil
}

// DeleteContainers force-removes the given containers in parallel
// (docker rm -f). A container that is already gone is logged and treated as
// success — the caller asked for the resource removed, not for a particular
// state transition. Like DeleteVolumes, it tries every container even when
// one fails, so that one failure does not cancel the removals still waiting
// for a docker call slot, and returns every failure.
//
// The removals are the progress step "nodes", with a target for each
// container, named by its node's short name and with the node's role (see
// removeEach). Without containers there is no step.
func DeleteContainers(ctx context.Context, client *docker.Client, containers []docker.ContainerListEntry) error {
	removals := make([]removal, len(containers))
	for i, c := range containers {
		removals[i] = removal{
			name: string(c.Name),
			node: nodeOf(c),
			role: c.Labels[LabelRole],
			run:  func(ctx context.Context) error { return client.RemoveContainer(ctx, c.Name) },
		}
	}
	return removeEach(ctx, "nodes", "container", removals)
}

// nodeOf returns the short name of the node whose container c is: the name
// without its realm and cluster prefix, or the whole name for a container
// that does not carry it.
func nodeOf(c docker.ContainerListEntry) string {
	return strings.TrimPrefix(string(c.Name), ContainerPrefix(c.Labels[LabelRealm], c.Labels[LabelCluster]))
}

// DeleteNetwork removes the cluster network. A network that is already gone
// is logged and treated as success.
func DeleteNetwork(ctx context.Context, client *docker.Client, name docker.NetworkName) error {
	return removeOne(ctx, "network", string(name), func(ctx context.Context) error { return client.RemoveNetwork(ctx, name) })
}

// removeClusterNetwork detaches every container from the cluster network
// and removes it, as the progress step "network".
func removeClusterNetwork(ctx context.Context, client *docker.Client, network docker.NetworkName) (err error) {
	ctx, end := startStep(ctx, "network")
	defer func() { end(err) }()
	// The mesh's SSH relay joins every cluster network, and containers
	// outside sind, such as kind nodes, may have been attached to it.
	if err := client.DisconnectAll(ctx, network); err != nil {
		return fmt.Errorf("detaching containers from network %s: %w", network, err)
	}
	sindlog.From(ctx).DebugContext(ctx, "removing network", "name", string(network))
	return DeleteNetwork(ctx, client, network)
}

// DeleteVolumes removes the given cluster volumes in parallel. Each removal
// retries past the dockerd async-cleanup race that follows `docker rm -f`.
// A volume that is already gone is logged and treated as success. Every
// volume is attempted; the errors of those that could not be removed are
// joined in argument order.
//
// The removals are the progress step "volumes", with a target for each
// volume, named by it (see removeEach). Without volumes there is no step.
func DeleteVolumes(ctx context.Context, client *docker.Client, volumes []docker.VolumeName) error {
	removals := make([]removal, len(volumes))
	for i, v := range volumes {
		removals[i] = removal{
			name: string(v),
			node: string(v),
			run:  func(ctx context.Context) error { return client.RemoveVolume(ctx, v) },
		}
	}
	return removeEach(ctx, "volumes", "volume", removals)
}

// removal is the removal of one resource: its name, what its progress
// target is named by and the role of its node, if it is one, and the docker
// command that removes it.
type removal struct {
	name, node, role string
	run              func(context.Context) error
}

// removeOne runs the removal of the resource name of a kind. A resource
// that is already gone is logged and treated as success.
func removeOne(ctx context.Context, kind, name string, remove func(context.Context) error) error {
	err := remove(ctx)
	switch {
	case err == nil:
	case docker.IsNotFound(err):
		sindlog.From(ctx).WarnContext(ctx, kind+" already gone, skipping", "name", name)
	default:
		return fmt.Errorf("removing %s %s: %w", kind, name, err)
	}
	return nil
}

// removeEach runs the removals of resources of a kind in parallel, all at
// once, as the progress step step with a target for each, all announced
// before the first runs. A resource that is already gone is logged and
// treated as success; the errors of the others are joined in the order of
// the removals, as "removing <kind> <name>: <error>". A removal stopped by
// the end of ctx ends its target canceled, and the step ends canceled when
// every one that failed was stopped, as the pools of fanout do. With no
// removals there is no step.
//
// Below a target of DeleteAll the step has no targets of its own (see
// withoutTargets).
func removeEach(ctx context.Context, step, kind string, removals []removal) (err error) {
	if len(removals) == 0 {
		return nil
	}
	targeted := hasTargets(ctx)
	var opts []progress.Option
	if targeted {
		opts = append(opts, progress.WithFlags(progress.Fold), progress.Total(len(removals)))
	}
	ctx, end := startStep(ctx, step, opts...)
	defer func() { end(err) }()

	ctxs := make([]context.Context, len(removals))
	spans := make([]*progress.Span, len(removals))
	for i, r := range removals {
		ctxs[i] = ctx
		if targeted {
			ctxs[i], spans[i] = progress.Start(ctx, progress.KindTarget, r.node,
				progress.Queued(), progress.Node(r.node), progress.Role(r.role))
		}
	}
	errs := make([]error, len(removals))
	var wg sync.WaitGroup
	for i, r := range removals {
		wg.Go(func() {
			spans[i].Run()
			errs[i] = stoppedBy(ctx, removeOne(ctxs[i], kind, r.name, r.run))
			spans[i].End(errs[i])
		})
	}
	wg.Wait()
	return failures(errs)
}

// failures joins the errors of work that was done for several items, the
// non-nil ones of errs in order, as joinFailures joins those of a pool: nil
// when there are none; of the class canceled when every one was a stop
// (stoppedBy), and of the target's otherwise.
func failures(errs []error) error {
	var failed []error
	class := progress.ClassCanceled
	for _, err := range errs {
		if err == nil {
			continue
		}
		failed = append(failed, err)
		if !errors.As(err, &stopped{}) {
			class = progress.ClassTarget
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return poolFailure{error: errors.Join(failed...), class: class}
}

// DeregisterMesh removes DNS records and known_hosts entries for all
// containers in batch, starting a stopped mesh first (see
// deregisterHostnames). This is the inverse of registerNodes during cluster
// creation. Failures are warnings, not errors: the cluster is being torn
// down, and stale entries in a mesh helper that's already unreachable will
// be overwritten on next register or cleared when the mesh itself is torn
// down.
func DeregisterMesh(ctx context.Context, meshMgr *mesh.Manager, clusterName string, containers []docker.ContainerListEntry) {
	deregisterHostnames(ctx, meshMgr, meshHostnames(meshMgr.Realm, clusterName, containers))
}

// meshHostnames returns the mesh DNS names of a cluster's node containers.
func meshHostnames(realm, clusterName string, containers []docker.ContainerListEntry) []string {
	prefix := ContainerPrefix(realm, clusterName)
	hostnames := make([]string, len(containers))
	for i, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		hostnames[i] = DNSName(shortName, clusterName, realm)
	}
	return hostnames
}

// deregisterHostnames removes the DNS records and known_hosts entries of the
// given mesh host names; a failure is a warning (meshMgr.Warn). It first
// starts the realm's DNS container and SSH relay if they are stopped, as
// after a host reboot (see
// mesh.Manager.StartMesh): known_hosts is read and written with docker
// exec in the relay, which needs it running. The Corefile and known_hosts
// live in different containers, so the two updates then run in parallel;
// each stays a single read-modify-write.
//
// It is the progress step "mesh deregistration", which ends ok whatever
// the warnings say; with no hostnames there is no step.
func deregisterHostnames(ctx context.Context, meshMgr *mesh.Manager, hostnames []string) {
	if len(hostnames) == 0 {
		return
	}
	ctx, end := startStep(ctx, "mesh deregistration")
	defer end(nil) // failures are warnings
	if _, _, err := meshMgr.StartMesh(ctx); err != nil {
		meshMgr.Warn(ctx, fmt.Sprintf("starting the mesh to remove the deleted nodes from it: %v", err))
	}
	var g errgroup.Group
	g.Go(func() error {
		if err := meshMgr.RemoveDNSRecords(ctx, hostnames); err != nil {
			meshMgr.Warn(ctx, fmt.Sprintf("not removing the deleted nodes from the mesh DNS: %v", err))
		}
		return nil
	})
	g.Go(func() error {
		if err := meshMgr.RemoveKnownHosts(ctx, hostnames); err != nil {
			meshMgr.Warn(ctx, fmt.Sprintf("not removing the deleted nodes from known_hosts: %v", err))
		}
		return nil
	})
	_ = g.Wait() // failures are warnings
}
