// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// WorkerAddOptions holds the parameters for adding compute workers to a cluster.
type WorkerAddOptions struct {
	ClusterName string
	Count       int
	Image       string
	CPUs        int
	Memory      string
	TmpSize     string
	Unmanaged   bool
}

// ValidateWorkerAdd checks prerequisites for adding workers to a cluster.
// For managed workers, it verifies that sind-nodes.conf exists on the
// controller (indicating sind-generated Slurm configuration is in use).
// Unmanaged workers bypass the sind-nodes.conf check.
func ValidateWorkerAdd(ctx context.Context, client *docker.Client, opts WorkerAddOptions) error {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return fmt.Errorf("listing cluster containers: %w", err)
	}

	controllerName := ContainerName(opts.ClusterName, "controller")
	found := false
	for _, c := range containers {
		if c.Name == controllerName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}

	if opts.Unmanaged {
		return nil
	}

	_, err = client.ReadFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf")
	if err != nil {
		return fmt.Errorf("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")
	}

	return nil
}
