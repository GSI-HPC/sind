// SPDX-License-Identifier: LGPL-3.0-or-later

// Package config handles parsing and validation of sind cluster configuration.
package config

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
	RoleDB         Role = "db"
	RoleSubmitter  Role = "submitter"
	RoleWorker     Role = "worker"
	// RoleAPI is the node that runs slurmrestd, Slurm's REST API daemon.
	RoleAPI Role = "api"
)

// Node represents a single node or node group in the cluster configuration.
type Node struct {
	Role             Role     `json:"role"`
	Count            int      `json:"count,omitempty"`
	Image            string   `json:"image,omitempty"`
	CPUs             int      `json:"cpus,omitempty"`
	Memory           string   `json:"memory,omitempty"`
	TmpSize          string   `json:"tmpSize,omitempty"`
	Managed          *bool    `json:"managed,omitempty"`
	BackupController bool     `json:"backupController,omitempty"`
	CapAdd           []string `json:"capAdd,omitempty"`
	CapDrop          []string `json:"capDrop,omitempty"`
	Devices          []string `json:"devices,omitempty"`
	SecurityOpt      []string `json:"securityOpt,omitempty"`
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

// UsesHostPath reports whether the data is a bind mount of HostPath rather
// than the cluster's Docker volume: with type hostPath, or with no type and a
// host path. With type volume, HostPath is ignored.
func (d DataStorage) UsesHostPath() bool {
	return d.Type == StorageHostPath || (d.Type == "" && d.HostPath != "")
}

// validate checks the data storage fields.
func (d DataStorage) validate() error {
	switch d.Type {
	case "", StorageVolume, StorageHostPath:
	default:
		return fmt.Errorf("storage.dataStorage.type must be %q or %q, got %q", StorageVolume, StorageHostPath, d.Type)
	}
	if d.Type == StorageHostPath && d.HostPath == "" {
		return fmt.Errorf("storage.dataStorage.hostPath is required with type %q", StorageHostPath)
	}
	if d.MountPath != "" && !strings.HasPrefix(d.MountPath, "/") {
		return fmt.Errorf("storage.dataStorage.mountPath must be absolute, got %q", d.MountPath)
	}
	// sind enter and sind exec read the mount point back from docker ps,
	// which joins labels with commas.
	if strings.Contains(d.MountPath, ",") {
		return fmt.Errorf("storage.dataStorage.mountPath must not contain a comma, got %q", d.MountPath)
	}
	return nil
}

// validateDataMountPath checks that the data storage's mountPath is not a
// path where every node already has a mount (see cluster.BuildRunArgs):
// Docker refuses a container with two mounts at one path.
func (c *Cluster) validateDataMountPath() error {
	if c.Storage.DataStorage.MountPath == "" {
		return nil
	}
	mountPath := filepath.Clean(c.Storage.DataStorage.MountPath)
	for _, m := range []struct {
		path, what string
		used       bool
	}{
		{"/etc/slurm", "the Slurm configuration", true},
		{"/etc/munge", "the munge key", c.Identity.Mode != IdentityClientIDs},
		{"/tmp", "a tmpfs", true},
		{"/run", "a tmpfs", true},
		{"/run/lock", "a tmpfs", true},
		{"/home", "the users' home directories", len(c.Users) > 0},
		{"/cvmfs", "CVMFS", c.Storage.CVMFS},
		{"/var/spool/slurmctld", "the controllers' shared state", c.HasBackupController()},
	} {
		if m.used && mountPath == m.path {
			return fmt.Errorf("storage.dataStorage.mountPath %s is where the nodes mount %s", c.Storage.DataStorage.MountPath, m.what)
		}
	}
	return nil
}

// Storage configures cluster storage options.
type Storage struct {
	DataStorage DataStorage `json:"dataStorage,omitempty"`
	// CVMFS mounts CVMFS read-only at /cvmfs on every node, from the cvmfs
	// Docker volume plugin or from the Docker host's /cvmfs.
	CVMFS bool `json:"cvmfs,omitempty"`
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
	return slices.Sorted(maps.Keys(s.Fragments))
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
	Kind     string    `json:"kind"`
	Name     string    `json:"name,omitempty"`
	Realm    string    `json:"realm,omitempty"`
	Defaults Defaults  `json:"defaults,omitempty"`
	Storage  Storage   `json:"storage,omitempty"`
	Slurm    Slurm     `json:"slurm,omitempty"`
	Users    []User    `json:"users,omitempty"`
	Groups   []Group   `json:"groups,omitempty"`
	Accounts []Account `json:"accounts,omitempty"`
	Identity Identity  `json:"identity,omitempty"`
	Nodes    []Node    `json:"nodes,omitempty"`
	// Monitoring collects the cluster's Slurm metrics, container resource
	// use and logs into the realm's monitoring stack. It needs a managed
	// cluster, and is fixed for the cluster's life.
	Monitoring bool `json:"monitoring,omitempty"`

	// Pull is a runtime flag (not part of the config file) that forces
	// fresh image pulls when creating containers.
	Pull bool `json:"-" yaml:"-"`
	// Wait is a runtime setting (not part of the config file): when
	// positive, how long cluster.Create waits for the nodes and Slurm to
	// become ready, counted from when the node containers have started.
	Wait time.Duration `json:"-" yaml:"-"`
}

// Default resource values for cluster nodes.
const (
	DefaultClusterName = "default"
	DefaultCPUs        = 1
	DefaultMemory      = "512m"
	DefaultTmpSize     = "256m"
)

// DefaultImage is the node image of nodes that set none. Release builds
// set it with -ldflags "-X" to the image the image workflow publishes for
// their release, ghcr.io/gsi-hpc/sind-node:vX.Y.Z (.goreleaser.yaml), so a
// release keeps the node image it was built with. Other builds use latest,
// the newest Slurm release line.
var DefaultImage = "ghcr.io/gsi-hpc/sind-node:latest"

// ApplyDefaults populates missing fields with defaults.
// If no nodes are defined, creates a minimal cluster (1 controller + 1 worker).
// Node-level fields inherit from the Defaults section, which in turn falls back
// to built-in defaults. Users without a uid, then groups without a gid, get
// the lowest free ID from MinUID up. The identity mode defaults to local.
func (c *Cluster) ApplyDefaults() {
	assignIDs(c.Users, c.Groups)
	if c.Identity.Mode == "" {
		c.Identity.Mode = IdentityLocal
	}

	if len(c.Nodes) == 0 {
		c.Nodes = []Node{
			{Role: RoleController},
			{Role: RoleWorker},
		}
	}

	// Each node field falls back to defaults, then to the built-in default.
	d := c.Defaults
	for i := range c.Nodes {
		n := &c.Nodes[i]
		n.Image = cmp.Or(n.Image, d.Image, DefaultImage)
		n.CPUs = cmp.Or(n.CPUs, d.CPUs, DefaultCPUs)
		n.Memory = cmp.Or(n.Memory, d.Memory, DefaultMemory)
		n.TmpSize = cmp.Or(n.TmpSize, d.TmpSize, DefaultTmpSize)
		// Security fields: per-node values merge with (not replace) defaults
		n.CapAdd = mergeStringSlices(d.CapAdd, n.CapAdd)
		n.CapDrop = mergeStringSlices(d.CapDrop, n.CapDrop)
		n.Devices = mergeStringSlices(d.Devices, n.Devices)
		n.SecurityOpt = mergeStringSlices(d.SecurityOpt, n.SecurityOpt)
	}
}

// mergeStringSlices merges two string slices, deduplicating entries.
// Returns nil if both inputs are empty.
func mergeStringSlices(base, overlay []string) []string {
	var result []string
	for _, s := range slices.Concat(base, overlay) {
		if !slices.Contains(result, s) {
			result = append(result, s)
		}
	}
	return result
}

// Validate checks that the cluster configuration satisfies all constraints.
// It should be called after ApplyDefaults.
func (c *Cluster) Validate() error {
	if err := CheckName("cluster", c.Name); err != nil {
		return err
	}
	if c.HasManagedDB() && len(c.Name) > MaxAccountingClusterName {
		return fmt.Errorf("cluster name %q has %d characters: with a managed db node it may have at most %d, as slurmdbd builds its table names from it", c.Name, len(c.Name), MaxAccountingClusterName)
	}
	if c.Realm != "" {
		if err := CheckName("realm", c.Realm); err != nil {
			return err
		}
	}

	roles := make(map[Role]int)
	for _, n := range c.Nodes {
		switch n.Role {
		case RoleController, RoleDB, RoleAPI, RoleSubmitter, RoleWorker:
			roles[n.Role]++
		default:
			return fmt.Errorf("invalid role %q, must be one of: controller, db, api, submitter, worker", n.Role)
		}

		if n.Count < 0 {
			return fmt.Errorf("count must not be negative, got %d", n.Count)
		}
		if n.Count > 0 && n.Role != RoleWorker {
			return fmt.Errorf("count is only valid for worker nodes, not %q", n.Role)
		}
		if n.Managed != nil && n.Role == RoleSubmitter {
			return fmt.Errorf("managed is only valid for controller, db, api and worker nodes, not %q", n.Role)
		}
		if n.BackupController && n.Role != RoleController {
			return fmt.Errorf("backupController is only valid for controller nodes, not %q", n.Role)
		}
	}

	if roles[RoleController] != 1 {
		return fmt.Errorf("exactly one controller required, got %d", roles[RoleController])
	}
	if roles[RoleDB] > 1 {
		return fmt.Errorf("at most one db node allowed, got %d", roles[RoleDB])
	}
	if roles[RoleAPI] > 1 {
		return fmt.Errorf("at most one api node allowed, got %d", roles[RoleAPI])
	}
	if roles[RoleSubmitter] > 1 {
		return fmt.Errorf("at most one submitter allowed, got %d", roles[RoleSubmitter])
	}
	if roles[RoleWorker] < 1 {
		return fmt.Errorf("at least one worker node required, got %d", roles[RoleWorker])
	}

	// The first failing check, in this order, is the one reported.
	if err := cmp.Or(
		c.validateResources(),
		c.Storage.DataStorage.validate(),
		c.validateDataMountPath(),
		validateUsers(c.Users, c.Groups),
		c.validateAccounts(),
		c.Identity.validate(c.Managed(), c.Slurm.Main),
		c.validateAPI(),
		c.validateSlurmdbd(),
		c.validateMonitoring(),
	); err != nil {
		return err
	}

	for _, n := range c.Nodes {
		if err := cmp.Or(
			CheckCapabilities("capAdd", n.CapAdd),
			CheckCapabilities("capDrop", n.CapDrop),
			CheckDevices(n.Devices),
			CheckSecurityOpts("securityOpt", n.SecurityOpt),
		); err != nil {
			return err
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

	if !c.Managed() {
		for _, n := range c.Nodes {
			if n.Role != RoleController && n.Managed != nil && *n.Managed {
				return fmt.Errorf("%s managed: true requires a managed controller", n.Role)
			}
		}
		for _, s := range sections {
			if !s.section.IsEmpty() {
				return fmt.Errorf("slurm %s requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster", s.name)
			}
		}
		if c.Monitoring {
			return fmt.Errorf("monitoring requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster")
		}
	}

	if !c.HasManagedDB() && !c.Slurm.Slurmdbd.IsEmpty() {
		return fmt.Errorf("slurm slurmdbd requires a managed db node: sind writes no slurmdbd.conf for an unmanaged one")
	}

	if c.HasBackupController() {
		for _, key := range backupControllerManagedKeys {
			if c.Slurm.Main.SetsParameter(key) {
				return fmt.Errorf("slurm main must not set %s when backupController is enabled: sind generates the controller configuration", key)
			}
		}
	}

	for _, s := range sections {
		if err := validateSection(s.name, s.section); err != nil {
			return err
		}
	}

	return nil
}

// backupControllerManagedKeys are slurm.conf parameters sind generates for a
// primary/backup controller pair; user overrides would break the pair.
// ControlMachine, BackupController and BackupAddr are the deprecated
// spellings of SlurmctldHost.
var backupControllerManagedKeys = []string{
	"SlurmctldHost", "ControlMachine", "BackupController", "BackupAddr", "StateSaveLocation",
}

// HasManagedDB reports whether the cluster has a db node whose accounting
// services sind manages: one without managed: false, in a managed cluster.
// Only then does sind configure accounting (slurmdbd.conf and the accounting
// parameters in slurm.conf).
func (c *Cluster) HasManagedDB() bool {
	return c.hasManaged(RoleDB)
}

// HasManagedAPI reports whether the cluster has an api node on which sind
// runs slurmrestd: one without managed: false, in a managed cluster. Only
// then does sind set up JWT authentication (jwt_hs256.key and the AuthAlt
// parameters in slurm.conf and slurmdbd.conf).
func (c *Cluster) HasManagedAPI() bool {
	return c.hasManaged(RoleAPI)
}

// hasManaged reports whether the cluster has a node of a role that is
// managed: one without managed: false, in a managed cluster. It is meant
// for the roles of at most one node.
func (c *Cluster) hasManaged(role Role) bool {
	if !c.Managed() {
		return false
	}
	for _, n := range c.Nodes {
		if n.Role == role {
			return n.Managed == nil || *n.Managed
		}
	}
	return false
}

// HasBackupController reports whether the controller node spec enables the
// backup controller.
func (c *Cluster) HasBackupController() bool {
	for _, n := range c.Nodes {
		if n.Role == RoleController && n.BackupController {
			return true
		}
	}
	return false
}

// Managed reports whether sind manages the cluster's Slurm configuration and
// daemons. managed: false on the controller node spec makes the whole
// cluster unmanaged: sind writes no Slurm configuration and starts no Slurm
// daemons on any node.
func (c *Cluster) Managed() bool {
	for _, n := range c.Nodes {
		if n.Role == RoleController && n.Managed != nil {
			return *n.Managed
		}
	}
	return true
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

	if err := checkSyntax(data); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	var cfg Cluster
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", plainError(err))
	}

	if cfg.Kind == "" {
		return nil, fmt.Errorf(`kind is required: add "kind: Cluster"`)
	}
	if cfg.Kind != "Cluster" {
		return nil, fmt.Errorf(`kind must be "Cluster", got %q`, cfg.Kind)
	}

	if cfg.Name == "" {
		cfg.Name = DefaultClusterName
	}

	return &cfg, nil
}
