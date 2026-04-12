// SPDX-License-Identifier: LGPL-3.0-or-later

// Package config handles parsing and validation of sind cluster configuration.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Defaults holds default settings applied to all nodes unless overridden.
type Defaults struct {
	Image       string   `json:"image,omitempty"`
	CPUs        int      `json:"cpus,omitempty"`
	Memory      string   `json:"memory,omitempty"`
	TmpSize     string   `json:"tmpSize,omitempty"`
	CapAdd      []string `json:"capAdd,omitempty"`
	CapDrop     []string `json:"capDrop,omitempty"`
	Devices     []string `json:"devices,omitempty"`
	SecurityOpt []string `json:"securityOpt,omitempty"`
}

// Role identifies the function of a node within a cluster.
type Role string

// Valid node roles.
const (
	RoleController Role = "controller"
	RoleDb         Role = "db"
	RoleSubmitter  Role = "submitter"
	RoleWorker     Role = "worker"
)

// Node represents a single node or node group in the cluster configuration.
type Node struct {
	Role        Role     `json:"role"`
	Count       int      `json:"count,omitempty"`
	Image       string   `json:"image,omitempty"`
	CPUs        int      `json:"cpus,omitempty"`
	Memory      string   `json:"memory,omitempty"`
	TmpSize     string   `json:"tmpSize,omitempty"`
	Managed     *bool    `json:"managed,omitempty"`
	CapAdd      []string `json:"capAdd,omitempty"`
	CapDrop     []string `json:"capDrop,omitempty"`
	Devices     []string `json:"devices,omitempty"`
	SecurityOpt []string `json:"securityOpt,omitempty"`
}

// UnmarshalJSON supports three YAML forms:
//   - bare string: "controller"
//   - shorthand map: "worker: 3"  (role: count)
//   - full object: "role: worker\n  count: 3\n  cpus: 4"
func (n *Node) UnmarshalJSON(data []byte) error {
	// Try bare string: "controller"
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		n.Role = Role(s)
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
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&alias); err != nil {
			return err
		}
		*n = Node(alias)
		return nil
	}

	// Shorthand map: {"worker": 3}
	if len(raw) != 1 {
		return fmt.Errorf("shorthand node must have exactly one key (role), got %d", len(raw))
	}
	for role, countRaw := range raw {
		n.Role = Role(role)
		var count int
		if err := json.Unmarshal(countRaw, &count); err != nil {
			return fmt.Errorf("shorthand node count for %q: %w", role, err)
		}
		n.Count = count
	}
	return nil
}

// StorageType identifies the backing mechanism for data storage.
type StorageType string

// Storage type values.
const (
	StorageVolume   StorageType = "volume"
	StorageHostPath StorageType = "hostPath"
)

// DataStorage configures the shared data volume.
type DataStorage struct {
	Type      StorageType `json:"type,omitempty"`
	HostPath  string      `json:"hostPath,omitempty"`
	MountPath string      `json:"mountPath,omitempty"`
}

// Storage configures cluster storage options.
type Storage struct {
	DataStorage DataStorage `json:"dataStorage,omitempty"`
}

// Section represents a Slurm config file section that can be either:
//   - a string (content appended directly to the config file)
//   - a map of fragment name → content (creates a .conf.d/ directory)
type Section struct {
	Content   string            // string form
	Fragments map[string]string // map form
}

// IsEmpty returns true if the section has no content and no fragments.
func (s Section) IsEmpty() bool {
	return s.Content == "" && len(s.Fragments) == 0
}

// IsMap returns true if the section uses the map/fragments form.
func (s Section) IsMap() bool {
	return len(s.Fragments) > 0
}

// FragmentNames returns the sorted keys from Fragments.
func (s Section) FragmentNames() []string {
	if len(s.Fragments) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.Fragments))
	for k := range s.Fragments {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// UnmarshalJSON supports two YAML/JSON forms:
//   - string: "content" → Section{Content: "content"}
//   - map: {"key": "content"} → Section{Fragments: {"key": "content"}}
func (s *Section) UnmarshalJSON(data []byte) error {
	// Try string form (also handles null → empty string)
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Content = str
		return nil
	}

	// Try map form
	var m map[string]string
	if err := json.Unmarshal(data, &m); err == nil {
		s.Fragments = m
		return nil
	}

	return fmt.Errorf("section must be a string or map of fragments")
}

// Slurm configures custom Slurm configuration files.
// Each field maps to a Slurm config file: main → slurm.conf,
// cgroup → cgroup.conf, gres → gres.conf, etc.
type Slurm struct {
	Main      Section `json:"main,omitempty"`
	Cgroup    Section `json:"cgroup,omitempty"`
	Gres      Section `json:"gres,omitempty"`
	Topology  Section `json:"topology,omitempty"`
	Plugstack Section `json:"plugstack,omitempty"`
	Slurmdbd  Section `json:"slurmdbd,omitempty"`
}

// Cluster represents a sind cluster configuration.
type Cluster struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name,omitempty"`
	Realm    string   `json:"realm,omitempty"`
	Defaults Defaults `json:"defaults,omitempty"`
	Storage  Storage  `json:"storage,omitempty"`
	Slurm    Slurm    `json:"slurm,omitempty"`
	Nodes    []Node   `json:"nodes,omitempty"`

	// Pull is a runtime flag (not part of the config file) that forces
	// fresh image pulls when creating containers.
	Pull bool `json:"-" yaml:"-"`
}

// Default resource values for cluster nodes.
const (
	DefaultClusterName = "default"
	DefaultImage       = "ghcr.io/gsi-hpc/sind-node:latest"
	DefaultCPUs        = 1
	DefaultMemory      = "512m"
	DefaultTmpSize     = "256m"
)

// ApplyDefaults populates missing fields with defaults.
// If no nodes are defined, creates a minimal cluster (1 controller + 1 worker).
// Node-level fields inherit from the Defaults section, which in turn falls back
// to built-in defaults.
func (c *Cluster) ApplyDefaults() {
	if len(c.Nodes) == 0 {
		c.Nodes = []Node{
			{Role: RoleController},
			{Role: RoleWorker},
		}
	}

	// Resolve effective defaults: user defaults → built-in defaults
	image := c.Defaults.Image
	if image == "" {
		image = DefaultImage
	}
	cpus := c.Defaults.CPUs
	if cpus == 0 {
		cpus = DefaultCPUs
	}
	memory := c.Defaults.Memory
	if memory == "" {
		memory = DefaultMemory
	}
	tmpSize := c.Defaults.TmpSize
	if tmpSize == "" {
		tmpSize = DefaultTmpSize
	}

	// Apply to each node where not already set
	for i := range c.Nodes {
		if c.Nodes[i].Image == "" {
			c.Nodes[i].Image = image
		}
		if c.Nodes[i].CPUs == 0 {
			c.Nodes[i].CPUs = cpus
		}
		if c.Nodes[i].Memory == "" {
			c.Nodes[i].Memory = memory
		}
		if c.Nodes[i].TmpSize == "" {
			c.Nodes[i].TmpSize = tmpSize
		}
		// Security fields: per-node values merge with (not replace) defaults
		c.Nodes[i].CapAdd = mergeStringSlices(c.Defaults.CapAdd, c.Nodes[i].CapAdd)
		c.Nodes[i].CapDrop = mergeStringSlices(c.Defaults.CapDrop, c.Nodes[i].CapDrop)
		c.Nodes[i].Devices = mergeStringSlices(c.Defaults.Devices, c.Nodes[i].Devices)
		c.Nodes[i].SecurityOpt = mergeStringSlices(c.Defaults.SecurityOpt, c.Nodes[i].SecurityOpt)
	}
}

// mergeStringSlices merges two string slices, deduplicating entries.
// Returns nil if both inputs are empty.
func mergeStringSlices(base, overlay []string) []string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(base)+len(overlay))
	var result []string
	for _, s := range base {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	for _, s := range overlay {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// Validate checks that the cluster configuration satisfies all constraints.
// It should be called after ApplyDefaults.
func (c *Cluster) Validate() error {
	var controllers, dbs, submitters, workers int
	for _, n := range c.Nodes {
		switch n.Role {
		case RoleController:
			controllers++
		case RoleDb:
			dbs++
		case RoleSubmitter:
			submitters++
		case RoleWorker:
			workers++
		default:
			return fmt.Errorf("invalid role %q, must be one of: controller, db, submitter, worker", n.Role)
		}

		if n.Count < 0 {
			return fmt.Errorf("count must not be negative, got %d", n.Count)
		}
		if n.Count > 0 && n.Role != RoleWorker {
			return fmt.Errorf("count is only valid for worker nodes, not %q", n.Role)
		}
		if n.Managed != nil && n.Role != RoleWorker {
			return fmt.Errorf("managed is only valid for worker nodes, not %q", n.Role)
		}
	}

	if controllers != 1 {
		return fmt.Errorf("exactly one controller required, got %d", controllers)
	}
	if dbs > 1 {
		return fmt.Errorf("at most one db node allowed, got %d", dbs)
	}
	if submitters > 1 {
		return fmt.Errorf("at most one submitter allowed, got %d", submitters)
	}
	if workers < 1 {
		return fmt.Errorf("at least one worker node required, got %d", workers)
	}

	for _, n := range c.Nodes {
		for _, cap := range n.CapAdd {
			if !isValidCapability(cap) {
				return fmt.Errorf("unknown capability %q in capAdd", cap)
			}
		}
		for _, cap := range n.CapDrop {
			if !isValidCapability(cap) {
				return fmt.Errorf("unknown capability %q in capDrop", cap)
			}
		}
		for _, dev := range n.Devices {
			hostDev := strings.SplitN(dev, ":", 2)[0]
			if !strings.HasPrefix(hostDev, "/") {
				return fmt.Errorf("device path must be absolute, got %q", dev)
			}
		}
	}

	sections := []struct {
		name    string
		section Section
	}{
		{"main", c.Slurm.Main},
		{"cgroup", c.Slurm.Cgroup},
		{"gres", c.Slurm.Gres},
		{"topology", c.Slurm.Topology},
		{"plugstack", c.Slurm.Plugstack},
		{"slurmdbd", c.Slurm.Slurmdbd},
	}
	for _, s := range sections {
		if err := validateSection(s.name, s.section); err != nil {
			return err
		}
	}

	return nil
}

// validateSection checks that a Slurm config section is valid.
func validateSection(sectionName string, s Section) error {
	for name, content := range s.Fragments {
		if name == "" {
			return fmt.Errorf("slurm %s fragment name must not be empty", sectionName)
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("slurm %s fragment name %q must be a plain filename without path separators", sectionName, name)
		}
		if content == "" {
			return fmt.Errorf("slurm %s fragment %q must not be empty", sectionName, name)
		}
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
		cfg.Name = DefaultClusterName
	}

	return &cfg, nil
}
