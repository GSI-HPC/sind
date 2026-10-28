// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
)

// External containers are containers outside sind, such as kind nodes,
// that join a realm's networks to reach its nodes: the mesh, for the DNS
// server, and every cluster network, for the addresses DNS answers with.
// Joining the mesh is what makes a container one of the realm's external
// containers, so a cluster created later is connected to them too, and
// deleting the realm's networks detaches them. Deleting the realm's last
// cluster removes the mesh, and with it the record of them (see Delete).

// ownContainer reports whether a container with these labels is sind's:
// a node or mesh container, which carry the realm label, or a container of
// a realm's monitoring stack, which joins the mesh's Compose project.
func ownContainer(labels docker.Labels, meshMgr *mesh.Manager) bool {
	return labels[LabelRealm] != "" || labels[docker.ComposeProjectLabel] == meshMgr.ComposeProject()
}

// ExternalContainers returns the containers outside sind attached to the
// realm's mesh network, sorted. A realm without mesh has none.
func ExternalContainers(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) ([]docker.ContainerName, error) {
	entries, err := client.ListContainers(ctx, "network="+string(meshMgr.NetworkName()))
	if err != nil {
		return nil, fmt.Errorf("listing containers on the mesh: %w", err)
	}
	var names []docker.ContainerName
	for _, e := range entries {
		if !ownContainer(e.Labels, meshMgr) {
			names = append(names, e.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// ConnectableContainers returns the containers outside sind that are not
// attached to the realm's mesh network, sorted: those Connect can attach.
func ConnectableContainers(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) ([]docker.ContainerName, error) {
	entries, err := Connectable(ctx, client, meshMgr)
	var names []docker.ContainerName
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names, err
}

// Connectable returns what docker ps says of the containers that
// ConnectableContainers names, sorted by name, running or not.
func Connectable(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) ([]docker.ContainerListEntry, error) {
	entries, err := client.ListContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	var out []docker.ContainerListEntry
	for _, e := range entries {
		if !ownContainer(e.Labels, meshMgr) && !slices.Contains(e.Networks, meshMgr.NetworkName()) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b docker.ContainerListEntry) int { return strings.Compare(string(a.Name), string(b.Name)) })
	return out, nil
}

// realmNetworks returns the realm's mesh and cluster networks, the mesh
// first, then the cluster networks sorted. It leaves out the realm lock.
func realmNetworks(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) ([]docker.NetworkName, error) {
	entries, err := client.ListNetworks(ctx, "label="+LabelRealm+"="+meshMgr.Realm)
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	meshNet := meshMgr.NetworkName()
	lock := state.LockNetworkName(meshMgr.Realm)
	hasMesh := false
	var clusterNets []docker.NetworkName
	for _, e := range entries {
		switch e.Name {
		case meshNet:
			hasMesh = true
		case lock:
		default:
			clusterNets = append(clusterNets, e.Name)
		}
	}
	slices.Sort(clusterNets)
	if !hasMesh {
		return clusterNets, nil
	}
	return append([]docker.NetworkName{meshNet}, clusterNets...), nil
}

// inspectExternal inspects the named containers and fails for one that does
// not exist or is sind's own.
func inspectExternal(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, names []docker.ContainerName) ([]*docker.ContainerInfo, error) {
	infos, err := client.InspectContainers(ctx, names...)
	if docker.IsNotFound(err) {
		return nil, missingContainer(ctx, client, names, err)
	}
	if err != nil {
		return nil, fmt.Errorf("inspecting containers: %w", err)
	}
	for _, info := range infos {
		if ownContainer(info.Labels, meshMgr) {
			return nil, fmt.Errorf("%s is a sind container", info.Name)
		}
	}
	return infos, nil
}

// missingContainer names the first of names that is not a container, for
// the error of an inspect that failed with "no such object": docker's own
// error names it, but in its own words. If every one exists by now, the
// inspect error stands.
func missingContainer(ctx context.Context, client *docker.Client, names []docker.ContainerName, inspectErr error) error {
	for _, name := range names {
		if _, err := client.InspectContainer(ctx, name); docker.IsNotFound(err) {
			return fmt.Errorf("container %q not found", name)
		}
	}
	return fmt.Errorf("inspecting containers: %w", inspectErr)
}

// Connect attaches containers outside sind to the realm's mesh network and
// every cluster network, skipping the ones they already join. Clusters
// created later, while the realm has a cluster, are connected to them as
// well (ConnectExternal). It fails
// for a realm without mesh, and for a container that does not exist or is
// sind's own.
//
// The attaching is the progress step "connecting", with a target for each
// container, named by it and announced queued before the first runs; one
// that joins every network already is skipped as "connected". The first
// container that fails ends the step, and the targets not yet tried end
// canceled with it.
func Connect(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, names ...docker.ContainerName) error {
	networks, err := realmNetworks(ctx, client, meshMgr)
	if err != nil {
		return err
	}
	if len(networks) == 0 || networks[0] != meshMgr.NetworkName() {
		return fmt.Errorf("no mesh found for realm %q", meshMgr.Realm)
	}
	infos, err := inspectExternal(ctx, client, meshMgr, names)
	if err != nil {
		return err
	}
	return eachExternal(ctx, "connecting", "connected", infos, func(ctx context.Context, info *docker.ContainerInfo) (bool, error) {
		changed := false
		for _, net := range networks {
			if _, ok := info.IPs[net]; ok {
				continue
			}
			if err := client.ConnectNetwork(ctx, net, info.Name); err != nil {
				return changed, fmt.Errorf("connecting %s to %s: %w", info.Name, net, err)
			}
			changed = true
		}
		return changed, nil
	})
}

// Disconnect detaches containers outside sind from every network of the
// realm they join. A container that joins none is left as it is. It fails
// for a container that does not exist or is sind's own.
//
// The detaching is the progress step "disconnecting", reported as the step
// of Connect is, with a container that joins no network skipped as "not
// connected".
func Disconnect(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, names ...docker.ContainerName) error {
	networks, err := realmNetworks(ctx, client, meshMgr)
	if err != nil {
		return err
	}
	infos, err := inspectExternal(ctx, client, meshMgr, names)
	if err != nil {
		return err
	}
	return eachExternal(ctx, "disconnecting", "not connected", infos, func(ctx context.Context, info *docker.ContainerInfo) (bool, error) {
		changed := false
		// The cluster networks first: the container leaves the mesh, and
		// with it the realm's external containers, last.
		for _, net := range slices.Backward(networks) {
			if _, ok := info.IPs[net]; !ok {
				continue
			}
			changed = true
			if err := docker.IgnoreNotFound(client.DisconnectNetwork(ctx, net, info.Name)); err != nil {
				return changed, fmt.Errorf("disconnecting %s from %s: %w", info.Name, net, err)
			}
		}
		return changed, nil
	})
}

// eachExternal applies work to the containers in turn as the progress step
// step, with a target for each container, all announced before the first
// runs. A container whose work changed nothing is skipped for reason. The
// first failure ends the step with its error, and the targets not yet tried
// end canceled with the step.
func eachExternal(ctx context.Context, step, reason string, infos []*docker.ContainerInfo,
	work func(ctx context.Context, info *docker.ContainerInfo) (changed bool, err error)) (err error) {
	ctx, end := startStep(ctx, step, progress.Flagged(progress.FlagFold), progress.Total(len(infos)))
	defer func() { end(err) }()
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = string(info.Name)
	}
	ctxs, targets := startTargets(ctx, names, func(int) string { return "" })
	for i, info := range infos {
		targets[i].Run()
		changed, err := work(ctxs[i], info)
		switch {
		case err != nil:
			endSpan(ctx, targets[i], err)
			return err
		case changed:
			targets[i].End(nil)
		default:
			targets[i].Skip(reason)
		}
	}
	return nil
}

// ConnectExternal connects the realm's external containers to a new
// cluster network. It is best-effort: a container that cannot be connected
// is a warning (meshMgr.Warn), and does not fail the create.
func ConnectExternal(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, network docker.NetworkName) {
	names, err := ExternalContainers(ctx, client, meshMgr)
	if err != nil {
		meshMgr.Warn(ctx, fmt.Sprintf("not connecting external containers to %s: %v", network, err))
		return
	}
	for _, name := range names {
		if err := client.ConnectNetwork(ctx, network, name); err != nil {
			meshMgr.Warn(ctx, fmt.Sprintf("not connecting external container %s to %s: %v", name, network, err))
		}
	}
}
