// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"golang.org/x/sync/errgroup"
)

// NodeShortNames returns the short hostname for each node defined in the config.
// Worker nodes are indexed sequentially across all worker groups, matching
// the indexing used in slurm.GenerateNodesConf.
func NodeShortNames(nodes []config.Node) []string {
	var names []string
	workerIdx := 0
	for _, n := range nodes {
		switch n.Role {
		case config.RoleController, config.RoleSubmitter:
			names = append(names, string(n.Role))
		case config.RoleWorker:
			count := n.Count
			if count <= 0 {
				count = 1
			}
			for i := 0; i < count; i++ {
				names = append(names, fmt.Sprintf("worker-%d", workerIdx))
				workerIdx++
			}
		}
	}
	return names
}

// PreflightCheck verifies that no Docker resources conflict with the cluster
// that would be created from the given configuration. It checks for existing
// networks, volumes, and containers with matching names.
//
// Container existence is checked with a single `docker ps` filtered by the
// cluster's realm + name labels; this keeps the call count constant regardless
// of node count.
func PreflightCheck(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster) error {
	var (
		mu        sync.Mutex
		conflicts []string
	)

	check := func(ctx context.Context, kind, name string, existsFn func(context.Context) (bool, error)) error {
		exists, err := existsFn(ctx)
		if err != nil {
			return fmt.Errorf("checking %s %s: %w", kind, name, err)
		}
		if exists {
			mu.Lock()
			conflicts = append(conflicts, kind+" "+name)
			mu.Unlock()
		}
		return nil
	}

	g, gctx := errgroup.WithContext(ctx)

	// Check cluster network.
	netName := NetworkName(realm, cfg.Name)
	g.Go(func() error {
		return check(gctx, "network", string(netName), func(ctx context.Context) (bool, error) {
			return client.NetworkExists(ctx, netName)
		})
	})

	// Check cluster volumes.
	for _, vtype := range AllVolumeTypes {
		volName := VolumeName(realm, cfg.Name, vtype)
		g.Go(func() error {
			return check(gctx, "volume", string(volName), func(ctx context.Context) (bool, error) {
				return client.VolumeExists(ctx, volName)
			})
		})
	}

	// Check node containers with a single filtered listing instead of N inspects.
	g.Go(func() error {
		entries, err := client.ListContainers(gctx,
			"label="+LabelRealm+"="+realm,
			"label="+LabelCluster+"="+cfg.Name)
		if err != nil {
			return fmt.Errorf("listing cluster containers: %w", err)
		}
		existing := make(map[docker.ContainerName]struct{}, len(entries))
		for _, e := range entries {
			existing[e.Name] = struct{}{}
		}
		for _, shortName := range NodeShortNames(cfg.Nodes) {
			name := ContainerName(realm, cfg.Name, shortName)
			if _, ok := existing[name]; ok {
				mu.Lock()
				conflicts = append(conflicts, "container "+string(name))
				mu.Unlock()
			}
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("conflicting resources already exist: %s", strings.Join(conflicts, ", "))
	}

	return nil
}
