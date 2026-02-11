// SPDX-License-Identifier: LGPL-3.0-or-later

// Package config handles parsing and validation of sind cluster configuration.
package config

import (
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

// Cluster represents a sind cluster configuration.
type Cluster struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Defaults Defaults `json:"defaults,omitempty"`
	Nodes    []Node   `json:"nodes,omitempty"`
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
