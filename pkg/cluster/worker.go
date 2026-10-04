// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"golang.org/x/sync/errgroup"
)

// WorkerAddOptions holds the parameters for adding worker nodes to a cluster.
// The image, resources and privileges are the new workers' shape: each one
// left at its zero value is taken from the cluster's newest worker (see
// workerShape).
type WorkerAddOptions struct {
	ClusterName string
	Count       int // 0 or less adds one worker
	Image       string
	CPUs        int
	Memory      string
	TmpSize     string
	Unmanaged   bool
	Pull        bool // pull Image first; needs Image
	// Wait, when positive, limits how long WorkerAdd waits for the new
	// workers to become ready, counted as for Create's config.Cluster.Wait.
	Wait        time.Duration
	CapAdd      []string
	CapDrop     []string
	Devices     []string
	SecurityOpt []string
}

// Check reports the first option WorkerAdd cannot act on: a negative CPU
// count, a memory or /tmp size sind cannot read, Pull without Image, an
// unknown capability, a device path that is not absolute or an unknown
// security option. It calls docker for none of them, so that the CLI can
// reject them as usage errors before it takes the realm lock.
func (o WorkerAddOptions) Check() error {
	if err := config.CheckCPUs("--cpus", o.CPUs); err != nil {
		return err
	}
	if err := config.CheckMemory("--memory", o.Memory); err != nil {
		return err
	}
	if err := config.CheckTmpSize("--tmp-size", o.TmpSize); err != nil {
		return err
	}
	if o.Pull && o.Image == "" {
		return errors.New("--pull needs --image: without one, new workers run the image of the cluster's newest worker, by ID")
	}
	if err := config.CheckCapabilities("--cap-add", o.CapAdd); err != nil {
		return err
	}
	if err := config.CheckCapabilities("--cap-drop", o.CapDrop); err != nil {
		return err
	}
	if err := config.CheckDevices(o.Devices); err != nil {
		return err
	}
	return config.CheckSecurityOpts("--security-opt", o.SecurityOpt)
}

// --- Exported functions ---

// WorkerAdd adds worker nodes to an existing cluster.
//
// The caller holds the realm lock (state.LockRealm) until WorkerAdd
// returns.
//
// For managed workers (default), the flow is:
//  1. Validate: options, controller exists, room on the realm's mesh and
//     the cluster network (see checkBridgePorts), sind-nodes.conf present
//  2. Inspect the controller and the newest worker, for the cluster's
//     settings and the new workers' shape
//  3. Check an explicit image: its Slurm version, its identity support;
//     meanwhile check the Docker host for nsdelegate (CheckNsdelegate) in
//     a container of the controller's image
//  4. Create worker container(s)
//  5. Wait for readiness, inject SSH keys, collect host keys
//  6. Register DNS + known_hosts, while sind-nodes.conf gets the new nodes,
//     slurmctld is reconfigured and slurmd starts on the new nodes
//
// For unmanaged workers (Unmanaged=true), the Slurm steps are skipped.
// Workers added to an unmanaged cluster are always unmanaged. A failure
// after the first container exists removes the new containers, mesh
// entries and NodeName lines again. readinessInterval is the delay
// between readiness probe rounds, as for Create.
func WorkerAdd(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, opts WorkerAddOptions, readinessInterval time.Duration) (result []*Node, retErr error) {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm
	rd := &readiness{interval: readinessInterval, wait: opts.Wait}

	log.InfoContext(ctx, "adding workers", "cluster", opts.ClusterName, "count", opts.Count)

	// Check the options as the config's are checked, before any container
	// exists.
	if err := opts.Check(); err != nil {
		return nil, err
	}

	// List cluster containers once for validation, index and the newest
	// worker.
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return nil, errorWith(ErrClusterNotFound, "controller not found for cluster %q", opts.ClusterName)
	}
	controllerName := controller.Name

	// The newest worker that is managed like the new ones gives them their
	// shape. sind always writes sind.managed, so the docker ps label is
	// reliable for this choice.
	wantManaged := !opts.Unmanaged && IsManaged(controller.Labels)
	newest, _ := newestWorker(containers, realm, opts.ClusterName, wantManaged)

	// The mesh and the cluster network must have room for the new workers
	// before sind starts the mesh or creates any of them.
	if err := checkBridgePorts(ctx, client, realm, opts.ClusterName, max(opts.Count, 1)); err != nil {
		return nil, err
	}

	infra, err := resolveWorkerInfra(ctx, client, meshMgr, controllerName, newest)
	if err != nil {
		return nil, err
	}

	// Check nsdelegate on the Docker host's kernel in a throwaway container
	// of the controller's image, which the daemon has, while the steps up to
	// the first worker container run. An early return stops the check and
	// waits for it, so that its docker run does not outlive WorkerAdd.
	nsdCtx, stopNsd := context.WithCancel(ctx)
	var nsdelegate errgroup.Group
	nsdelegate.Go(func() error { return CheckNsdelegate(nsdCtx, client, infra.controller.Image) })
	defer func() {
		stopNsd()
		_ = nsdelegate.Wait()
	}()

	// The cluster's settings come from the controller's labels as docker
	// inspect reports them, a map. docker ps joins all labels with commas,
	// so it cannot return a value with a comma in it.
	labels := infra.controller.Labels

	// sind wrote no Slurm configuration for an unmanaged cluster to add
	// workers to.
	if !opts.Unmanaged && !IsManaged(labels) {
		log.DebugContext(ctx, "cluster is unmanaged, adding unmanaged workers", "cluster", opts.ClusterName)
		opts.Unmanaged = true
	}
	managed := !opts.Unmanaged

	// Read sind-nodes.conf for managed workers: it has to be there, and
	// updateNodesConf adds the new nodes to it. Read slurm.conf too: whether
	// it binds their tasks with task/affinity, and whether it declares a
	// default partition other than sind's.
	var nodesConf string
	var slurmConf config.Section
	if managed {
		nodesConf, err = readNodesConf(ctx, client, controller)
		if err != nil {
			return nil, err
		}
		slurmConf, err = slurm.ReadSlurmConf(func(path string) (string, error) {
			return client.ReadFile(ctx, controller.Name, path)
		})
		if err != nil {
			return nil, err
		}
	}
	taskAffinity := slurm.EnablesTaskAffinity(slurmConf)

	// Inherit the data and CVMFS mounts, the users and the identity mode
	// from the controller. A managed worker gets the users only with
	// identity local; with nssSlurm and clientIds it resolves them with
	// nss_slurm instead.
	dataHostPath := labels[LabelDataHostPath]
	if dataHostPath != "" && !path.IsAbs(dataHostPath) {
		return nil, fmt.Errorf("controller label %s=%q is not an absolute path: refusing to bind-mount it into new workers", LabelDataHostPath, dataHostPath)
	}
	dataMountPath := DataMountPath(labels)
	cvmfs := config.StorageType(labels[LabelCVMFS])
	users, err := LinuxUsersFromLabels(labels)
	if err != nil {
		return nil, err
	}
	identity := config.Identity{Mode: IdentityFromLabels(labels)}
	slurmVersion := labels[LabelSlurmVersion]

	// The new workers' shape: the newest worker's, or the controller's
	// image and built-in resources without one, with the options' settings
	// on top.
	shape := defaultWorkerShape(infra.controller.Image)
	if infra.worker != nil {
		shape = existingWorkerShape(ctx, infra.worker, shape, taskAffinity)
	}
	shape = shape.override(opts)

	if err := checkWorkerImage(ctx, client, opts, managed, slurmVersion); err != nil {
		return nil, err
	}
	// A local image may be from before identity modes.
	if managed && identity.UsesNSSSlurm() {
		if err := checkIdentityImage(ctx, client, shape.Image, identity.Mode); err != nil {
			return nil, err
		}
	}

	// Build RunConfig entries for new nodes.
	startIdx := nextWorkerIndexFromContainers(containers, realm, opts.ClusterName)
	count := max(opts.Count, 1)
	nodeConfigs := make([]RunConfig, count)
	for i := range count {
		nodeConfigs[i] = RunConfig{
			Realm:           realm,
			ClusterName:     opts.ClusterName,
			ShortName:       fmt.Sprintf("worker-%d", startIdx+i),
			Role:            config.RoleWorker,
			Image:           shape.Image,
			CPUs:            shape.CPUs,
			Memory:          shape.Memory,
			TmpSize:         shape.TmpSize,
			SlurmVersion:    slurmVersion,
			DNSIP:           infra.dnsIP,
			DataHostPath:    dataHostPath,
			DataMountPath:   dataMountPath,
			Managed:         managed,
			ContainerNumber: startIdx + i + 1,
			CapAdd:          shape.CapAdd,
			CapDrop:         shape.CapDrop,
			Devices:         shape.Devices,
			SecurityOpt:     shape.SecurityOpt,
			CVMFS:           cvmfs,
			Users:           users,
			AddUsers:        nodeGetsUsers(identity, config.RoleWorker, managed, false),
			Identity:        identity.Mode,
			NSSSlurm:        managed && identity.UsesNSSSlurm(),
			TaskAffinity:    taskAffinity,
		}
	}
	logExtraPrivileges(ctx, nodeConfigs)

	if err := nsdelegate.Wait(); err != nil {
		return nil, err
	}

	// From this point on, worker containers, and their NodeName lines in
	// sind-nodes.conf, may exist. Clean them up on failure so the user does
	// not have to remove them manually before retrying: a retry picks the
	// same names.
	needsCleanup := true
	confUpdated := false
	defer func() {
		if retErr != nil && rd.expired() {
			retErr = fmt.Errorf("workers %w within %s: %w", ErrNotReady, rd.wait, retErr)
		}
		if retErr != nil && needsCleanup {
			log.ErrorContext(ctx, "cleaning up partial resources, please wait")
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			defer cancel()
			if confUpdated {
				if err := revertNodesConf(cleanupCtx, client, controllerName, nodesConf); err != nil {
					retErr = errors.Join(retErr, fmt.Errorf("rolling back: %w", err))
				}
			}
			if err := cleanupWorkers(cleanupCtx, client, meshMgr, realm, opts.ClusterName, nodeConfigs); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rolling back: %w", err))
			}
		}
	}()

	// Start event watcher before creating nodes.
	prefix := ContainerPrefix(realm, opts.ClusterName)
	watcher, stopWatcher := startWatcher(ctx, client, prefix, opts.ClusterName)
	defer stopWatcher()

	// Create nodes, start systemd monitors, and wait for readiness.
	nodeResults, err := setupNodes(ctx, client, meshMgr, realm, opts.ClusterName, infra.sshPubKey, nodeConfigs, rd, watcher)
	if err != nil {
		return nil, err
	}
	readyCtx, cancelReady := rd.context(ctx)
	defer cancelReady()

	// Register DNS + known_hosts while the Slurm side is updated: Slurm
	// uses short hostnames resolved by Docker embedded DNS on the cluster
	// network, as in Create. The group has no shared context, so a failure
	// on one side does not cancel the other halfway, such as in the middle
	// of a CoreDNS restart; the rollback undoes both.
	var nodes []*Node
	var g errgroup.Group
	g.Go(func() error {
		var err error
		nodes, err = registerNodes(readyCtx, meshMgr, opts.ClusterName, nodeConfigs, nodeResults)
		return err
	})
	if managed {
		confUpdated = true
		g.Go(func() error {
			if err := updateNodesConf(readyCtx, client, controllerName, nodesConf, slurmConf, nodeConfigs); err != nil {
				return err
			}
			return enableSlurm(readyCtx, client, realm, opts.ClusterName, nodeConfigs, readinessInterval, watcher)
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	needsCleanup = false
	return nodes, nil
}

// --- Unexported helpers ---

// findController returns the controller container sind uses to edit the
// shared Slurm configuration and run scontrol: the primary controller, or
// the backup when the primary is gone or stopped and the backup runs (e.g.
// after a failover). Both mount the same config volume, and scontrol always
// reaches the controller in control. Returns false if the cluster has
// neither.
func findController(containers []docker.ContainerListEntry, realm, clusterName string) (docker.ContainerListEntry, bool) {
	primaryName := ContainerName(realm, clusterName, string(config.RoleController))
	backupName := ContainerName(realm, clusterName, ControllerBackupShortName)
	var primary, backup *docker.ContainerListEntry
	for i := range containers {
		switch containers[i].Name {
		case primaryName:
			primary = &containers[i]
		case backupName:
			backup = &containers[i]
		}
	}
	backupRuns := backup != nil && backup.State == docker.StateRunning
	switch {
	case primary != nil && (primary.State == docker.StateRunning || !backupRuns):
		return *primary, true
	case backup != nil:
		return *backup, true
	}
	return docker.ContainerListEntry{}, false
}

// clusterManaged reports whether sind manages the Slurm configuration and
// daemons of a cluster: the sind.managed label of the controller container
// findController picks. A cluster without a controller counts as managed.
func clusterManaged(containers []docker.ContainerListEntry, realm, clusterName string) bool {
	controller, ok := findController(containers, realm, clusterName)
	return !ok || IsManaged(controller.Labels)
}

// readNodesConf reads sind-nodes.conf through the controller, which has to
// run: docker exec cannot reach a stopped or paused container, and sind
// would otherwise add or remove containers without telling Slurm. A file
// that is not there, because the user replaced sind's configuration, is
// ErrNodesConfMissing; any other failure is returned as it is.
func readNodesConf(ctx context.Context, client *docker.Client, controller docker.ContainerListEntry) (string, error) {
	if controller.State != docker.StateRunning {
		return "", fmt.Errorf("controller %s is not running (%s): sind updates sind-nodes.conf and reconfigures Slurm through it; start it with sind power on or sind power unfreeze first", controller.Name, controller.State)
	}
	content, err := client.ReadFile(ctx, controller.Name, slurm.NodesConfPath)
	if err == nil {
		return content, nil
	}
	if exitErr, ok := errors.AsType[*cmdexec.ExitError](err); ok &&
		strings.Contains(exitErr.Stderr, slurm.NodesConfPath+": No such file or directory") {
		return "", ErrNodesConfMissing
	}
	return "", fmt.Errorf("reading sind-nodes.conf: %w", err)
}

// workerInfra is what WorkerAdd learns about a cluster before it creates
// workers.
type workerInfra struct {
	dnsIP, sshPubKey string
	controller       *docker.ContainerInfo
	worker           *docker.ContainerInfo // the newest worker, nil without one
}

// resolveWorkerInfra fetches the DNS IP and SSH public key, and inspects the
// controller and the newest worker (none when worker is empty),
// concurrently.
//
//	┌──────────┐  ┌──────────┐  ┌──────────┐  ┌─────────────┐
//	│  DNS IP  │  │ SSH key  │  │controller│  │newest worker│
//	└────┬─────┘  └────┬─────┘  └────┬─────┘  └──────┬──────┘
//	     └─────────────┴──────┬──────┴───────────────┘
func resolveWorkerInfra(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, controllerName, worker docker.ContainerName) (infra workerInfra, err error) {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var meshErr error
		infra.dnsIP, infra.sshPubKey, meshErr = resolveMeshInfra(gctx, client, meshMgr)
		return meshErr
	})
	g.Go(func() error {
		info, err := client.InspectContainer(gctx, controllerName)
		if err != nil {
			return fmt.Errorf("inspecting controller: %w", err)
		}
		infra.controller = info
		return nil
	})
	if worker != "" {
		g.Go(func() error {
			info, err := client.InspectContainer(gctx, worker)
			if err != nil {
				return fmt.Errorf("inspecting worker %s: %w", worker, err)
			}
			infra.worker = info
			return nil
		})
	}
	err = g.Wait()
	return
}

// checkWorkerImage pulls an image given with --image when opts.Pull asks
// for it, once, and checks it for managed workers of a cluster whose Slurm
// version sind knows: it runs slurmctld -V in the image and refuses a
// version other than the cluster's, as slurmd may not be newer than
// slurmctld and sind labels every node with the cluster's version.
func checkWorkerImage(ctx context.Context, client *docker.Client, opts WorkerAddOptions, managed bool, clusterVersion string) error {
	if opts.Pull {
		if err := pullImages(ctx, client, []string{opts.Image}); err != nil {
			return err
		}
	}
	if opts.Image == "" || !managed || clusterVersion == "" {
		return nil
	}
	version, err := slurm.DiscoverVersion(ctx, client, opts.Image)
	if err != nil {
		return fmt.Errorf("discovering the Slurm version of %s: %w", opts.Image, err)
	}
	if version != clusterVersion {
		return fmt.Errorf("image %s has Slurm %s, but cluster %q runs Slurm %s: managed workers need the cluster's version", opts.Image, version, opts.ClusterName, clusterVersion)
	}
	return nil
}

// updateNodesConf adds the new node definitions to the sind-nodes.conf
// content read before (see readNodesConf), writes it back, and
// reconfigures slurmctld. slurmConf, the cluster's slurm.conf, decides
// whether a new partition line is the default (see slurm.AddNodesToConf).
func updateNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, current string, slurmConf config.Section, nodeConfigs []RunConfig) error {
	var entries []slurm.NodeEntry
	for _, nc := range nodeConfigs {
		memMB, err := slurm.ParseMemoryMB(nc.Memory)
		if err != nil {
			return fmt.Errorf("parsing memory %q for %s: %w", nc.Memory, nc.ShortName, err)
		}
		entries = append(entries, slurm.NodeEntry{
			Name:     nc.ShortName,
			CPUs:     nc.CPUs,
			MemoryMB: memMB,
		})
	}
	updated := slurm.AddNodesToConf(current, entries, slurmConf)

	return writeNodesConfAndReconfigure(ctx, client, controllerName, updated)
}

// revertNodesConf takes the nodes of a failed WorkerAdd out of
// sind-nodes.conf again, and reconfigures slurmctld, so that neither a
// phantom node nor, after a retry, a duplicate definition stays behind. It
// writes back original, the content WorkerAdd read before it added them:
// the realm lock kept others from changing the file since, and the file
// itself may hold a write that failed.
func revertNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, original string) error {
	if err := writeNodesConfAndReconfigure(ctx, client, controllerName, original); err != nil {
		return fmt.Errorf("restoring sind-nodes.conf: %w", err)
	}
	return nil
}

// writeNodesConfAndReconfigure writes sind-nodes.conf to the controller
// and triggers slurmctld to reload. sind-nodes.conf defines every managed
// worker, so the write replaces the file in one step (see
// docker.Client.ReplaceFile), and it ignores the cancellation of ctx, as
// the --wait deadline or Ctrl+C would otherwise kill the docker CLI
// halfway.
func writeNodesConfAndReconfigure(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, content string) error {
	if err := client.ReplaceFile(context.WithoutCancel(ctx), controllerName, slurm.NodesConfPath, content); err != nil {
		return fmt.Errorf("updating sind-nodes.conf: %w", err)
	}
	if _, err := client.Exec(ctx, controllerName, "scontrol", "reconfigure"); err != nil {
		return fmt.Errorf("reconfiguring slurmctld: %w", err)
	}
	return nil
}

// workerIndex returns the index of a worker container, the N of
// <prefix>worker-N, and whether name is one.
func workerIndex(name docker.ContainerName, prefix string) (int, bool) {
	suffix, ok := strings.CutPrefix(string(name), prefix)
	if !ok {
		return 0, false
	}
	idx, err := strconv.Atoi(suffix)
	return idx, err == nil
}

// nextWorkerIndexFromContainers computes the next worker node index from
// a pre-fetched container list: one more than the highest index of the
// cluster's worker containers, or 0 without any.
func nextWorkerIndexFromContainers(containers []docker.ContainerListEntry, realm, clusterName string) int {
	prefix := string(ContainerName(realm, clusterName, "worker-"))
	maxIdx := -1
	for _, c := range containers {
		if idx, ok := workerIndex(c.Name, prefix); ok && idx > maxIdx {
			maxIdx = idx
		}
	}
	return maxIdx + 1
}

// newestWorker returns the worker container with the highest index, the
// one sind created last, among the workers that are managed or unmanaged
// as managed says, or among all workers when none is. It returns false for
// a cluster without workers.
func newestWorker(containers []docker.ContainerListEntry, realm, clusterName string, managed bool) (docker.ContainerName, bool) {
	prefix := string(ContainerName(realm, clusterName, "worker-"))
	matchIdx, anyIdx := -1, -1
	var match, anyWorker docker.ContainerName
	for _, c := range containers {
		idx, ok := workerIndex(c.Name, prefix)
		if !ok {
			continue
		}
		if idx > anyIdx {
			anyIdx, anyWorker = idx, c.Name
		}
		if IsManaged(c.Labels) == managed && idx > matchIdx {
			matchIdx, match = idx, c.Name
		}
	}
	if matchIdx >= 0 {
		return match, true
	}
	return anyWorker, anyIdx >= 0
}

// workerShape is what sets a worker container apart beyond the cluster's
// shared settings: its image, resources and privileges. sind create worker
// gives new workers the shape of the cluster's newest worker, so that
// scaling up a cluster adds nodes like the ones it has.
type workerShape struct {
	Image       string
	CPUs        int
	Memory      string
	TmpSize     string
	CapAdd      []string
	CapDrop     []string
	Devices     []string
	SecurityOpt []string
}

// defaultWorkerShape returns the shape of a cluster's first worker when it
// has none: the controller's image, by ID, and sind's built-in resources,
// without extra privileges.
func defaultWorkerShape(controllerImage string) workerShape {
	return workerShape{
		Image:   controllerImage,
		CPUs:    config.DefaultCPUs,
		Memory:  config.DefaultMemory,
		TmpSize: config.DefaultTmpSize,
	}
}

// existingWorkerShape returns the shape of an existing worker container as
// docker inspect reports it. The image is the container's image ID: its
// tag may since have moved to another image, even another Slurm release.
// A resource docker reports none for keeps fallback's value. The security
// options every node gets are left out, as sind decides on them for each
// new worker, and so is TaskAffinityCapability where sind gave it to this
// worker by itself: on a managed worker of a cluster whose slurm.conf
// enables task/affinity, as taskAffinity says (see
// getsTaskAffinityCapability). A SYS_NICE the user asked for stays.
// WorkerAdd reads slurm.conf for managed new workers only, so unmanaged
// ones modelled on a managed worker keep its SYS_NICE.
func existingWorkerShape(ctx context.Context, info *docker.ContainerInfo, fallback workerShape, taskAffinity bool) workerShape {
	hc := info.HostConfig
	capDrop := capabilityNames(hc.CapDrop)
	var own []string
	if IsManaged(info.Labels) && getsTaskAffinityCapability(taskAffinity, capDrop) {
		own = append(own, TaskAffinityCapability)
	}
	shape := workerShape{
		Image:       cmp.Or(info.Image, fallback.Image),
		CPUs:        fallback.CPUs,
		Memory:      fallback.Memory,
		TmpSize:     cmp.Or(tmpfsSize(hc.Tmpfs["/tmp"]), fallback.TmpSize),
		CapAdd:      capabilityNames(hc.CapAdd, own...),
		CapDrop:     capDrop,
		Devices:     deviceArgs(hc.Devices),
		SecurityOpt: extraSecurityOpts(ctx, info.Name, hc.SecurityOpt),
	}
	if cpus := int(hc.NanoCPUs / 1e9); cpus > 0 {
		shape.CPUs = cpus
	}
	if hc.Memory > 0 {
		shape.Memory = memoryArg(hc.Memory)
	}
	return shape
}

// override returns s with each setting that opts sets in place of s's.
// A list option replaces the inherited list as a whole.
func (s workerShape) override(opts WorkerAddOptions) workerShape {
	return workerShape{
		Image:       cmp.Or(opts.Image, s.Image),
		CPUs:        cmp.Or(opts.CPUs, s.CPUs),
		Memory:      cmp.Or(opts.Memory, s.Memory),
		TmpSize:     cmp.Or(opts.TmpSize, s.TmpSize),
		CapAdd:      orList(opts.CapAdd, s.CapAdd),
		CapDrop:     orList(opts.CapDrop, s.CapDrop),
		Devices:     orList(opts.Devices, s.Devices),
		SecurityOpt: orList(opts.SecurityOpt, s.SecurityOpt),
	}
}

// orList returns list, or fallback when list is empty.
func orList(list, fallback []string) []string {
	if len(list) > 0 {
		return list
	}
	return fallback
}

// tmpfsSize returns the size option of a --tmpfs mount's options, empty
// without one.
func tmpfsSize(options string) string {
	for opt := range strings.SplitSeq(options, ",") {
		if size, ok := strings.CutPrefix(opt, "size="); ok {
			return size
		}
	}
	return ""
}

// memoryArg returns a memory limit in bytes as a --memory value, in the
// largest unit that holds it exactly.
func memoryArg(bytes int64) string {
	switch {
	case bytes%(1<<30) == 0:
		return strconv.FormatInt(bytes>>30, 10) + "g"
	case bytes%(1<<20) == 0:
		return strconv.FormatInt(bytes>>20, 10) + "m"
	case bytes%(1<<10) == 0:
		return strconv.FormatInt(bytes>>10, 10) + "k"
	default:
		return strconv.FormatInt(bytes, 10) + "b"
	}
}

// capabilityNames returns capabilities as docker inspect reports them,
// CAP_SYS_ADMIN, under the names sind's options take, SYS_ADMIN, leaving
// out those in skip.
func capabilityNames(caps []string, skip ...string) []string {
	var names []string
	for _, c := range caps {
		name := strings.TrimPrefix(strings.ToUpper(c), "CAP_")
		if !slices.Contains(skip, name) {
			names = append(names, name)
		}
	}
	return names
}

// deviceArgs returns devices as --device values: the host path alone when
// the device keeps its path and docker's default permissions.
func deviceArgs(devices []docker.DeviceMapping) []string {
	var args []string
	for _, d := range devices {
		arg := d.PathOnHost
		if d.PathInContainer != d.PathOnHost || d.CgroupPermissions != "rwm" {
			arg += ":" + d.PathInContainer + ":" + d.CgroupPermissions
		}
		args = append(args, arg)
	}
	return args
}

// extraSecurityOpts returns a container's security options other than the
// ones BuildRunArgs gives every node. A seccomp profile is left out with a
// warning: docker reports its content, which the docker CLI cannot take
// back, as it reads a profile from a file.
func extraSecurityOpts(ctx context.Context, container docker.ContainerName, opts []string) []string {
	own := nodeSecurityOpts()
	var extra []string
	for _, opt := range opts {
		switch {
		case slices.Contains(own, opt):
		case strings.HasPrefix(opt, "seccomp={"):
			sindlog.From(ctx).WarnContext(ctx, "not inheriting the seccomp profile of the newest worker: pass it with --security-opt seccomp=FILE", "worker", string(container))
		default:
			extra = append(extra, opt)
		}
	}
	return extra
}

// nodeSecurityOpts returns the security options BuildRunArgs gives every
// node.
func nodeSecurityOpts() []string {
	args := BuildRunArgs(RunConfig{})
	var opts []string
	for i := range len(args) - 1 {
		if args[i] == "--security-opt" {
			opts = append(opts, args[i+1])
		}
	}
	return opts
}

// cleanupWorkers removes containers and mesh registrations for the given
// worker node configs. Used to roll back a failed WorkerAdd. It goes on past
// a failure, a container that is already gone is no failure, and it
// returns the failures joined.
func cleanupWorkers(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, realm, clusterName string, nodeConfigs []RunConfig) error {
	log := sindlog.From(ctx)
	var errs []error

	dnsNames := make([]string, len(nodeConfigs))
	for i, nc := range nodeConfigs {
		dnsNames[i] = DNSName(nc.ShortName, clusterName, meshMgr.Realm)
	}
	if err := meshMgr.RemoveDNSRecords(ctx, dnsNames); err != nil {
		log.DebugContext(ctx, "cleanup: removing DNS records", "error", err)
		errs = append(errs, fmt.Errorf("removing DNS records: %w", err))
	}
	if err := meshMgr.RemoveKnownHosts(ctx, dnsNames); err != nil {
		log.DebugContext(ctx, "cleanup: removing known hosts", "error", err)
		errs = append(errs, fmt.Errorf("removing known hosts: %w", err))
	}

	for _, nc := range nodeConfigs {
		containerName := ContainerName(realm, clusterName, nc.ShortName)
		logContainerDiagnostics(ctx, client, containerName)
		if err := client.RemoveContainer(ctx, containerName); err != nil && !docker.IsNotFound(err) {
			log.DebugContext(ctx, "cleanup: removing container", "node", nc.ShortName, "error", err)
			errs = append(errs, fmt.Errorf("removing container %s: %w", containerName, err))
		}
	}
	return errors.Join(errs...)
}
