// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/go-clikit/fanout"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/retry"
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
//	deleteClusterResources   deregisters from the mesh only if other clusters remain
//	      │
//	    yes → done
//	    no  → CleanupMesh
//
// Whether other clusters remain does not depend on this cluster's
// containers, so Delete settles it first: when the mesh goes too, removing
// the nodes' DNS records and known_hosts entries would be wasted work.
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
// (fanout.Map).
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
		return err
	}

	log.InfoContext(ctx, "all clusters deleted, cleaning up mesh")
	return meshMgr.CleanupMesh(ctx)
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
		if name := c.Labels[LabelCluster]; name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
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
// with deregister first the nodes' DNS records and known_hosts entries.
func removeClusterResources(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, res *Resources, deregister bool) error {
	log := sindlog.From(ctx)

	// Remove DNS records and known_hosts entries before deleting containers.
	// Failures are logged inside DeregisterMesh; the teardown continues.
	if deregister {
		_ = DeregisterMesh(ctx, meshMgr, clusterName, res.Containers)
	}

	log.DebugContext(ctx, "removing containers", "count", len(res.Containers))
	if err := DeleteContainers(ctx, client, res.Containers); err != nil {
		return err
	}

	if res.NetworkExists {
		_ = client.DisconnectNetwork(ctx, res.Network, meshMgr.SSHContainerName())
		log.DebugContext(ctx, "removing network", "name", string(res.Network))
		if err := DeleteNetwork(ctx, client, res.Network); err != nil {
			return err
		}
	}

	log.DebugContext(ctx, "removing volumes", "count", len(res.Volumes))
	if err := DeleteVolumes(ctx, client, res.Volumes); err != nil {
		return err
	}

	return nil
}

// DeleteContainers force-removes the given containers in parallel
// (docker rm -f). A container that is already gone is logged and treated as
// success — the caller asked for the resource removed, not for a particular
// state transition. Like DeleteVolumes, it tries every container even when
// one fails, so that one failure does not cancel the removals still waiting
// for a docker call slot, and returns every failure.
func DeleteContainers(ctx context.Context, client *docker.Client, containers []docker.ContainerListEntry) error {
	log := sindlog.From(ctx)
	errs := make([]error, len(containers))
	var wg sync.WaitGroup
	for i, c := range containers {
		wg.Go(func() {
			err := client.RemoveContainer(ctx, c.Name)
			switch {
			case err == nil:
			case docker.IsNotFound(err):
				log.WarnContext(ctx, "container already gone, skipping", "name", string(c.Name))
			default:
				errs[i] = fmt.Errorf("removing container %s: %w", c.Name, err)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// DeleteNetwork removes the cluster network. A network that is already gone
// is logged and treated as success.
func DeleteNetwork(ctx context.Context, client *docker.Client, name docker.NetworkName) error {
	err := client.RemoveNetwork(ctx, name)
	if err == nil {
		return nil
	}
	if docker.IsNotFound(err) {
		sindlog.From(ctx).WarnContext(ctx, "network already gone, skipping",
			"name", string(name))
		return nil
	}
	return fmt.Errorf("removing network %s: %w", name, err)
}

// DeleteVolumes removes the given cluster volumes in parallel. Each removal
// retries past the dockerd async-cleanup race that follows `docker rm -f`.
// A volume that is already gone is logged and treated as success. Every
// volume is attempted; the errors of those that could not be removed are
// joined in argument order.
func DeleteVolumes(ctx context.Context, client *docker.Client, volumes []docker.VolumeName) error {
	log := sindlog.From(ctx)
	errs := make([]error, len(volumes))
	var wg sync.WaitGroup
	for i, v := range volumes {
		wg.Go(func() {
			err := retry.Do(ctx,
				func() error { return client.RemoveVolume(ctx, v) },
				docker.IsVolumeInUse,
				6, 100*time.Millisecond)
			switch {
			case err == nil:
			case docker.IsNotFound(err):
				log.WarnContext(ctx, "volume already gone, skipping", "name", string(v))
			default:
				errs[i] = fmt.Errorf("removing volume %s: %w", v, err)
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// DeregisterMesh removes DNS records and known_hosts entries for all
// containers in batch, starting a stopped mesh first (see
// deregisterHostnames). This is the inverse of registerMesh during cluster
// creation. Failures are logged and swallowed: the cluster is being torn
// down, and stale entries in a mesh helper that's already unreachable will
// be overwritten on next register or cleared when the mesh itself is torn
// down.
func DeregisterMesh(ctx context.Context, meshMgr *mesh.Manager, clusterName string, containers []docker.ContainerListEntry) error {
	deregisterHostnames(ctx, meshMgr, meshHostnames(meshMgr.Realm, clusterName, containers))
	return nil
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
// given mesh host names, logging failures. It first starts the realm's DNS
// container and SSH relay if they are stopped, as after a host reboot (see
// mesh.Manager.StartMesh): known_hosts is read and written with docker
// exec in the relay, which needs it running. The Corefile and known_hosts
// live in different containers, so the two updates then run in parallel;
// each stays a single read-modify-write.
func deregisterHostnames(ctx context.Context, meshMgr *mesh.Manager, hostnames []string) {
	if len(hostnames) == 0 {
		return
	}
	log := sindlog.From(ctx)
	if _, _, err := meshMgr.StartMesh(ctx); err != nil {
		log.WarnContext(ctx, "starting the mesh failed, continuing", "error", err)
	}
	var g errgroup.Group
	g.Go(func() error {
		if err := meshMgr.RemoveDNSRecords(ctx, hostnames); err != nil {
			log.WarnContext(ctx, "removing DNS records failed, continuing", "error", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := meshMgr.RemoveKnownHosts(ctx, hostnames); err != nil {
			log.WarnContext(ctx, "removing known_hosts entries failed, continuing", "error", err)
		}
		return nil
	})
	_ = g.Wait() // failures are logged
}
