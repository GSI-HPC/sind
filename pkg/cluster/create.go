// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// clusterImages returns the distinct images of the cluster's nodes, the
// controller's first: the helper containers, the version check and the
// CVMFS check run it.
func clusterImages(cfg *config.Cluster) []string {
	images := []string{controllerImage(cfg)}
	for _, n := range cfg.Nodes {
		if n.Image != "" && !slices.Contains(images, n.Image) {
			images = append(images, n.Image)
		}
	}
	return images
}

// pullImages pulls each image once, concurrently.
func pullImages(ctx context.Context, client *docker.Client, images []string) error {
	sindlog.From(ctx).InfoContext(ctx, "pulling images", "images", strings.Join(images, ","))
	g, gctx := errgroup.WithContext(ctx)
	for _, image := range images {
		g.Go(func() error {
			if err := client.PullImage(gctx, image); err != nil {
				return fmt.Errorf("pulling %s: %w", image, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// imagePull is the --pull of a Create: the steps that run an image wait for
// it, the others run alongside.
type imagePull struct {
	done chan struct{}
	err  error
}

// startPull pulls images in g. Without images the pull is done at once.
func startPull(ctx context.Context, g *errgroup.Group, client *docker.Client, images []string) *imagePull {
	p := &imagePull{done: make(chan struct{})}
	if len(images) == 0 {
		close(p.done)
		return p
	}
	g.Go(func() error {
		p.err = pullImages(ctx, client, images)
		close(p.done)
		return p.err
	})
	return p
}

// wait blocks until the pull is done, and returns its error, or until ctx
// ends.
func (p *imagePull) wait(ctx context.Context) error {
	select {
	case <-p.done:
		return p.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// logExtraPrivileges emits a notice, at info level, for each node that has
// extra capabilities, devices or security options configured or bind-mounts
// host directories (the data directory, the host's /cvmfs), making the
// node's access to the host visible with -v.
func logExtraPrivileges(ctx context.Context, configs []RunConfig) {
	log := sindlog.From(ctx)
	for _, cfg := range configs {
		var binds []string
		if cfg.DataHostPath != "" {
			mountPath := cfg.DataMountPath
			if mountPath == "" {
				mountPath = DefaultDataMountPath
			}
			binds = append(binds, cfg.DataHostPath+":"+mountPath)
		}
		if cfg.CVMFS == config.StorageHostPath {
			binds = append(binds, CVMFSPath+":"+CVMFSPath+":ro")
		}
		var parts []string
		for _, p := range []struct {
			name   string
			values []string
		}{
			{"capAdd", cfg.CapAdd},
			{"devices", cfg.Devices},
			{"securityOpt", cfg.SecurityOpt},
			{"hostMounts", binds},
		} {
			if len(p.values) > 0 {
				parts = append(parts, p.name+"=["+strings.Join(p.values, ",")+"]")
			}
		}
		if len(parts) > 0 {
			log.InfoContext(ctx, "extra privileges", "node", cfg.ShortName, "config", strings.Join(parts, " "))
		}
	}
}

// DefaultReadinessInterval is the delay between readiness probe rounds
// that Create and WorkerAdd use for a readinessInterval of zero or less.
const DefaultReadinessInterval = probe.DefaultInterval

// inspectResult is the result of a docker inspect run in a goroutine.
type inspectResult struct {
	info *docker.ContainerInfo
	err  error
}

// nodeResult holds per-node data collected during concurrent setup.
type nodeResult struct {
	info    *docker.ContainerInfo
	hostKey string
}

// Create orchestrates the full cluster creation flow.
//
// The caller holds the realm lock (state.LockRealm) from before
// mesh.Manager.EnsureMesh until Create returns, and checks the daemon with
// CheckDaemon before either.
//
// The caller must ensure mesh infrastructure exists (via mesh.Manager.EnsureMesh)
// before calling Create, and apply the config's defaults
// (config.Cluster.ApplyDefaults). Create validates the config first, and
// refuses a config realm other than meshMgr.Realm, the realm it creates the
// cluster in. The context deadline controls the overall timeout;
// readinessInterval controls the polling interval for readiness probes, and
// zero or less means DefaultReadinessInterval. cfg.Wait, when positive,
// limits how long Create waits for the nodes and Slurm to become ready,
// counted for each node from when its container has started, so that image
// pulls do not count; the steps after the nodes are ready end cfg.Wait
// after the last node container started. When the limit is reached, Create
// rolls back and returns an error that wraps ErrNotReady.
//
//	┌ PreflightCheck → createResources ─────────────────┐
//	├ resolveInfra (DNS IP ║ SSH key ║ Slurm version) ──┼→ setupNodes
//	├ DetectCVMFS (storage.cvmfs only) ─────────────────┤
//	└ pullImages (cfg.Pull only) ───────────────────────┘
//	                        │
//	registerMesh ║ enableSlurm → createSlurmAccounts (accounts only) ║ createHomes (users only)
//	                        │
//	                    *Cluster
//
// With cfg.Pull, each distinct image is pulled once, concurrently, and the
// steps that run one (the helpers of createResources, the Slurm version,
// DetectCVMFS) wait for the pull; nothing is created with --pull always.
//
// An unmanaged cluster (managed: false on the controller) gets the same
// containers, volumes and munge key, but sind writes no Slurm configuration,
// does not discover the Slurm version and enables no Slurm daemon.
func Create(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster, readinessInterval time.Duration) (result *Cluster, retErr error) {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm
	rd := &readiness{interval: readinessInterval, wait: cfg.Wait}

	log.InfoContext(ctx, "creating cluster", "name", cfg.Name, "nodes", len(NodeShortNames(cfg.Nodes)))

	// Register cleanup before any fallible operation. Cluster resource
	// cleanup runs only after createResources starts; the mesh goes too
	// when this invocation created it and no other cluster uses it.
	// WithoutCancel keeps the cleanup running when the parent context is
	// cancelled (e.g. Ctrl+C), and rollbackTimeout bounds it. Its failures
	// are joined to the error.
	resourcesCreated := false
	defer func() {
		if retErr == nil {
			return
		}
		if rd.expired() {
			retErr = fmt.Errorf("cluster %s %w within %s: %w", cfg.Name, ErrNotReady, rd.wait, retErr)
		}
		if !resourcesCreated && !meshMgr.Created() {
			return
		}
		log.ErrorContext(ctx, "cleaning up partial resources, please wait")
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		errs := []error{retErr}
		removeMesh := rollbackRemovesMesh(cleanupCtx, client, meshMgr, cfg.Name, resourcesCreated)
		if resourcesCreated {
			logClusterDiagnostics(cleanupCtx, client, realm, cfg.Name)
			log.DebugContext(ctx, "removing cluster resources", "name", cfg.Name)
			if err := deleteClusterResources(cleanupCtx, client, meshMgr, cfg.Name, !removeMesh); err != nil {
				errs = append(errs, fmt.Errorf("rolling back: removing cluster resources (sind delete cluster removes what is left): %w", err))
			}
		}
		if removeMesh {
			log.DebugContext(ctx, "removing mesh created by this invocation")
			if err := meshMgr.CleanupMesh(cleanupCtx); err != nil {
				errs = append(errs, fmt.Errorf("rolling back: removing the mesh: %w", err))
			}
		}
		if len(errs) > 1 {
			retErr = errors.Join(errs...)
		}
	}()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid cluster config: %w", err)
	}
	if cfg.Realm != "" && cfg.Realm != realm {
		return nil, fmt.Errorf("config realm %q differs from the mesh manager's realm %q", cfg.Realm, realm)
	}

	var dnsIP, sshPubKey, slurmVersion string
	prepGroup, prepCtx := errgroup.WithContext(ctx)
	var images []string
	if cfg.Pull {
		images = clusterImages(cfg)
	}
	pull := startPull(prepCtx, prepGroup, client, images)

	// Branch A: preflight → createResources, which connects the SSH relay
	// to the cluster network. Serialised because createResources only makes
	// sense once preflight has passed. Preflight includes refusing cached
	// sind-node images from before identity modes (checkIdentityImages,
	// identity nssSlurm and clientIds only).
	prepGroup.Go(func() error {
		if err := checkIdentityImages(prepCtx, client, cfg); err != nil {
			return err
		}
		if err := PreflightCheck(prepCtx, client, realm, cfg); err != nil {
			return err
		}
		log.DebugContext(prepCtx, "preflight check passed")
		resourcesCreated = true
		if err := createResources(prepCtx, client, realm, cfg, meshMgr.SSHContainerName(), pull); err != nil {
			return err
		}
		log.DebugContext(prepCtx, "cluster resources created")
		return nil
	})

	// Branch B: resolve mesh DNS/SSH details and Slurm version in parallel
	// with Branch A's resource work.
	prepGroup.Go(func() error {
		ip, key, ver, err := resolveInfra(prepCtx, client, meshMgr, cfg, pull)
		if err != nil {
			return err
		}
		dnsIP, sshPubKey, slurmVersion = ip, key, ver
		log.InfoContext(prepCtx, "resolved infrastructure", "slurm", slurmVersion)
		return nil
	})

	// Branch C (storage.cvmfs only): pick how the nodes mount CVMFS.
	var cvmfs config.StorageType
	if cfg.Storage.CVMFS {
		prepGroup.Go(func() error {
			if err := pull.wait(prepCtx); err != nil {
				return err
			}
			backend, err := DetectCVMFS(prepCtx, client, controllerImage(cfg))
			if err != nil {
				return err
			}
			cvmfs = backend
			log.InfoContext(prepCtx, "mounting cvmfs", "type", backend, "source", cvmfsSource(backend))
			return nil
		})
	}

	if err := prepGroup.Wait(); err != nil {
		return nil, err
	}

	nodeConfigs := NodeRunConfigs(cfg, realm, dnsIP, slurmVersion, cvmfs)
	logExtraPrivileges(ctx, nodeConfigs)

	// Start event watcher before creating nodes so it captures all
	// container start events. If the watcher fails to start, fall back
	// to poll-only mode.
	prefix := ContainerPrefix(realm, cfg.Name)
	watcher, stopWatcher := startWatcher(ctx, client, prefix, cfg.Name)
	defer stopWatcher()

	nodeResults, err := setupNodes(ctx, client, meshMgr, realm, cfg.Name, sshPubKey, nodeConfigs, rd, watcher)
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "nodes ready", "count", len(nodeConfigs))

	// registerMesh, enableSlurm and createHomes are independent: Slurm uses
	// short hostnames resolved by Docker embedded DNS on the cluster network.
	// Mesh DNS records are for SSH relay and host-side resolution only.
	readyCtx, cancelReady := rd.context(ctx)
	defer cancelReady()
	var cluster *Cluster
	g, gctx := errgroup.WithContext(readyCtx)
	g.Go(func() error {
		var meshErr error
		cluster, meshErr = registerMesh(gctx, meshMgr, cfg.Name, slurmVersion, nodeConfigs, nodeResults)
		if meshErr != nil {
			return meshErr
		}
		log.DebugContext(gctx, "mesh registration complete")
		return nil
	})
	if cfg.Managed() {
		g.Go(func() error {
			if err := enableSlurm(gctx, client, realm, cfg.Name, nodeConfigs, readinessInterval, watcher); err != nil {
				return err
			}
			log.InfoContext(gctx, "slurm services enabled")
			if !cfg.UsesAccounts() {
				return nil
			}
			if err := createSlurmAccounts(gctx, client, realm, cfg, readinessInterval, watcher); err != nil {
				return err
			}
			log.InfoContext(gctx, "slurm accounts created", "accounts", len(cfg.Accounts))
			return nil
		})
	}
	if users := NewLinuxUsers(cfg).Users; len(users) > 0 {
		g.Go(func() error {
			controller := ContainerName(realm, cfg.Name, string(config.RoleController))
			return createHomes(gctx, client, controller, users, sshPubKey)
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return cluster, nil
}

// rollbackRemovesMesh reports whether a failed Create removes the mesh: only
// when EnsureMesh created it and no cluster uses it. A Manager can serve
// several creates, so Created alone does not tell. When the create did not
// get to make its resources, a cluster of the same name counts too: that
// is the one the preflight check found.
func rollbackRemovesMesh(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, resourcesCreated bool) bool {
	if !meshMgr.Created() {
		return false
	}
	ours := ""
	if resourcesCreated {
		ours = clusterName
	}
	inUse, err := HasOtherClusters(ctx, client, meshMgr.Realm, ours)
	if err != nil {
		sindlog.From(ctx).ErrorContext(ctx, "keeping the mesh", "error", err)
		return false
	}
	return !inUse
}

// resolveMeshInfra starts the realm's mesh DNS and SSH relay if they are
// stopped, and returns the DNS IP and the SSH public key.
func resolveMeshInfra(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager) (dnsIP, sshPubKey string, err error) {
	dnsIP, found, err := meshMgr.StartMesh(ctx)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", fmt.Errorf("inspecting DNS container: %s not found", meshMgr.DNSContainerName())
	}
	key, err := client.ReadFile(ctx, meshMgr.SSHContainerName(), "/root/.ssh/id_ed25519.pub")
	if err != nil {
		return "", "", fmt.Errorf("reading SSH public key: %w", err)
	}
	return dnsIP, key, nil
}

// resolveInfra fetches mesh details and the Slurm version concurrently.
// The version stays empty for an unmanaged cluster: sind does not know which
// Slurm it runs, as the provisioning under test may install another one than
// the image ships. The version check waits for pull.
//
//	┌──────────┐  ┌──────────┐  ┌──────────────┐
//	│  DNS IP  │  │ SSH key  │  │Slurm version │
//	└────┬─────┘  └────┬─────┘  └──────┬───────┘
//	     └─────────────┼───────────────┘
func resolveInfra(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster, pull *imagePull) (dnsIP, sshPubKey, slurmVersion string, err error) {
	image := controllerImage(cfg)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var meshErr error
		dnsIP, sshPubKey, meshErr = resolveMeshInfra(gctx, client, meshMgr)
		return meshErr
	})
	if cfg.Managed() {
		g.Go(func() error {
			if err := pull.wait(gctx); err != nil {
				return err
			}
			ver, err := slurm.DiscoverVersion(gctx, client, image)
			if err != nil {
				return fmt.Errorf("discovering Slurm version: %w", err)
			}
			slurmVersion = ver
			return nil
		})
	}
	err = g.Wait()
	return
}

// createResources creates cluster network, volumes, config, and munge key,
// and connects the SSH relay container to the network, which it needs to
// reach the nodes. The config volume of an unmanaged cluster stays empty.
// The helpers that write the config and the munge key run the controller's
// image, so they wait for pull.
//
//	(network → relay connect) ║ (config vol → write config, managed only) ║ (munge vol → write munge key, not clientIds) ║ data vol ║ state vol (backup pair only) ║ home vol (users only)
func createResources(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster, relay docker.ContainerName, pull *imagePull) error {
	image := controllerImage(cfg)
	mungeKey := slurm.GenerateMungeKey()

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := CreateClusterNetwork(gctx, client, realm, cfg.Name); err != nil {
			return err
		}
		if err := client.ConnectNetwork(gctx, NetworkName(realm, cfg.Name), relay); err != nil {
			return fmt.Errorf("connecting SSH relay to cluster network: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeConfig); err != nil {
			return err
		}
		if !cfg.Managed() {
			return nil
		}
		if err := pull.wait(gctx); err != nil {
			return err
		}
		return WriteClusterConfig(gctx, client, realm, cfg, image)
	})
	// auth/slurm (identity clientIds) replaces munge: its slurm.key is on
	// the config volume.
	if cfg.Identity.Mode != config.IdentityClientIDs {
		g.Go(func() error {
			if err := CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeMunge); err != nil {
				return err
			}
			if err := pull.wait(gctx); err != nil {
				return err
			}
			return WriteMungeKey(gctx, client, realm, cfg.Name, mungeKey, image)
		})
	}
	if !cfg.Storage.DataStorage.UsesHostPath() {
		g.Go(func() error { return CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeData) })
	}
	if cfg.HasBackupController() {
		g.Go(func() error { return CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeState) })
	}
	if len(cfg.Users) > 0 {
		g.Go(func() error { return CreateClusterVolume(gctx, client, realm, cfg.Name, VolumeHome) })
	}
	return g.Wait()
}

// setupNodes creates each node, starts its systemd monitor, waits for base
// readiness, switches managed workers to nss_slurm (identity nssSlurm and
// clientIds), adds the cluster users and groups where the identity mode
// puts them, injects SSH public keys, and collects host keys — all
// concurrently per node with no barrier between creation and probing.
//
//	per node:  create → monitor → wait(container, systemd, sshd, munge) → (inspect ║ nss_slurm → users → SSH → hostkey)
//
// With identity clientIds munge is masked, so there is no munge to wait for.
// rd bounds each node's steps after its container has started.
func setupNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, realm, clusterName, sshPubKey string, nodeConfigs []RunConfig, rd *readiness, watcher *monitor.Watcher) ([]nodeResult, error) {
	log := sindlog.From(ctx)
	results := make([]nodeResult, len(nodeConfigs))

	g, gctx := errgroup.WithContext(ctx)
	for i, nc := range nodeConfigs {
		g.Go(func() error {
			containerName := ContainerName(realm, clusterName, nc.ShortName)

			if _, err := CreateNode(gctx, client, meshMgr, nc); err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
			// The wait limit counts from here, after the image pull
			// that docker create may have made.
			nctx, cancel := rd.nodeContext(gctx)
			defer cancel()

			if watcher != nil {
				watcher.AddNodes([]monitor.NodeTarget{{
					ShortName: nc.ShortName,
					Container: containerName,
				}})
			}

			baseProbes := []probe.Probe{
				{Name: "container", Check: probe.ContainerRunning},
				{Name: "systemd", Check: probe.SystemdReady},
				{Name: "sshd", Check: probe.SSHDReady},
			}
			if nc.Identity != config.IdentityClientIDs {
				baseProbes = append(baseProbes, probe.Probe{Name: "munge", Check: probe.MungeReady})
			}
			log.DebugContext(nctx, "waiting for node", "node", nc.ShortName)
			if err := waitReady(nctx, client, containerName, baseProbes, rd.interval, watcher); err != nil {
				return fmt.Errorf("waiting for %s: %w", nc.ShortName, err)
			}

			// The node's IPs and ID do not depend on the steps below, so
			// the inspect runs alongside them, off the critical path.
			inspected := make(chan inspectResult, 1)
			go func() {
				info, err := client.InspectContainer(nctx, containerName)
				inspected <- inspectResult{info: info, err: err}
			}()

			if nc.NSSSlurm {
				if err := enableNSSSlurm(nctx, client, containerName, nc); err != nil {
					return fmt.Errorf("node %s: %w", nc.ShortName, err)
				}
			}

			if nc.AddUsers && !nc.Users.IsEmpty() {
				if err := addUsers(nctx, client, containerName, nc.Users); err != nil {
					return fmt.Errorf("node %s: %w", nc.ShortName, err)
				}
			}

			hostKey, err := ssh.InjectKeyAndCollectHostKey(nctx, client, containerName, sshPubKey)
			if err != nil {
				return fmt.Errorf("setting up SSH on %s: %w", nc.ShortName, err)
			}

			ir := <-inspected
			if ir.err != nil {
				return fmt.Errorf("inspecting node %s: %w", nc.ShortName, ir.err)
			}

			results[i] = nodeResult{info: ir.info, hostKey: hostKey}
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

	// The Corefile and known_hosts live in different containers: update
	// them in parallel, each with a single read-modify-write. Neither
	// update cancels the other, which could cut a write short.
	var dnsErr, hostsErr error
	var g errgroup.Group
	g.Go(func() error {
		if err := meshMgr.AddDNSRecords(ctx, dnsRecords); err != nil {
			dnsErr = fmt.Errorf("registering DNS: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := meshMgr.AddKnownHosts(ctx, hostEntries); err != nil {
			hostsErr = fmt.Errorf("registering host keys: %w", err)
		}
		return nil
	})
	_ = g.Wait() // the goroutines report through dnsErr and hostsErr
	if err := errors.Join(dnsErr, hostsErr); err != nil {
		return nil, err
	}

	return nodes, nil
}

// enableSlurm enables the Slurm daemon on each managed node with a Slurm
// role, and sackd on the submitter with identity clientIds (see
// nodeSlurmService), and waits for the service to become ready —
// concurrently per node.
// A managed db node goes first: slurmctld registers the cluster with slurmdbd
// when it starts, so mariadb and slurmdbd must be up by then.
//
//	┌──────────────────────────────────┐
//	│ managed db (if present):         │
//	│ enable mariadb → init DB         │
//	│ enable slurmdbd → wait slurmdbd  │
//	└────────────────┬─────────────────┘
//	┌────────────────┴───┐ ┌─────────────────────┐
//	│ controller:        │ │ worker-0:            │
//	│ enable slurmctld   │ │ enable slurmd        │ ...
//	│ wait slurmctld     │ │ wait slurmd          │
//	└─────────┬──────────┘ └──────────┬───────────┘
//	          └───────────┬───────────┘
func enableSlurm(ctx context.Context, client *docker.Client, realm, clusterName string, nodeConfigs []RunConfig, interval time.Duration, watcher *monitor.Watcher) error {
	log := sindlog.From(ctx)

	for _, nc := range nodeConfigs {
		if nc.Role != config.RoleDB || !nc.Managed {
			continue
		}
		containerName := ContainerName(realm, clusterName, nc.ShortName)
		log.DebugContext(ctx, "enabling accounting services", "node", nc.ShortName)
		if err := enableDBNode(ctx, client, containerName, nc.ShortName, nc.StoragePass); err != nil {
			return err
		}
		slurmdbdProbe := probe.ForService(probe.ServiceSlurmdbd)
		if err := waitReady(ctx, client, containerName, []probe.Probe{slurmdbdProbe}, interval, watcher); err != nil {
			return err
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, nc := range nodeConfigs {
		if !nc.Managed || nc.Role == config.RoleDB {
			continue
		}
		service, ok := nodeSlurmService(nc)
		if !ok {
			continue
		}
		slurmProbe := probe.ForService(service)
		g.Go(func() error {
			containerName := ContainerName(realm, clusterName, nc.ShortName)
			log.DebugContext(gctx, "enabling slurm service", "node", nc.ShortName, "service", service)
			if err := enableService(gctx, client, containerName, nc.ShortName, service); err != nil {
				return err
			}
			return waitReady(gctx, client, containerName, []probe.Probe{slurmProbe}, interval, watcher)
		})
	}
	return g.Wait()
}

// enableService enables and starts a systemd unit on a node. When the unit
// fails to start, the error carries the tail of its journal: systemctl only
// points to journalctl, which is gone once a failed create removes the node.
func enableService(ctx context.Context, client *docker.Client, name docker.ContainerName, shortName string, service probe.Service) error {
	if _, err := client.Exec(ctx, name, "systemctl", "enable", "--now", string(service)); err != nil {
		return fmt.Errorf("enabling %s on %s: %w\n%s journal:\n%s",
			service, shortName, err, service, probe.UnitJournal(ctx, client, name, string(service)))
	}
	return nil
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
	ch := watcher.SubscribeTo(name)
	defer watcher.Unsubscribe(ch)
	return probe.UntilReadyWithEvents(ctx, client, name, probes, interval, ch)
}
