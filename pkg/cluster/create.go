// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/monitor"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/GSI-HPC/sind/pkg/ssh"
	"golang.org/x/sync/errgroup"
)

// controllerImage returns the image configured for the controller node.
func controllerImage(cfg *config.Cluster) string {
	for _, n := range cfg.Nodes {
		if n.Role == config.RoleController {
			return n.Image
		}
	}
	return config.DefaultImage
}

// logExtraPrivileges emits a notice for each node that has extra capabilities,
// devices, or security options configured, making privilege escalation visible.
func logExtraPrivileges(ctx context.Context, configs []RunConfig) {
	log := sindlog.From(ctx)
	for _, cfg := range configs {
		if len(cfg.CapAdd) == 0 && len(cfg.Devices) == 0 && len(cfg.SecurityOpt) == 0 {
			continue
		}
		var parts []string
		if len(cfg.CapAdd) > 0 {
			parts = append(parts, "capAdd=["+strings.Join(cfg.CapAdd, ",")+"]")
		}
		if len(cfg.Devices) > 0 {
			parts = append(parts, "devices=["+strings.Join(cfg.Devices, ",")+"]")
		}
		if len(cfg.SecurityOpt) > 0 {
			parts = append(parts, "securityOpt=["+strings.Join(cfg.SecurityOpt, ",")+"]")
		}
		log.InfoContext(ctx, "extra privileges", "node", cfg.ShortName, "config", strings.Join(parts, " "))
	}
}

// nodeResult holds per-node data collected during concurrent setup.
type nodeResult struct {
	info    *docker.ContainerInfo
	hostKey string
}

// Create orchestrates the full cluster creation flow.
//
// The caller must ensure mesh infrastructure exists (via mesh.Manager.EnsureMesh)
// before calling Create. The context deadline controls the overall timeout;
// readinessInterval controls the polling interval for readiness probes.
//
//	PreflightCheck
//	      │
//	resolveInfra        DNS IP ║ SSH key ║ Slurm version
//	      │
//	createResources     network ║ (config vol → write) ║ (munge vol → write)
//	      │
//	setupNodes          (create + wait + SSH + hostkey) per node
//	      │
//	registerMesh ║ enableSlurm
//	      │
//	  *Cluster
func Create(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster, readinessInterval time.Duration) (result *Cluster, retErr error) {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	log.InfoContext(ctx, "creating cluster", "name", cfg.Name, "nodes", len(NodeShortNames(cfg.Nodes)))

	// Register cleanup before any fallible operation. Mesh cleanup runs
	// whenever this invocation created the mesh. Cluster resource cleanup
	// runs only after createResources starts. WithoutCancel keeps the
	// cleanup running when the parent context is cancelled (e.g. Ctrl+C).
	resourcesCreated := false
	defer func() {
		if retErr == nil {
			return
		}
		log.ErrorContext(ctx, "cleaning up partial resources, please wait")
		cleanupCtx := context.WithoutCancel(ctx)
		if resourcesCreated {
			logClusterDiagnostics(cleanupCtx, client, realm, cfg.Name)
			log.DebugContext(ctx, "removing cluster resources", "name", cfg.Name)
			if err := deleteClusterResources(cleanupCtx, client, meshMgr, cfg.Name); err != nil {
				log.ErrorContext(ctx, "cleanup failed", "error", err)
			}
		}
		if meshMgr.Created() {
			log.DebugContext(ctx, "removing mesh created by this invocation")
			if err := meshMgr.CleanupMesh(cleanupCtx); err != nil {
				log.ErrorContext(ctx, "mesh cleanup failed", "error", err)
			}
		}
	}()

	if err := PreflightCheck(ctx, client, realm, cfg); err != nil {
		return nil, err
	}
	log.DebugContext(ctx, "preflight check passed")

	dnsIP, sshPubKey, slurmVersion, err := resolveInfra(ctx, client, meshMgr, cfg)
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "resolved infrastructure", "slurm", slurmVersion)

	resourcesCreated = true
	if err := createResources(ctx, client, realm, cfg); err != nil {
		return nil, err
	}
	// Connect SSH relay to cluster network so it can reach nodes at cluster IPs.
	clusterNet := NetworkName(realm, cfg.Name)
	if err := client.ConnectNetwork(ctx, clusterNet, meshMgr.SSHContainerName()); err != nil {
		return nil, fmt.Errorf("connecting SSH relay to cluster network: %w", err)
	}
	log.DebugContext(ctx, "cluster resources created")

	nodeConfigs := NodeRunConfigs(cfg, realm, dnsIP, slurmVersion)
	logExtraPrivileges(ctx, nodeConfigs)

	// Start event watcher before creating nodes so it captures all
	// container start events. If the watcher fails to start, fall back
	// to poll-only mode.
	prefix := ContainerPrefix(realm, cfg.Name)
	watcher, stopWatcher := startWatcher(ctx, client, prefix, cfg.Name)
	defer stopWatcher()

	nodeResults, err := setupNodes(ctx, client, meshMgr, realm, cfg.Name, sshPubKey, nodeConfigs, readinessInterval, watcher)
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "nodes ready", "count", len(nodeConfigs))

	// registerMesh and enableSlurm are independent: Slurm uses short
	// hostnames resolved by Docker embedded DNS on the cluster network.
	// Mesh DNS records are for SSH relay and host-side resolution only.
	var cluster *Cluster
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var meshErr error
		cluster, meshErr = registerMesh(gctx, meshMgr, cfg.Name, slurmVersion, nodeConfigs, nodeResults)
		if meshErr != nil {
			return meshErr
		}
		log.DebugContext(gctx, "mesh registration complete")
		return nil
	})
	g.Go(func() error {
		if err := enableSlurm(gctx, client, realm, cfg.Name, nodeConfigs, readinessInterval, watcher); err != nil {
			return err
		}
		log.InfoContext(gctx, "slurm services enabled")
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return cluster, nil
}

// resolveMeshInfra fetches DNS IP and SSH public key from mesh infrastructure
// concurrently.
func resolveMeshInfra(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) (dnsIP, sshPubKey string, err error) {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		info, err := client.InspectContainer(gctx, meshMgr.DNSContainerName())
		if err != nil {
			return fmt.Errorf("inspecting DNS container: %w", err)
		}
		dnsIP = info.IPs[meshMgr.NetworkName()]
		return nil
	})
	g.Go(func() error {
		key, err := client.ReadFile(gctx, meshMgr.SSHContainerName(), "/root/.ssh/id_ed25519.pub")
		if err != nil {
			return fmt.Errorf("reading SSH public key: %w", err)
		}
		sshPubKey = key
		return nil
	})
	err = g.Wait()
	return
}

// resolveInfra fetches mesh details and the Slurm version concurrently.
//
//	┌──────────┐  ┌──────────┐  ┌──────────────┐
//	│  DNS IP  │  │ SSH key  │  │Slurm version │
//	└────┬─────┘  └────┬─────┘  └──────┬───────┘
//	     └─────────────┼───────────────┘
func resolveInfra(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster) (dnsIP, sshPubKey, slurmVersion string, err error) {
	image := controllerImage(cfg)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var meshErr error
		dnsIP, sshPubKey, meshErr = resolveMeshInfra(gctx, client, meshMgr)
		return meshErr
	})
	g.Go(func() error {
		ver, err := slurm.DiscoverVersion(gctx, client, image, cfg.Pull)
		if err != nil {
			return fmt.Errorf("discovering Slurm version: %w", err)
		}
		slurmVersion = ver
		return nil
	})
	err = g.Wait()
	return
}

// createResources creates cluster network, volumes, config, and munge key.
//
//	┌─────────┐  ┌─────────┐
//	network ║ (config vol → write config) ║ (munge vol → write munge key) ║ data vol
func createResources(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster) error {
	image := controllerImage(cfg)
	mungeKey := slurm.GenerateMungeKey()

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return CreateClusterNetwork(gctx, client, realm, cfg.Name) })
	g.Go(func() error {
		if err := CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeConfig); err != nil {
			return err
		}
		return WriteClusterConfig(gctx, client, realm, cfg, image, cfg.Pull)
	})
	g.Go(func() error {
		if err := CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeMunge); err != nil {
			return err
		}
		return WriteMungeKey(gctx, client, realm, cfg.Name, mungeKey, image, cfg.Pull)
	})
	if cfg.Storage.DataStorage.HostPath == "" {
		g.Go(func() error { return CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeData) })
	}
	return g.Wait()
}

// setupNodes creates each node, starts its systemd monitor, waits for base
// readiness, injects SSH public keys, and collects host keys — all
// concurrently per node with no barrier between creation and probing.
//
//	per node:  create → monitor → wait(container, systemd, sshd) → inspect → SSH → hostkey
func setupNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, realm, clusterName, sshPubKey string, nodeConfigs []RunConfig, interval time.Duration, watcher *monitor.Watcher) ([]nodeResult, error) {
	log := sindlog.From(ctx)
	baseProbes := []probe.Probe{
		{Name: "container", Check: probe.ContainerRunning},
		{Name: "systemd", Check: probe.SystemdReady},
		{Name: "sshd", Check: probe.SSHDReady},
	}
	results := make([]nodeResult, len(nodeConfigs))

	g, gctx := errgroup.WithContext(ctx)
	for i, nc := range nodeConfigs {
		g.Go(func() error {
			containerName := ContainerName(realm, clusterName, nc.ShortName)

			if _, err := CreateNode(gctx, client, meshMgr, nc); err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}

			if watcher != nil {
				watcher.AddNodes(gctx, []monitor.NodeTarget{{
					ShortName: nc.ShortName,
					Container: containerName,
				}})
			}

			log.DebugContext(gctx, "waiting for node", "node", nc.ShortName)
			if err := waitReady(gctx, client, containerName, baseProbes, interval, watcher); err != nil {
				return fmt.Errorf("waiting for %s: %w", nc.ShortName, err)
			}

			info, err := client.InspectContainer(gctx, containerName)
			if err != nil {
				return fmt.Errorf("inspecting node %s: %w", nc.ShortName, err)
			}

			if err := ssh.InjectPublicKey(gctx, client, containerName, sshPubKey); err != nil {
				return fmt.Errorf("injecting SSH key into %s: %w", nc.ShortName, err)
			}

			hostKey, err := ssh.CollectHostKey(gctx, client, containerName)
			if err != nil {
				return fmt.Errorf("collecting host key from %s: %w", nc.ShortName, err)
			}

			results[i] = nodeResult{info: info, hostKey: hostKey}
			return nil
		})
	}
	return results, g.Wait()
}

// registerMesh writes DNS records and known hosts for all nodes in batch,
// and builds the Cluster result.
func registerMesh(ctx context.Context, meshMgr *mesh.Manager, clusterName, slurmVersion string, nodeConfigs []RunConfig, results []nodeResult) (*Cluster, error) {
	nodes, err := registerNodes(ctx, meshMgr, clusterName, nodeConfigs, results)
	if err != nil {
		return nil, err
	}
	return &Cluster{
		Name:         clusterName,
		SlurmVersion: slurmVersion,
		State:        StateRunning,
		Nodes:        nodes,
	}, nil
}

// registerNodes registers DNS records and known_hosts entries for all nodes
// in batch, and returns the resulting Node list.
func registerNodes(ctx context.Context, meshMgr *mesh.Manager, clusterName string, nodeConfigs []RunConfig, results []nodeResult) ([]*Node, error) {
	dnsRecords := make([]mesh.DNSRecord, len(nodeConfigs))
	hostEntries := make([]mesh.KnownHostEntry, len(nodeConfigs))
	nodes := make([]*Node, len(nodeConfigs))

	for i, nc := range nodeConfigs {
		nr := results[i]
		netName := NetworkName(meshMgr.Realm, clusterName)
		dnsName := DNSName(nc.ShortName, clusterName, meshMgr.Realm)

		dnsRecords[i] = mesh.DNSRecord{Hostname: dnsName, IP: nr.info.IPs[netName]}
		hostEntries[i] = mesh.KnownHostEntry{Hostname: dnsName, HostKey: nr.hostKey}
		nodes[i] = &Node{
			Name:        nc.ShortName,
			Role:        nc.Role,
			ContainerID: nr.info.ID,
			IP:          nr.info.IPs[netName],
			State:       StateRunning,
		}
	}

	if err := meshMgr.AddDNSRecords(ctx, dnsRecords); err != nil {
		return nil, fmt.Errorf("registering DNS: %w", err)
	}
	if err := meshMgr.AddKnownHosts(ctx, hostEntries); err != nil {
		return nil, fmt.Errorf("registering host keys: %w", err)
	}

	return nodes, nil
}

// enableSlurm enables the Slurm daemon on each eligible node and waits for
// the service to become ready — concurrently per node.
//
//	┌────────────────────┐ ┌─────────────────────┐
//	│ controller:        │ │ worker-0:            │
//	│ enable slurmctld   │ │ enable slurmd        │ ...
//	│ wait slurmctld     │ │ wait slurmd          │
//	└─────────┬──────────┘ └──────────┬───────────┘
//	          └───────────┬───────────┘
func enableSlurm(ctx context.Context, client *docker.Client, realm, clusterName string, nodeConfigs []RunConfig, interval time.Duration, watcher *monitor.Watcher) error {
	log := sindlog.From(ctx)
	g, gctx := errgroup.WithContext(ctx)
	for _, nc := range nodeConfigs {
		if nc.Role == config.RoleWorker && !nc.Managed {
			continue
		}
		service, ok := probe.ServiceForRole(nc.Role)
		if !ok {
			continue
		}
		slurmProbe := probe.ForService(service)
		g.Go(func() error {
			containerName := ContainerName(realm, clusterName, nc.ShortName)
			log.DebugContext(gctx, "enabling slurm service", "node", nc.ShortName, "service", service)
			_, err := client.Exec(gctx, containerName, "systemctl", "enable", "--now", string(service))
			if err != nil {
				return fmt.Errorf("enabling %s on %s: %w", service, nc.ShortName, err)
			}
			return waitReady(gctx, client, containerName, []probe.Probe{slurmProbe}, interval, watcher)
		})
	}
	return g.Wait()
}

// startWatcher creates and starts an event watcher for the given cluster.
// If the watcher fails to start (e.g. docker events unavailable), nil is
// returned and the caller falls back to poll-only mode. The returned stop
// function cancels the watcher and waits for its goroutines to exit.
func startWatcher(ctx context.Context, client *docker.Client, containerPrefix, clusterName string) (w *monitor.Watcher, stop func()) {
	watchCtx, cancel := context.WithCancel(ctx)
	w = monitor.NewWatcher(client.Executor, containerPrefix, clusterName)
	if err := w.Start(watchCtx, nil); err != nil {
		cancel()
		sindlog.From(ctx).Log(ctx, sindlog.LevelTrace, "event watcher not available, using poll-only mode", "err", err)
		return nil, func() {}
	}
	return w, func() {
		cancel()
		w.Wait()
	}
}

// waitReady polls probes until ready, optionally accelerated by events from
// a watcher. If watcher is nil, falls back to plain polling.
func waitReady(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []probe.Probe, interval time.Duration, watcher *monitor.Watcher) error {
	if watcher == nil {
		return probe.UntilReady(ctx, client, name, probes, interval)
	}
	ch := watcher.Subscribe()
	defer watcher.Unsubscribe(ch)
	return probe.UntilReadyWithEvents(ctx, client, name, probes, interval, ch)
}
