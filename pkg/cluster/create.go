// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/monitor"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
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

// pullImages pulls each image once, concurrently. It reports the pulls as
// the progress step "pull images", with a target for each image that shows
// the lines docker pull writes.
func pullImages(ctx context.Context, client *docker.Client, images []string) (err error) {
	sindlog.From(ctx).InfoContext(ctx, "pulling images", "images", strings.Join(images, ","))
	ctx, end := startStep(ctx, "pull images", progress.Flagged(progress.FlagFold|progress.FlagShowLines), progress.Total(len(images)))
	defer func() { end(err) }()
	g, gctx := errgroup.WithContext(ctx)
	ctxs, targets := startTargets(gctx, images, func(int) string { return "" })
	for i, image := range images {
		g.Go(func() (err error) {
			targets[i].Run()
			defer func() { endSpan(gctx, targets[i], err) }()
			if err := client.PullImage(ctxs[i], image); err != nil {
				return fmt.Errorf("pulling %s: %w", image, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// missingImages returns the images the daemon does not have, in order.
func missingImages(ctx context.Context, client *docker.Client, images []string) ([]string, error) {
	local := make([]bool, len(images))
	g, gctx := errgroup.WithContext(ctx)
	for i, image := range images {
		g.Go(func() error {
			exists, err := client.ImageExists(gctx, image)
			if err != nil {
				return fmt.Errorf("inspecting image %s: %w", image, err)
			}
			local[i] = exists
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var missing []string
	for i, image := range images {
		if !local[i] {
			missing = append(missing, image)
		}
	}
	return missing, nil
}

// imagePull is the image pull of a Create: the steps that run an image wait
// for it, the others run alongside. pulled says whether the step "pull
// images" ran, which err is the error of when it failed; an err without it
// is that of the inspection of the images. waiting is the progress wait
// "images" of the steps that wait for it.
type imagePull struct {
	done    chan struct{}
	err     error
	pulled  bool
	waiting *progress.Span
}

// startPull pulls images in g: all of them with all (--pull), else those
// the daemon does not have. Pulling the missing images up front keeps the
// pulls out of the node containers' docker create, where each would hold
// one of the client's docker call slots for as long as it pulls, and so
// keep the readiness probes of nodes that have started from running.
// Without images the pull is done at once.
//
// The steps that run an image wait under the span of waitCtx, the hidden
// step "preflight", which shows the wait "images" from the start of the
// pull to its end, so that a display shows what holds the step up.
func startPull(ctx, waitCtx context.Context, g *errgroup.Group, client *docker.Client, images []string, all bool) *imagePull {
	p := &imagePull{done: make(chan struct{})}
	if len(images) == 0 {
		close(p.done)
		return p
	}
	_, p.waiting = progress.Start(waitCtx, progress.KindWait, "images")
	g.Go(func() error {
		defer p.end()
		if !all {
			images, p.err = missingImages(ctx, client, images)
		}
		if p.err == nil && len(images) > 0 {
			p.pulled = true
			p.err = pullImages(ctx, client, images)
		}
		return p.err
	})
	return p
}

// end ends the wait "images" with the pull, and marks the pull done: the
// wait ends ok, or stopped by a pull that failed, which the step "pull
// images" or the preflight shows.
func (p *imagePull) end() {
	if p.err != nil {
		p.waiting.End(stopped{p.err})
	} else {
		p.waiting.End(nil)
	}
	close(p.done)
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
//	├ resolveInfra (DNS IP ║ SSH key ║ Slurm version) ──┤  (version from the image probe with monitoring)
//	├ DetectCVMFS (storage.cvmfs only) ─────────────────┼→ setupNodes
//	├ CheckNsdelegate ──────────────────────────────────┤
//	└ pullImages (missing images; all with cfg.Pull) ───┘
//	                        │
//	registerNodes ║ enableSlurm → createSlurmAccounts (accounts only) ║ createHomes (users only) ║ ensureMonitoring (monitoring only)
//	                        │
//	              registerStackDNS (monitoring only)
//	                        │
//	                    *Cluster
//
// Each distinct image the daemon does not have, or with cfg.Pull each
// distinct image, is pulled once, concurrently, and the steps that run one
// (the helpers of createResources, the Slurm version, DetectCVMFS,
// CheckNsdelegate) wait for the pull; nothing is created with --pull
// always, and no node container's docker create pulls (see startPull). A failed check rolls back what the other branches have
// created by then; no node container exists yet.
//
// A monitored cluster (monitoring) gets its images probed for the HTTP
// endpoints the monitoring stack scrapes, which also gives the Slurm
// version, the stack's images pulled, its nodes' journal directories made,
// and in the last phase the realm's monitoring stack created or started
// and synced with the new nodes, and slurmctld's HTTP endpoints checked
// once it is ready. A failed create removes the stack when it made it and
// no other cluster of the realm is monitored.
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
	// are joined to the error. It is the progress step "rollback".
	resourcesCreated := false
	stackCreated := false
	defer func() {
		if retErr == nil {
			return
		}
		if rd.expired() {
			retErr = fmt.Errorf("cluster %q %w within %s: %w", cfg.Name, ErrNotReady, rd.wait, retErr)
		}
		if !resourcesCreated && !meshMgr.Created() {
			return
		}
		log.WarnContext(ctx, "cleaning up partial resources, please wait")
		cleanupCtx, endRollback := startRollback(ctx)
		errs := []error{retErr}
		removeMesh := rollbackRemovesMesh(cleanupCtx, client, meshMgr, cfg.Name, resourcesCreated)
		if resourcesCreated {
			logClusterDiagnostics(cleanupCtx, client, realm, cfg.Name)
			log.DebugContext(ctx, "removing cluster resources", "name", cfg.Name)
			if err := deleteClusterResources(cleanupCtx, client, meshMgr, cfg.Name, !removeMesh); err != nil {
				errs = append(errs, fmt.Errorf("rolling back: removing cluster resources (sind delete cluster removes what is left): %w", err))
			}
		}
		// The monitoring stack goes with the mesh, or when this create
		// made it and no other cluster is monitored; its containers
		// before the mesh network, its volumes after them.
		removeStack := removeMesh || stackCreated && !rollbackKeepsStack(cleanupCtx, client, realm, cfg.Name)
		if removeStack {
			if err := removeMonitoringContainers(cleanupCtx, client, realm); err != nil {
				errs = append(errs, fmt.Errorf("rolling back: %w", err))
			}
		}
		if removeMesh {
			log.DebugContext(ctx, "removing mesh created by this invocation")
			if err := meshMgr.CleanupMesh(cleanupCtx); err != nil {
				errs = append(errs, fmt.Errorf("rolling back: removing the mesh: %w", err))
			}
		}
		if removeStack {
			if err := removeMonitoringVolumes(cleanupCtx, client, realm); err != nil {
				errs = append(errs, fmt.Errorf("rolling back: %w", err))
			}
		}
		endRollback(errors.Join(errs[1:]...))
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
	prepGroup, pullCtx := errgroup.WithContext(ctx)
	images := clusterImages(cfg)
	if cfg.Monitoring {
		images = append(images, observability.Images()...)
	}
	// The checks and the resources are the hidden progress step
	// "preflight", which a display shows only once it has run for a
	// second; the pull is a step of its own beside it, which the preflight
	// waits for.
	prepCtx, preflight := progress.Start(pullCtx, progress.KindStep, "preflight", progress.Flagged(progress.FlagHidden))
	pull := startPull(pullCtx, prepCtx, prepGroup, client, images, cfg.Pull)

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
		if err := createResources(prepCtx, client, meshMgr, cfg, pull); err != nil {
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

	// Branch D: nsdelegate on the Docker host's kernel, in a throwaway
	// container of the controller's image, before any node container.
	prepGroup.Go(func() error {
		if err := pull.wait(prepCtx); err != nil {
			return err
		}
		if err := CheckNsdelegate(prepCtx, client, controllerImage(cfg)); err != nil {
			return err
		}
		log.DebugContext(prepCtx, "nsdelegate check passed")
		return nil
	})

	err := prepGroup.Wait()
	if pull.pulled && pull.err != nil && errors.Is(err, pull.err) {
		// The checks stopped because the pull failed, which the step
		// "pull images" shows; an inspection of the images that failed
		// before it is the preflight's failure.
		preflight.End(stopped{err})
	} else {
		preflight.End(err)
	}
	if err != nil {
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

	// registerNodes, enableSlurm and createHomes are independent: Slurm uses
	// short hostnames resolved by Docker embedded DNS on the cluster network.
	// Mesh DNS records are for SSH relay and host-side resolution only.
	readyCtx, cancelReady := rd.context(ctx)
	defer cancelReady()
	var cluster *Cluster
	g, gctx := errgroup.WithContext(readyCtx)
	g.Go(func() error {
		nodes, err := registerNodes(gctx, meshMgr, cfg.Name, nodeConfigs, nodeResults)
		if err != nil {
			return err
		}
		cluster = &Cluster{Name: cfg.Name, SlurmVersion: slurmVersion, State: StateRunning, Nodes: nodes}
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
			actx, end := startStep(gctx, "accounts")
			err := createSlurmAccounts(actx, client, realm, cfg, readinessInterval, watcher)
			end(err)
			if err != nil {
				return err
			}
			log.InfoContext(gctx, "slurm accounts created", "accounts", len(cfg.Accounts))
			return nil
		})
	}
	if users := NewLinuxUsers(cfg).Users; len(users) > 0 {
		g.Go(func() error {
			controller := ContainerName(realm, cfg.Name, string(config.RoleController))
			hctx, end := startStep(gctx, "home directories")
			err := createHomes(hctx, client, controller, users, sshPubKey)
			end(err)
			return err
		})
	}
	if cfg.Monitoring {
		g.Go(func() error {
			created, err := ensureMonitoring(gctx, client, meshMgr)
			stackCreated = created
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	// The stack's DNS names go into the Corefile after the nodes': two
	// read-modify-writes of it must not run at once.
	if cfg.Monitoring {
		if err := registerStackDNS(readyCtx, client, meshMgr); err != nil {
			return nil, err
		}
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
		sindlog.From(ctx).WarnContext(ctx, "keeping the mesh", "error", err)
		return false
	}
	return !inUse
}

// rollbackKeepsStack reports whether a failed Create keeps the monitoring
// stack it created: when another cluster of the realm is monitored, or
// when that cannot be told.
func rollbackKeepsStack(ctx context.Context, client *docker.Client, realm, clusterName string) bool {
	other, err := otherMonitoredClusters(ctx, client, realm, clusterName)
	if err != nil {
		sindlog.From(ctx).WarnContext(ctx, "keeping the monitoring stack", "error", err)
		return true
	}
	return other
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
	key, err := client.ReadFile(ctx, meshMgr.SSHContainerName(), mesh.PublicKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("reading SSH public key: %w", err)
	}
	return dnsIP, key, nil
}

// resolveInfra fetches mesh details and the Slurm version concurrently.
// The version stays empty for an unmanaged cluster: sind does not know which
// Slurm it runs, as the provisioning under test may install another one than
// the image ships. The version check waits for pull. On a monitored cluster
// the image probe (see probeMonitoringImages) replaces it: it reads the
// version too.
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
	if cfg.Monitoring || cfg.Managed() {
		g.Go(func() (err error) {
			if err = pull.wait(gctx); err != nil {
				return err
			}
			if cfg.Monitoring {
				slurmVersion, err = probeMonitoringImages(gctx, client, meshMgr, cfg)
				return err
			}
			if slurmVersion, err = slurm.DiscoverVersion(gctx, client, image); err != nil {
				return fmt.Errorf("discovering Slurm version: %w", err)
			}
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
// On a monitored cluster it also creates the realm's journal volume, and
// makes the nodes' directories in it (resetClusterJournals).
//
//	(network → relay connect → external containers connect) ║ (config vol → write config, managed only) ║ (munge vol → write munge key, not clientIds) ║ data vol ║ state vol (backup pair only) ║ home vol (users only) ║ (journal vol → journal dirs, monitoring only)
func createResources(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster, pull *imagePull) error {
	realm := meshMgr.Realm
	image := controllerImage(cfg)
	mungeKey := slurm.GenerateMungeKey()

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := CreateClusterNetwork(gctx, client, realm, cfg.Name); err != nil {
			return err
		}
		if err := client.ConnectNetwork(gctx, NetworkName(realm, cfg.Name), meshMgr.SSHContainerName()); err != nil {
			return fmt.Errorf("connecting SSH relay to cluster network: %w", err)
		}
		ConnectExternal(gctx, client, meshMgr, NetworkName(realm, cfg.Name))
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
	if cfg.Monitoring {
		g.Go(func() error {
			if err := createJournalVolume(gctx, client, realm); err != nil {
				return err
			}
			if err := pull.wait(gctx); err != nil {
				return err
			}
			return resetClusterJournals(gctx, client, realm, cfg.Name, image, NodeShortNames(cfg.Nodes))
		})
	}
	return g.Wait()
}

// setupNodes creates each node, starts its systemd monitor, waits for base
// readiness, and sets the node up in one docker exec (setupNode): it
// switches managed workers to nss_slurm (identity nssSlurm and clientIds),
// checks a managed api node for slurmrestd, adds the cluster users and
// groups where the identity mode puts them, injects the SSH public key, and
// collects the host key — all concurrently per node with no barrier between
// creation and probing.
//
//	per node:  create → monitor → wait(container, systemd, sshd, munge) → (inspect ║ setup: nss_slurm | slurmrestd → users → SSH key, host key)
//
// With identity clientIds munge is masked, so there is no munge to wait for.
// rd bounds each node's steps after its container has started.
//
// The nodes are the progress step "nodes", with a target for each node,
// all announced before the first node starts, and under it the node's
// docker calls, its wait "ready" and its call "setup". The first node that
// fails cancels the others, whose targets end canceled.
func setupNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, realm, clusterName, sshPubKey string, nodeConfigs []RunConfig, rd *readiness, watcher *monitor.Watcher) (_ []nodeResult, err error) {
	log := sindlog.From(ctx)
	results := make([]nodeResult, len(nodeConfigs))

	ctx, end := startStep(ctx, "nodes", progress.Flagged(progress.FlagFold), progress.Total(len(nodeConfigs)))
	defer func() { end(err) }()
	g, gctx := errgroup.WithContext(ctx)
	ctxs, targets := nodeTargets(gctx, nodeConfigs)
	for i, nc := range nodeConfigs {
		g.Go(func() (err error) {
			targets[i].Run()
			defer func() { endSpan(gctx, targets[i], err) }()
			tctx := ctxs[i]
			containerName := ContainerName(realm, clusterName, nc.ShortName)

			if _, err := CreateNode(tctx, client, meshMgr, nc); err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
			// The wait limit counts from here, after the image pull
			// that docker create may have made.
			nctx, cancel := rd.nodeContext(tctx)
			defer cancel()

			if watcher != nil {
				watcher.AddNodes([]monitor.NodeTarget{{
					ShortName: nc.ShortName,
					Container: containerName,
				}})
			}

			// The sshd and munge probes follow their units: a wait with
			// events runs them again after they passed only on an event of
			// their unit (see probe.UntilReadyWithEvents).
			baseProbes := []probe.Probe{
				{Name: "container", Check: probe.ContainerRunning},
				{Name: "systemd", Check: probe.SystemdReady},
				probe.ForService(probe.ServiceSSHD),
			}
			if nc.Identity != config.IdentityClientIDs {
				baseProbes = append(baseProbes, probe.ForService(probe.ServiceMunge))
			}
			log.DebugContext(nctx, "waiting for node", "node", nc.ShortName)
			if err := waitReady(nctx, client, containerName, baseProbes, rd.interval, watcher); err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}

			// The node's IPs and ID do not depend on its setup, so the
			// inspect runs alongside it, off the critical path.
			inspected := make(chan inspectResult, 1)
			go func() {
				info, err := client.InspectContainer(nctx, containerName)
				inspected <- inspectResult{info: info, err: err}
			}()

			hostKey, err := setupNode(nctx, client, containerName, nc, sshPubKey)
			ir := <-inspected // on a failed setup too: no inspect outlives setupNodes
			if err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
			if ir.err != nil {
				return fmt.Errorf("inspecting node %s: %w", nc.ShortName, ir.err)
			}

			results[i] = nodeResult{info: ir.info, hostKey: hostKey}
			return nil
		})
	}
	return results, g.Wait()
}

// registerNodes registers DNS records and known_hosts entries for all nodes
// in batch, and returns the resulting Node list. It is the progress step
// "mesh registration".
func registerNodes(ctx context.Context, meshMgr *mesh.Manager, clusterName string, nodeConfigs []RunConfig, results []nodeResult) (_ []*Node, err error) {
	ctx, end := startStep(ctx, "mesh registration")
	defer func() { end(err) }()
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
// role, slurmrestd on the api node and sackd on the submitter with identity
// clientIds (see nodeSlurmService), and waits for the service to become
// ready — concurrently per node.
// A managed db node goes first: slurmctld registers the cluster with slurmdbd
// when it starts, so mariadb and slurmdbd must be up by then.
//
//	┌──────────────────────────────────┐
//	│ managed db (if present):         │
//	│ enable mariadb → init DB         │
//	│ enable slurmdbd → wait slurmdbd  │
//	└────────────────┬─────────────────┘
//	┌────────────────┴───┐ ┌─────────────────────┐ ┌─────────────────────┐
//	│ controller:        │ │ worker-0:            │ │ api:                │
//	│ enable slurmctld   │ │ enable slurmd        │ │ enable slurmrestd   │ ...
//	│ wait slurmctld     │ │ wait slurmd          │ │ wait slurmrestd     │
//	└─────────┬──────────┘ └──────────┬───────────┘ └──────────┬──────────┘
//	          └───────────────────────┼────────────────────────┘
//
// The services are the progress step "slurm", with a target for each node
// it enables one on, all announced before the db node starts.
func enableSlurm(ctx context.Context, client *docker.Client, realm, clusterName string, nodeConfigs []RunConfig, interval time.Duration, watcher *monitor.Watcher) (err error) {
	log := sindlog.From(ctx)

	var dbNodes, nodes []RunConfig
	var services []probe.Service
	for _, nc := range nodeConfigs {
		if !nc.Managed {
			continue
		}
		if nc.Role == config.RoleDB {
			dbNodes = append(dbNodes, nc)
			continue
		}
		if service, ok := nodeSlurmService(nc); ok {
			nodes = append(nodes, nc)
			services = append(services, service)
		}
	}

	ctx, end := startStep(ctx, "slurm", progress.Flagged(progress.FlagFold), progress.Total(len(dbNodes)+len(nodes)))
	defer func() { end(err) }()
	// The group's context is made before the db nodes start, for their
	// targets to be announced with the others'.
	g, gctx := errgroup.WithContext(ctx)
	dbCtxs, dbTargets := nodeTargets(ctx, dbNodes)
	ctxs, targets := nodeTargets(gctx, nodes)

	for i, nc := range dbNodes {
		dbTargets[i].Run()
		containerName := ContainerName(realm, clusterName, nc.ShortName)
		log.DebugContext(dbCtxs[i], "enabling accounting services", "node", nc.ShortName)
		err := enableDBNode(dbCtxs[i], client, containerName, nc.ShortName, nc.StoragePass)
		if err == nil {
			slurmdbdProbe := probe.ForService(probe.ServiceSlurmdbd)
			if err = waitReady(dbCtxs[i], client, containerName, []probe.Probe{slurmdbdProbe}, interval, watcher); err != nil {
				err = fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
		}
		endSpan(ctx, dbTargets[i], err)
		if err != nil {
			// Nothing runs in g yet: Wait only releases its context. The
			// nodes that did not start end canceled with the step.
			_ = g.Wait()
			return err
		}
	}

	for i, nc := range nodes {
		service := services[i]
		slurmProbe := probe.ForService(service)
		g.Go(func() (err error) {
			targets[i].Run()
			defer func() { endSpan(gctx, targets[i], err) }()
			containerName := ContainerName(realm, clusterName, nc.ShortName)
			log.DebugContext(ctxs[i], "enabling slurm service", "node", nc.ShortName, "service", service)
			if err := enableService(ctxs[i], client, containerName, nc.ShortName, service); err != nil {
				return err
			}
			if err := waitReady(ctxs[i], client, containerName, []probe.Probe{slurmProbe}, interval, watcher); err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
			if !nc.Monitoring || service != probe.ServiceSlurmctld {
				return nil
			}
			if err := waitReady(ctxs[i], client, containerName, []probe.Probe{slurmctldHTTPProbe(nc.ShortName)}, interval, watcher); err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// slurmctldHTTPProbe returns the check that a monitored cluster's slurmctld
// serves the HTTP endpoints the monitoring stack scrapes: /metrics/nodes on
// the primary controller, /livez on the backup, whose standby slurmctld
// redirects the metrics to the controller in control.
func slurmctldHTTPProbe(shortName string) probe.Probe {
	path := "/metrics/nodes"
	if shortName == ControllerBackupShortName {
		path = "/livez"
	}
	return probe.Probe{Name: "slurmctld HTTP", Check: probe.SlurmctldHTTP(path)}
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
