// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "github.com/GSI-HPC/sind/pkg/docker"

// Status represents the state of a cluster or node.
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusPaused  Status = "paused"
	StatusUnknown Status = "unknown"
)

// Cluster represents a live sind cluster as it exists in Docker.
// This is distinct from config.Cluster, which represents the configuration input.
type Cluster struct {
	Name         string
	SlurmVersion string
	Status       Status
	Nodes        []*Node
}

// Node represents a running node in a sind cluster.
type Node struct {
	Name        string             // short name: "controller", "compute-0"
	Role        string             // "controller", "submitter", "compute"
	ContainerID docker.ContainerID // Docker container ID
	IP          string             // container IP address
	Status      Status
}
