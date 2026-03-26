// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
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
	if err := PreflightCheck(ctx, client, cfg); err != nil {
		return nil, err
	}

	dnsIP, sshPubKey, slurmVersion, err := resolveInfra(ctx, client, cfg)
	if err != nil {
		return nil, err
	}

	if err := createResources(ctx, client, cfg); err != nil {
		return nil, err
	}

	nodeConfigs := NodeRunConfigs(cfg, dnsIP, slurmVersion)
	if err := createAllNodes(ctx, client, nodeConfigs); err != nil {
		return nil, err
	}

	nodeResults, err := setupNodes(ctx, client, cfg.Name, sshPubKey, nodeConfigs, readinessInterval)
	if err != nil {
		return nil, err
	}

	cluster, err := registerMesh(ctx, meshMgr, cfg.Name, slurmVersion, nodeConfigs, nodeResults)
	if err != nil {
		return nil, err
	}

	if err := enableSlurm(ctx, client, cfg.Name, nodeConfigs, readinessInterval); err != nil {
		return nil, err
	}

	return cluster, nil
}

// resolveMeshInfra fetches DNS IP and SSH public key from mesh infrastructure
// concurrently.
func resolveMeshInfra(ctx context.Context, client *docker.Client) (dnsIP, sshPubKey string, err error) {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		info, err := client.InspectContainer(gctx, mesh.DNSContainerName)
		if err != nil {
			return fmt.Errorf("inspecting DNS container: %w", err)
		}
		dnsIP = info.IPs[mesh.NetworkName]
		return nil
	})
	g.Go(func() error {
		key, err := client.ReadFile(gctx, mesh.SSHContainerName, "/root/.ssh/id_ed25519.pub")
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
func resolveInfra(ctx context.Context, client *docker.Client, cfg *config.Cluster) (dnsIP, sshPubKey, slurmVersion string, err error) {
	image := controllerImage(cfg)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var meshErr error
		dnsIP, sshPubKey, meshErr = resolveMeshInfra(gctx, client)
		return meshErr
	})
	g.Go(func() error {
		ver, err := slurm.DiscoverVersion(gctx, client, image)
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
func createResources(ctx context.Context, client *docker.Client, cfg *config.Cluster) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return CreateClusterNetwork(gctx, client, cfg.Name) })
	g.Go(func() error { return CreateClusterVolumes(gctx, client, cfg.Name) })
	if err := g.Wait(); err != nil {
		return err
	}

	image := controllerImage(cfg)
	mungeKey := slurm.GenerateMungeKey()
	g, gctx = errgroup.WithContext(ctx)
	g.Go(func() error { return WriteClusterConfig(gctx, client, cfg, image) })
	g.Go(func() error { return WriteMungeKey(gctx, client, cfg.Name, mungeKey, image) })
	return g.Wait()
}

// createAllNodes creates and starts all node containers concurrently.
//
//	┌───────┐ ┌───────┐     ┌───────┐
//	│ node₁ │ │ node₂ │ ... │ nodeₙ │
//	└───┬───┘ └───┬───┘     └───┬───┘
//	    └─────────┼─────────────┘
func createAllNodes(ctx context.Context, client *docker.Client, nodeConfigs []RunConfig) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, nc := range nodeConfigs {
		nc := nc
		g.Go(func() error {
			_, err := CreateNode(gctx, client, nc)
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
func setupNodes(ctx context.Context, client *docker.Client, clusterName, sshPubKey string, nodeConfigs []RunConfig, interval time.Duration) ([]nodeResult, error) {
	baseProbes := []probe.Probe{
		{Name: "container", Check: probe.ContainerRunning},
		{Name: "systemd", Check: probe.SystemdReady},
		{Name: "sshd", Check: probe.SSHDReady},
	}
	results := make([]nodeResult, len(nodeConfigs))

	g, gctx := errgroup.WithContext(ctx)
	for i, nc := range nodeConfigs {
		i, nc := i, nc
		g.Go(func() error {
			containerName := ContainerName(clusterName, nc.ShortName)

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
		Status:       StatusRunning,
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
		nodeIP := nr.info.IPs[mesh.NetworkName]
		dnsName := DNSName(nc.ShortName, clusterName)

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
			IP:          nr.info.IPs[NetworkName(clusterName)],
			Status:      StatusRunning,
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
func enableSlurm(ctx context.Context, client *docker.Client, clusterName string, nodeConfigs []RunConfig, interval time.Duration) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, nc := range nodeConfigs {
		nc := nc
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
			containerName := ContainerName(clusterName, nc.ShortName)
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
func CreateClusterNetwork(ctx context.Context, client *docker.Client, clusterName string) error {
	labels := map[string]string{
		LabelComposeProject: ComposeProject(clusterName),
		LabelComposeNetwork: "net",
	}
	_, err := client.CreateNetwork(ctx, NetworkName(clusterName), labels)
	if err != nil {
		return fmt.Errorf("creating cluster network: %w", err)
	}
	return nil
}

// CreateClusterVolumes creates the config, munge, and data volumes for a cluster.
func CreateClusterVolumes(ctx context.Context, client *docker.Client, clusterName string) error {
	for _, vtype := range []string{"config", "munge", "data"} {
		labels := map[string]string{
			LabelComposeProject: ComposeProject(clusterName),
			LabelComposeVolume:  vtype,
		}
		if err := client.CreateVolume(ctx, VolumeName(clusterName, vtype), labels); err != nil {
			return fmt.Errorf("creating %s volume: %w", vtype, err)
		}
	}
	return nil
}

// WriteClusterConfig generates and writes slurm.conf, sind-nodes.conf, and
// cgroup.conf to the config volume. Uses a temporary container to access the
// volume.
func WriteClusterConfig(ctx context.Context, client *docker.Client, cfg *config.Cluster, image string) error {
	helperName := ContainerName(cfg.Name, "config-helper")
	volName := VolumeName(cfg.Name, "config")

	_, err := client.CreateContainer(ctx,
		"--name", string(helperName),
		"-v", string(volName)+":/etc/slurm",
		image,
	)
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
func WriteMungeKey(ctx context.Context, client *docker.Client, clusterName string, key []byte, image string) error {
	helperName := ContainerName(clusterName, "munge-helper")
	volName := VolumeName(clusterName, "munge")

	_, err := client.RunContainer(ctx,
		"--name", string(helperName),
		"-v", string(volName)+":/etc/munge",
		image,
		"sleep", "30",
	)
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
func NodeRunConfigs(cfg *config.Cluster, dnsIP, slurmVersion string) []RunConfig {
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
			})
		case "worker":
			count := n.Count
			if count <= 0 {
				count = 1
			}
			isManaged := n.Managed == nil || *n.Managed
			for i := 0; i < count; i++ {
				configs = append(configs, RunConfig{
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
				})
				workerIdx++
			}
		}
	}
	return configs
}

// CreateClusterNodes creates all node containers for the cluster.
// Each node is created, connected to the mesh network, and started.
func CreateClusterNodes(ctx context.Context, client *docker.Client, configs []RunConfig) error {
	for _, cfg := range configs {
		_, err := CreateNode(ctx, client, cfg)
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

		containerName := ContainerName(cfg.ClusterName, cfg.ShortName)
		_, err := client.Exec(ctx, containerName, "systemctl", "enable", "--now", service)
		if err != nil {
			return fmt.Errorf("enabling %s on %s: %w", service, cfg.ShortName, err)
		}
	}
	return nil
}
