// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/GSI-HPC/sind/pkg/ssh"
	"golang.org/x/sync/errgroup"
)

// controllerImage returns the image configured for the controller node.
func controllerImage(cfg *config.Cluster) string {
	for _, n := range cfg.Nodes {
		if n.Role == "controller" {
			return n.Image
		}
	}
	return config.DefaultImage
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
//	createResources     network ║ volumes → config ║ munge
//	      │
//	createAllNodes      node₁ ║ node₂ ║ ... ║ nodeₙ
//	      │
//	setupNodes          (wait + SSH + hostkey) per node
//	      │
//	registerMesh        DNS records + known_hosts (serial)
//	      │
//	enableSlurm         (enable + probe) per eligible node
//	      │
//	  *Cluster
func Create(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, cfg *config.Cluster, readinessInterval time.Duration) (*Cluster, error) {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	log.InfoContext(ctx, "creating cluster", "name", cfg.Name, "nodes", len(cfg.Nodes))

	if err := PreflightCheck(ctx, client, realm, cfg); err != nil {
		return nil, err
	}
	log.DebugContext(ctx, "preflight check passed")

	dnsIP, sshPubKey, slurmVersion, err := resolveInfra(ctx, client, meshMgr, cfg)
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "resolved infrastructure", "slurm", slurmVersion)

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
	if err := createAllNodes(ctx, client, meshMgr, nodeConfigs); err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "node containers started", "count", len(nodeConfigs))

	nodeResults, err := setupNodes(ctx, client, realm, cfg.Name, sshPubKey, nodeConfigs, readinessInterval)
	if err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "nodes ready", "count", len(nodeConfigs))

	cluster, err := registerMesh(ctx, meshMgr, cfg.Name, slurmVersion, nodeConfigs, nodeResults)
	if err != nil {
		return nil, err
	}
	log.DebugContext(ctx, "mesh registration complete")

	if err := enableSlurm(ctx, client, realm, cfg.Name, nodeConfigs, readinessInterval); err != nil {
		return nil, err
	}
	log.InfoContext(ctx, "slurm services enabled")

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
//	│ network │  │ volumes │
//	└────┬────┘  └────┬────┘
//	     │       ┌────┴────┐
//	     │  ┌────┴───┐ ┌───┴────┐
//	     │  │ config │ │  munge │
//	     │  └────┬───┘ └───┬────┘
//	     └───────┼─────────┘
func createResources(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster) error {
	useDataVolume := cfg.Storage.DataStorage.HostPath == ""
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return CreateClusterNetwork(gctx, client, realm, cfg.Name) })
	g.Go(func() error { return CreateClusterVolumes(gctx, client, realm, cfg.Name, useDataVolume) })
	if err := g.Wait(); err != nil {
		return err
	}

	image := controllerImage(cfg)
	mungeKey := slurm.GenerateMungeKey()
	g, gctx = errgroup.WithContext(ctx)
	g.Go(func() error { return WriteClusterConfig(gctx, client, realm, cfg, image, cfg.Pull) })
	g.Go(func() error { return WriteMungeKey(gctx, client, realm, cfg.Name, mungeKey, image, cfg.Pull) })
	return g.Wait()
}

// createAllNodes creates and starts all node containers concurrently.
//
//	┌───────┐ ┌───────┐     ┌───────┐
//	│ node₁ │ │ node₂ │ ... │ nodeₙ │
//	└───┬───┘ └───┬───┘     └───┬───┘
//	    └─────────┼─────────────┘
func createAllNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, nodeConfigs []RunConfig) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, nc := range nodeConfigs {
		g.Go(func() error {
			_, err := CreateNode(gctx, client, meshMgr, nc)
			if err != nil {
				return fmt.Errorf("node %s: %w", nc.ShortName, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// setupNodes waits for base readiness, injects SSH public keys, and collects
// host keys from each node — all concurrently.
//
//	per node:  wait(container, systemd, sshd) → inspect → SSH inject → host key
//
//	┌───────────────┐ ┌───────────────┐     ┌───────────────┐
//	│ node₁ setup   │ │ node₂ setup   │ ... │ nodeₙ setup   │
//	└───────┬───────┘ └───────┬───────┘     └───────┬───────┘
//	        └─────────────────┼─────────────────────┘
func setupNodes(ctx context.Context, client *docker.Client, realm, clusterName, sshPubKey string, nodeConfigs []RunConfig, interval time.Duration) ([]nodeResult, error) {
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

			log.DebugContext(gctx, "waiting for node", "node", nc.ShortName)
			if err := probe.UntilReady(gctx, client, containerName, baseProbes, interval); err != nil {
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

// registerMesh writes DNS records and known hosts for all nodes, and builds
// the Cluster result. Serial because AddDNSRecord is a read-modify-write
// on the shared Corefile.
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

// registerNodes registers DNS records and known_hosts entries for each node,
// and returns the resulting Node list. Serial because AddDNSRecord is a
// read-modify-write on the shared Corefile.
func registerNodes(ctx context.Context, meshMgr *mesh.Manager, clusterName string, nodeConfigs []RunConfig, results []nodeResult) ([]*Node, error) {
	nodes := make([]*Node, 0, len(nodeConfigs))
	for i, nc := range nodeConfigs {
		nr := results[i]
		nodeIP := nr.info.IPs[NetworkName(meshMgr.Realm, clusterName)]
		dnsName := DNSName(nc.ShortName, clusterName, meshMgr.Realm)

		if err := meshMgr.AddDNSRecord(ctx, dnsName, nodeIP); err != nil {
			return nil, fmt.Errorf("registering DNS for %s: %w", nc.ShortName, err)
		}
		if err := meshMgr.AddKnownHost(ctx, dnsName, nr.hostKey); err != nil {
			return nil, fmt.Errorf("registering host key for %s: %w", nc.ShortName, err)
		}

		nodes = append(nodes, &Node{
			Name:        nc.ShortName,
			Role:        nc.Role,
			ContainerID: nr.info.ID,
			IP:          nr.info.IPs[NetworkName(meshMgr.Realm, clusterName)],
			State:       StateRunning,
		})
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
func enableSlurm(ctx context.Context, client *docker.Client, realm, clusterName string, nodeConfigs []RunConfig, interval time.Duration) error {
	log := sindlog.From(ctx)
	g, gctx := errgroup.WithContext(ctx)
	for _, nc := range nodeConfigs {
		var service string
		var slurmProbe probe.Probe
		switch nc.Role {
		case "controller":
			service = "slurmctld"
			slurmProbe = probe.Probe{Name: "slurmctld", Check: probe.SlurmctldReady}
		case "worker":
			if !nc.Managed {
				continue
			}
			service = "slurmd"
			slurmProbe = probe.Probe{Name: "slurmd", Check: probe.SlurmdReady}
		default:
			continue
		}
		g.Go(func() error {
			containerName := ContainerName(realm, clusterName, nc.ShortName)
			log.DebugContext(gctx, "enabling slurm service", "node", nc.ShortName, "service", service)
			_, err := client.Exec(gctx, containerName, "systemctl", "enable", "--now", service)
			if err != nil {
				return fmt.Errorf("enabling %s on %s: %w", service, nc.ShortName, err)
			}
			return probe.UntilReady(gctx, client, containerName, []probe.Probe{slurmProbe}, interval)
		})
	}
	return g.Wait()
}

// CreateClusterNetwork creates the cluster-specific Docker bridge network.
func CreateClusterNetwork(ctx context.Context, client *docker.Client, realm, clusterName string) error {
	labels := map[string]string{
		LabelComposeProject: ComposeProject(realm, clusterName),
		LabelComposeNetwork: "net",
	}
	_, err := client.CreateNetwork(ctx, NetworkName(realm, clusterName), labels)
	if err != nil {
		return fmt.Errorf("creating cluster network: %w", err)
	}
	return nil
}

// CreateClusterVolumes creates the config and munge volumes for a cluster.
// When useDataVolume is true, a data volume is also created; otherwise the
// caller is expected to use a host-path bind mount for /data.
func CreateClusterVolumes(ctx context.Context, client *docker.Client, realm, clusterName string, useDataVolume bool) error {
	types := []string{"config", "munge"}
	if useDataVolume {
		types = append(types, "data")
	}
	for _, vtype := range types {
		labels := map[string]string{
			LabelComposeProject: ComposeProject(realm, clusterName),
			LabelComposeVolume:  vtype,
		}
		if err := client.CreateVolume(ctx, VolumeName(realm, clusterName, vtype), labels); err != nil {
			return fmt.Errorf("creating %s volume: %w", vtype, err)
		}
	}
	return nil
}

// WriteClusterConfig generates and writes slurm.conf, sind-nodes.conf, and
// cgroup.conf to the config volume. Uses a temporary container to access the
// volume.
func WriteClusterConfig(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster, image string, pull bool) error {
	helperName := ContainerName(realm, cfg.Name, "config-helper")
	volName := VolumeName(realm, cfg.Name, "config")

	args := []string{
		"--name", string(helperName),
		"-v", string(volName) + ":/etc/slurm",
	}
	if pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, image)
	_, err := client.CreateContainer(ctx, args...)
	if err != nil {
		return fmt.Errorf("creating config helper container: %w", err)
	}
	defer client.RemoveContainer(ctx, helperName) //nolint:errcheck

	files := map[string][]byte{
		"slurm.conf":      []byte(slurm.GenerateSlurmConf(cfg.Name)),
		"sind-nodes.conf": []byte(slurm.GenerateNodesConf(cfg.Nodes)),
		"cgroup.conf":     []byte(slurm.GenerateCgroupConf()),
	}

	err = client.CopyToContainer(ctx, helperName, "/etc/slurm", files)
	if err != nil {
		return fmt.Errorf("writing slurm config: %w", err)
	}

	return nil
}

// WriteMungeKey writes the given munge key to the munge volume.
// Uses a temporary container to access the volume.
func WriteMungeKey(ctx context.Context, client *docker.Client, realm, clusterName string, key []byte, image string, pull bool) error {
	helperName := ContainerName(realm, clusterName, "munge-helper")
	volName := VolumeName(realm, clusterName, "munge")

	args := []string{
		"--name", string(helperName),
		"-v", string(volName) + ":/etc/munge",
	}
	if pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, image, "sleep", "30")
	_, err := client.RunContainer(ctx, args...)
	if err != nil {
		return fmt.Errorf("creating munge helper container: %w", err)
	}
	defer func() {
		_ = client.KillContainer(ctx, helperName)
		_ = client.RemoveContainer(ctx, helperName)
	}()

	err = client.CopyToContainer(ctx, helperName, "/etc/munge", map[string][]byte{
		"munge.key": key,
	})
	if err != nil {
		return fmt.Errorf("writing munge key: %w", err)
	}

	// docker cp creates files as root; munge requires ownership by the munge user.
	_, err = client.Exec(ctx, helperName, "chown", "munge:munge", "/etc/munge/munge.key")
	if err != nil {
		return fmt.Errorf("fixing munge key ownership: %w", err)
	}
	_, err = client.Exec(ctx, helperName, "chmod", "0400", "/etc/munge/munge.key")
	if err != nil {
		return fmt.Errorf("fixing munge key permissions: %w", err)
	}

	return nil
}

// NodeRunConfigs builds RunConfig entries for all nodes in the cluster config.
// Worker nodes are indexed sequentially across all worker groups.
func NodeRunConfigs(cfg *config.Cluster, realm, dnsIP, slurmVersion string) []RunConfig {
	var configs []RunConfig
	workerIdx := 0

	dataHostPath := ""
	dataMountPath := ""
	if cfg.Storage.DataStorage.Type == "hostPath" {
		dataHostPath = cfg.Storage.DataStorage.HostPath
	}
	if cfg.Storage.DataStorage.MountPath != "" {
		dataMountPath = cfg.Storage.DataStorage.MountPath
	}

	for _, n := range cfg.Nodes {
		switch n.Role {
		case "controller", "submitter":
			configs = append(configs, RunConfig{
				Realm:           realm,
				ClusterName:     cfg.Name,
				ShortName:       n.Role,
				Role:            n.Role,
				Image:           n.Image,
				CPUs:            n.CPUs,
				Memory:          n.Memory,
				TmpSize:         n.TmpSize,
				SlurmVersion:    slurmVersion,
				DNSIP:           dnsIP,
				DataHostPath:    dataHostPath,
				DataMountPath:   dataMountPath,
				ContainerNumber: 1,
				Pull:            cfg.Pull,
			})
		case "worker":
			count := n.Count
			if count <= 0 {
				count = 1
			}
			isManaged := n.Managed == nil || *n.Managed
			for i := 0; i < count; i++ {
				configs = append(configs, RunConfig{
					Realm:           realm,
					ClusterName:     cfg.Name,
					ShortName:       fmt.Sprintf("worker-%d", workerIdx),
					Role:            "worker",
					Image:           n.Image,
					CPUs:            n.CPUs,
					Memory:          n.Memory,
					TmpSize:         n.TmpSize,
					SlurmVersion:    slurmVersion,
					DNSIP:           dnsIP,
					DataHostPath:    dataHostPath,
					DataMountPath:   dataMountPath,
					Managed:         isManaged,
					ContainerNumber: workerIdx + 1,
					Pull:            cfg.Pull,
				})
				workerIdx++
			}
		}
	}
	return configs
}

// CreateClusterNodes creates all node containers for the cluster.
// Each node is created, connected to the mesh network, and started.
func CreateClusterNodes(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, configs []RunConfig) error {
	for _, cfg := range configs {
		_, err := CreateNode(ctx, client, meshMgr, cfg)
		if err != nil {
			return fmt.Errorf("node %s: %w", cfg.ShortName, err)
		}
	}
	return nil
}

// EnableSlurmServices enables the role-appropriate Slurm daemon on each node.
// Controller nodes get slurmctld; managed worker nodes get slurmd.
// Submitter and unmanaged worker nodes are skipped.
func EnableSlurmServices(ctx context.Context, client *docker.Client, configs []RunConfig) error {
	for _, cfg := range configs {
		var service string
		switch cfg.Role {
		case "controller":
			service = "slurmctld"
		case "worker":
			if !cfg.Managed {
				continue
			}
			service = "slurmd"
		default:
			continue
		}

		containerName := ContainerName(cfg.Realm, cfg.ClusterName, cfg.ShortName)
		_, err := client.Exec(ctx, containerName, "systemctl", "enable", "--now", service)
		if err != nil {
			return fmt.Errorf("enabling %s on %s: %w", service, cfg.ShortName, err)
		}
	}
	return nil
}
