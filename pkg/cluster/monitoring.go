// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"golang.org/x/sync/errgroup"
)

// Monitored reports whether a node container mounts the realm's journal
// volume: the node is collected by the realm's monitoring stack, and on
// a controller the whole cluster is monitored. The sind.monitoring label
// does not decide, as an image could carry it; an image cannot forge a
// mount.
func Monitored(realm string, info *docker.ContainerInfo) bool {
	journal := observability.JournalVolume(realm)
	return slices.ContainsFunc(info.Mounts, func(m docker.Mount) bool {
		return m.Type == docker.MountVolume && m.Name == journal
	})
}

// monitoredContainers lists the realm's node containers that mount its
// journal volume, of one cluster when cluster is not empty. Helpers that
// mount the volume carry no role, and are left out.
func monitoredContainers(ctx context.Context, client *docker.Client, realm, cluster string) ([]docker.ContainerListEntry, error) {
	filters := []string{
		"label=" + LabelRealm + "=" + realm,
		"label=" + LabelRole,
		"volume=" + string(observability.JournalVolume(realm)),
	}
	if cluster != "" {
		filters = append(filters, "label="+LabelCluster+"="+cluster)
	}
	containers, err := client.ListContainers(ctx, filters...)
	if err != nil {
		return nil, fmt.Errorf("listing monitored nodes: %w", err)
	}
	return containers, nil
}

// monitoringTargets returns the realm's monitored nodes.
func monitoringTargets(ctx context.Context, client *docker.Client, realm string) ([]observability.Target, error) {
	containers, err := monitoredContainers(ctx, client, realm, "")
	if err != nil {
		return nil, err
	}
	targets := make([]observability.Target, 0, len(containers))
	for _, c := range containers {
		cluster := c.Labels[LabelCluster]
		targets = append(targets, observability.Target{
			Cluster:     cluster,
			Node:        strings.TrimPrefix(string(c.Name), ContainerPrefix(realm, cluster)),
			Role:        config.Role(c.Labels[LabelRole]),
			Managed:     IsManaged(c.Labels),
			Container:   c.Name,
			ContainerID: c.ID,
		})
	}
	return targets, nil
}

// alloyConfig returns the Alloy configuration of the realm's monitored
// nodes, under the daemon's cgroup driver.
func alloyConfig(ctx context.Context, client *docker.Client, realm string) (string, error) {
	var driver string
	var targets []observability.Target
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		info, err := client.Info(gctx)
		if err != nil {
			return fmt.Errorf("reading the cgroup driver: %w", err)
		}
		driver = info.CgroupDriver
		return nil
	})
	g.Go(func() error {
		var err error
		targets, err = monitoringTargets(gctx, client, realm)
		return err
	})
	if err := g.Wait(); err != nil {
		return "", err
	}
	return observability.GenerateAlloyConfig(realm, driver, targets), nil
}

// ensureMonitoring creates the realm's monitoring stack, or starts or
// upgrades it (see observability.Manager.EnsureStack), with the addresses
// pinned below the mesh DNS container's, waits for it and syncs Alloy with
// the realm's monitored nodes. It reports whether it created the stack:
// whether Alloy did not exist before. It is the progress step
// "monitoring".
func ensureMonitoring(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) (created bool, err error) {
	ctx, end := startStep(ctx, "monitoring")
	defer func() { end(err) }()
	exists, err := client.ContainerExists(ctx, observability.ContainerName(meshMgr.Realm, observability.ServiceAlloy))
	if err != nil {
		return false, fmt.Errorf("checking the monitoring stack: %w", err)
	}
	pinned, err := meshMgr.PinnedDNSIP(ctx)
	if err != nil {
		return false, err
	}
	conf, err := alloyConfig(ctx, client, meshMgr.Realm)
	if err != nil {
		return false, err
	}
	stack := observability.NewManager(client, meshMgr.Realm)
	if _, err := stack.EnsureStack(ctx, pinned, conf); err != nil {
		return !exists, err
	}
	// A stack that ran already has the configuration it had: the new
	// nodes come with this sync.
	return !exists, stack.Sync(ctx, conf)
}

// syncMonitoring rewrites Alloy's configuration from the realm's monitored
// nodes and has Alloy reload it.
func syncMonitoring(ctx context.Context, client *docker.Client, realm string) error {
	conf, err := alloyConfig(ctx, client, realm)
	if err != nil {
		return err
	}
	return observability.NewManager(client, realm).Sync(ctx, conf)
}

// stackDNSServices are the stack's services with a mesh DNS name.
var stackDNSServices = []observability.Service{
	observability.ServiceGrafana,
	observability.ServicePrometheus,
	observability.ServiceLoki,
}

// registerStackDNS registers the mesh DNS names of the stack's Grafana,
// Prometheus and Loki (grafana.<realm>.sind and so on) at their addresses
// on the mesh. AddDNSRecords leaves the Corefile alone when it has them
// already. It must not run alongside another change of the Corefile, such
// as registerNodes.
func registerStackDNS(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) error {
	names := make([]docker.ContainerName, len(stackDNSServices))
	for i, s := range stackDNSServices {
		names[i] = observability.ContainerName(meshMgr.Realm, s)
	}
	infos, err := client.InspectContainers(ctx, names...)
	if err != nil {
		return fmt.Errorf("inspecting the monitoring stack: %w", err)
	}
	var records []mesh.DNSRecord
	for i, info := range infos {
		if ip := info.IPs[meshMgr.NetworkName()]; ip != "" {
			records = append(records, mesh.DNSRecord{Hostname: observability.DNSName(meshMgr.Realm, stackDNSServices[i]), IP: ip})
		}
	}
	if err := meshMgr.AddDNSRecords(ctx, records); err != nil {
		return fmt.Errorf("registering the monitoring stack's DNS names: %w", err)
	}
	return nil
}

// startMonitoring starts the realm's monitoring stack when it exists but is
// stopped, as after a host reboot, and registers its DNS names again: its
// containers may get other addresses on a mesh without pinned ones. It
// reports whether the realm has a stack.
func startMonitoring(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) (bool, error) {
	found, err := observability.NewManager(client, meshMgr.Realm).Start(ctx)
	if err != nil || !found {
		return found, err
	}
	return true, registerStackDNS(ctx, client, meshMgr)
}

// forgetMonitoredNodes takes removed nodes of a monitored cluster out of
// the realm's monitoring: it starts the stack when it is stopped, syncs
// Alloy, which then no longer reads their journals, and removes their
// journal directories, or the cluster's directory when nodes is empty, or
// none without a cluster. The removal is done by then, so failures are only warnings: Alloy keeps
// reading what is gone until the next sync, and a directory left behind
// is cleared when a node of its name comes back, or goes with the stack.
func forgetMonitoredNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cluster string, nodes ...string) {
	realm := meshMgr.Realm
	warn := func(err error) {
		meshMgr.Warn(ctx, fmt.Sprintf("updating the monitoring stack of realm %q: %v", realm, err))
	}
	found, err := startMonitoring(ctx, client, meshMgr)
	if err != nil {
		warn(err)
		return
	}
	if !found {
		return
	}
	if err := syncMonitoring(ctx, client, realm); err != nil {
		warn(err)
		return
	}
	if cluster == "" {
		return
	}
	if err := observability.NewManager(client, realm).RemoveJournals(ctx, cluster, nodes...); err != nil {
		warn(err)
	}
}

// clusterMonitored reports whether any node container of a cluster mounts
// the realm's journal volume.
func clusterMonitored(ctx context.Context, client *docker.Client, realm, cluster string) (bool, error) {
	containers, err := monitoredContainers(ctx, client, realm, cluster)
	return len(containers) > 0, err
}

// otherMonitoredClusters reports whether a cluster other than cluster has
// a node container that mounts the realm's journal volume.
func otherMonitoredClusters(ctx context.Context, client *docker.Client, realm, cluster string) (bool, error) {
	containers, err := monitoredContainers(ctx, client, realm, "")
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(containers, func(c docker.ContainerListEntry) bool {
		return c.Labels[LabelCluster] != cluster
	}), nil
}

// removeMonitoringContainers removes the realm's stack containers, which
// keep the mesh network in use until they go.
func removeMonitoringContainers(ctx context.Context, client *docker.Client, realm string) error {
	if err := observability.NewManager(client, realm).Remove(ctx); err != nil {
		return fmt.Errorf("removing the monitoring stack: %w", err)
	}
	return nil
}

// removeMonitoringVolumes removes the realm's stack volumes and its
// journal volume, once their containers are gone.
func removeMonitoringVolumes(ctx context.Context, client *docker.Client, realm string) error {
	if err := observability.NewManager(client, realm).RemoveVolumes(ctx); err != nil {
		return fmt.Errorf("removing the monitoring stack's volumes: %w", err)
	}
	return nil
}

// createJournalVolume creates the realm's journal volume, which every node
// of a monitored cluster mounts its directory of. It belongs to the mesh's
// Compose project, like the stack's volumes, and goes with the stack.
func createJournalVolume(ctx context.Context, client *docker.Client, realm string) error {
	labels := docker.Labels{
		LabelRealm:                 realm,
		docker.ComposeProjectLabel: realm + "-mesh",
		docker.ComposeVolumeLabel:  "journal",
	}
	if err := client.CreateVolume(ctx, observability.JournalVolume(realm), labels); err != nil {
		return fmt.Errorf("creating journal volume: %w", err)
	}
	return nil
}

// journalHelperScript empties a cluster's directory of the journal volume,
// mounted at /journal, and makes a directory in it for each node.
const journalHelperScript = `c=$1; shift
rm -rf -- "/journal/$c" || exit
for n; do mkdir -p -- "/journal/$c/$n" || exit; done`

// resetClusterJournals makes the journal directories of a new cluster's
// nodes, which the nodes mount, in a throwaway container of image: the
// journal helper, <realm>-<cluster>-journal-helper, without a network, with
// the volume mounted volume-nocopy. It first removes what the directory of
// an earlier cluster of the name left, so that sind logs and Loki show no
// journal of it. Create runs it only after the preflight check, which
// rejects a cluster that exists. Like the other helpers it carries the
// realm and cluster labels, so delete cluster removes one an interrupted
// create left behind.
func resetClusterJournals(ctx context.Context, client *docker.Client, realm, clusterName, image string, nodes []string) error {
	flags := []string{
		"--name", string(ContainerName(realm, clusterName, "journal-helper")),
		"--label", LabelRealm + "=" + realm,
		"--label", LabelCluster + "=" + clusterName,
		"--network", "none",
		"--mount", docker.VolumeMount(observability.JournalVolume(realm), "/journal", "", false, true),
		"--entrypoint", "/bin/sh",
	}
	args := append([]string{"-c", journalHelperScript, "sh", clusterName}, nodes...)
	if _, err := client.RunEphemeralWith(ctx, flags, image, args...); err != nil {
		return fmt.Errorf("making journal directories: %w", err)
	}
	return nil
}

// imageProbe is what Create checks one image for on a monitored cluster.
type imageProbe struct {
	image string
	req   slurm.ProbeRequest
}

// monitoringProbes returns the probes of a monitored cluster's images, one
// per distinct image, the controllers' first: slurmctld in the
// controllers' image, slurmd in each managed worker group's, and MariaDB's
// error log in a managed db node's. PluginDir is slurm.main's.
func monitoringProbes(cfg *config.Cluster) []imageProbe {
	pluginDir, _ := cfg.Slurm.Main.Parameter("PluginDir")
	probes := []imageProbe{{image: controllerImage(cfg), req: slurm.ProbeRequest{Daemons: []string{slurm.Slurmctld}, PluginDir: pluginDir}}}
	add := func(image, daemon string, mariadb bool) {
		i := slices.IndexFunc(probes, func(p imageProbe) bool { return p.image == image })
		if i < 0 {
			probes = append(probes, imageProbe{image: image, req: slurm.ProbeRequest{PluginDir: pluginDir}})
			i = len(probes) - 1
		}
		if daemon != "" && !slices.Contains(probes[i].req.Daemons, daemon) {
			probes[i].req.Daemons = append(probes[i].req.Daemons, daemon)
		}
		probes[i].req.MariaDB = probes[i].req.MariaDB || mariadb
	}
	for _, n := range cfg.Nodes {
		switch {
		case n.Role == config.RoleWorker && (n.Managed == nil || *n.Managed):
			add(n.Image, slurm.Slurmd, false)
		case n.Role == config.RoleDB && (n.Managed == nil || *n.Managed):
			add(n.Image, "", true)
		}
	}
	return probes
}

// probeMonitoringImages probes a monitored cluster's images (see
// monitoringProbes), concurrently, and returns the Slurm version of the
// controllers' image. An image that cannot serve the endpoints the stack
// uses fails with ErrImageUnsupported, naming every problem; a db image
// whose MariaDB logs to a file only gets a warning.
func probeMonitoringImages(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster) (string, error) {
	probes := monitoringProbes(cfg)
	infos := make([]*slurm.ImageInfo, len(probes))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range probes {
		g.Go(func() error {
			info, err := slurm.ProbeImage(gctx, client, p.image, p.req)
			infos[i] = info
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}
	for i, p := range probes {
		if problems := infos[i].MonitoringProblems(p.req); len(problems) > 0 {
			return "", errorWith(ErrImageUnsupported, "image %s cannot serve monitoring: %s", p.image, strings.Join(problems, "; "))
		}
		if p.req.MariaDB {
			if warning := infos[i].MariaDBWarning(); warning != "" {
				meshMgr.Warn(ctx, fmt.Sprintf("image %s: %s", p.image, warning))
			}
		}
	}
	sindlog.From(ctx).DebugContext(ctx, "images can serve monitoring", "images", len(probes))
	return infos[0].Version, nil
}
