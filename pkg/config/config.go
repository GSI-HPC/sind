// SPDX-License-Identifier: LGPL-3.0-or-later

// Package config handles parsing and validation of sind cluster configuration.
package config

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// ClusterConfig represents a sind cluster configuration.
type ClusterConfig struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Parse parses a YAML cluster configuration and returns a ClusterConfig.
func Parse(data []byte) (*ClusterConfig, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty configuration")
	}

	var cfg ClusterConfig
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
