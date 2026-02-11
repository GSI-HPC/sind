// SPDX-License-Identifier: LGPL-3.0-or-later

// Package config handles parsing and validation of sind cluster configuration.
package config

import (
	"encoding/json"
	"fmt"

	"sigs.k8s.io/yaml"
)

// Defaults holds default settings applied to all nodes unless overridden.
type Defaults struct {
	Image   string `json:"image,omitempty"`
	CPUs    int    `json:"cpus,omitempty"`
	Memory  string `json:"memory,omitempty"`
	TmpSize string `json:"tmpSize,omitempty"`
}

// Node represents a single node or node group in the cluster configuration.
type Node struct {
	Role    string `json:"role"`
	Count   int    `json:"count,omitempty"`
	Image   string `json:"image,omitempty"`
	CPUs    int    `json:"cpus,omitempty"`
	Memory  string `json:"memory,omitempty"`
	TmpSize string `json:"tmpSize,omitempty"`
	Managed *bool  `json:"managed,omitempty"`
}

// UnmarshalJSON supports three YAML forms:
//   - bare string: "controller"
//   - shorthand map: "compute: 3"  (role: count)
//   - full object: "role: compute\n  count: 3\n  cpus: 4"
func (n *Node) UnmarshalJSON(data []byte) error {
	// Try bare string: "controller"
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		n.Role = s
		return nil
	}

	// Try object form
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("node must be a string, role:count map, or full object: %w", err)
	}

	// If "role" key exists, it's the full form
	if _, ok := raw["role"]; ok {
		type nodeAlias Node // prevents infinite recursion
		var alias nodeAlias
		if err := json.Unmarshal(data, &alias); err != nil {
			return err
		}
		*n = Node(alias)
		return nil
	}

	// Shorthand map: {"compute": 3}
	if len(raw) != 1 {
		return fmt.Errorf("shorthand node must have exactly one key (role), got %d", len(raw))
	}
	for role, countRaw := range raw {
		n.Role = role
		var count int
		if err := json.Unmarshal(countRaw, &count); err != nil {
			return fmt.Errorf("shorthand node count for %q: %w", role, err)
		}
		n.Count = count
	}
	return nil
}

// DataStorage configures the shared data volume.
type DataStorage struct {
	Type      string `json:"type,omitempty"`
	HostPath  string `json:"hostPath,omitempty"`
	MountPath string `json:"mountPath,omitempty"`
}

// Storage configures cluster storage options.
type Storage struct {
	DataStorage DataStorage `json:"dataStorage,omitempty"`
}

// Cluster represents a sind cluster configuration.
type Cluster struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Defaults Defaults `json:"defaults,omitempty"`
	Storage  Storage  `json:"storage,omitempty"`
	Nodes    []Node   `json:"nodes,omitempty"`
}

var validRoles = map[string]bool{
	"controller": true,
	"submitter":  true,
	"compute":    true,
}

// Validate checks that the cluster configuration satisfies all constraints.
// It should be called after ApplyDefaults.
func (c *Cluster) Validate() error {
	var controllers, submitters, computes int
	for _, n := range c.Nodes {
		if !validRoles[n.Role] {
			return fmt.Errorf("invalid role %q, must be one of: controller, submitter, compute", n.Role)
		}

		switch n.Role {
		case "controller":
			controllers++
		case "submitter":
			submitters++
		case "compute":
			computes++
		}

		if n.Count > 0 && n.Role != "compute" {
			return fmt.Errorf("count is only valid for compute nodes, not %q", n.Role)
		}
		if n.Managed != nil && n.Role != "compute" {
			return fmt.Errorf("managed is only valid for compute nodes, not %q", n.Role)
		}
	}

	if controllers != 1 {
		return fmt.Errorf("exactly one controller required, got %d", controllers)
	}
	if submitters > 1 {
		return fmt.Errorf("at most one submitter allowed, got %d", submitters)
	}
	if computes < 1 {
		return fmt.Errorf("at least one compute node required, got %d", computes)
	}

	return nil
}

// Parse parses a YAML cluster configuration and returns a Cluster.
func Parse(data []byte) (*Cluster, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty configuration")
	}

	var cfg Cluster
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Kind != "Cluster" {
		return nil, fmt.Errorf(`kind must be "Cluster", got %q`, cfg.Kind)
	}

	if cfg.Name == "" {
		cfg.Name = "default"
	}

	return &cfg, nil
}
