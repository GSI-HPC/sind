// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "github.com/GSI-HPC/sind/pkg/docker"

// State represents the state of a cluster or node.
type State string

// Possible cluster states.
const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StatePaused  State = "paused"
	StateUnknown State = "unknown"
)

// Cluster represents a live sind cluster as it exists in Docker.
// This is distinct from config.Cluster, which represents the configuration input.
type Cluster struct {
	Name         string
	SlurmVersion string
	State        State
	Nodes        []*Node
}

// Node represents a running node in a sind cluster.
type Node struct {
	Name        string             // short name: "controller", "worker-0"
	Role        string             // "controller", "submitter", "worker"
	ContainerID docker.ContainerID // Docker container ID
	IP          string             // container IP address
	State       State
}
