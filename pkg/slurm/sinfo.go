// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SinfoNodesCommand lists each node of each partition on one line: its
// name, state, CPUs as allocated/idle/other/total, memory in MB, partition
// (with a "*" for the default one) and the reason it is down or drained.
// The reason comes last, as it is free text that may hold the separator.
var SinfoNodesCommand = []string{"sinfo", "--noheader", "--Node", "--format=%N|%T|%C|%m|%P|%E"}

// SinfoPartitionsCommand lists each partition on one line: its name (with
// a "*" for the default one), availability, time limit, number of nodes and
// node list.
var SinfoPartitionsCommand = []string{"sinfo", "--noheader", "--format=%P|%a|%l|%D|%N"}

// NodeInfo is a node as sinfo reports it.
type NodeInfo struct {
	Name string `json:"name"`
	// State is sinfo's long state, such as idle, allocated, mixed,
	// drained or down; a "*" at its end means that the node does not
	// answer.
	State string `json:"state"`
	// Reason is why the node is down or drained, empty for none.
	Reason    string `json:"reason,omitempty"`
	CPUs      int    `json:"cpus"`
	AllocCPUs int    `json:"alloc_cpus"`
	MemoryMB  int    `json:"memory_mb"`
	// Partitions are the partitions of the node, in sinfo's order.
	Partitions []string `json:"partitions"`
}

// PartitionInfo is a partition as sinfo reports it.
type PartitionInfo struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	// State is the availability: up, down, drain or inact.
	State     string `json:"state"`
	TimeLimit string `json:"time_limit"`
	Nodes     int    `json:"nodes"`
	// NodeList is the nodes as a host list, such as worker-[0-3].
	NodeList string `json:"node_list"`
}

// ParseSinfoNodes parses the output of SinfoNodesCommand: one NodeInfo per
// node, in the order of its first line, with the partitions of all its
// lines.
func ParseSinfoNodes(out string) ([]NodeInfo, error) {
	var nodes []NodeInfo
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 6)
		if len(f) != 6 {
			return nil, fmt.Errorf("sinfo node line %q: want 6 fields, got %d", line, len(f))
		}
		cpus := strings.Split(f[2], "/")
		if len(cpus) != 4 {
			return nil, fmt.Errorf("sinfo node line %q: CPUs %q are not A/I/O/T", line, f[2])
		}
		alloc, err := strconv.Atoi(cpus[0])
		if err != nil {
			return nil, fmt.Errorf("sinfo node line %q: allocated CPUs: %w", line, err)
		}
		total, err := strconv.Atoi(cpus[3])
		if err != nil {
			return nil, fmt.Errorf("sinfo node line %q: CPUs: %w", line, err)
		}
		mem, err := strconv.Atoi(f[3])
		if err != nil {
			return nil, fmt.Errorf("sinfo node line %q: memory: %w", line, err)
		}
		part := strings.TrimSuffix(f[4], "*")
		if i := slices.IndexFunc(nodes, func(n NodeInfo) bool { return n.Name == f[0] }); i >= 0 {
			nodes[i].Partitions = append(nodes[i].Partitions, part)
			continue
		}
		reason := f[5]
		if reason == "none" {
			reason = ""
		}
		nodes = append(nodes, NodeInfo{
			Name: f[0], State: f[1], Reason: reason, CPUs: total, AllocCPUs: alloc, MemoryMB: mem, Partitions: []string{part},
		})
	}
	return nodes, nil
}

// ParseSinfoPartitions parses the output of SinfoPartitionsCommand.
func ParseSinfoPartitions(out string) ([]PartitionInfo, error) {
	var parts []PartitionInfo
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 5 {
			return nil, fmt.Errorf("sinfo partition line %q: want 5 fields, got %d", line, len(f))
		}
		n, err := strconv.Atoi(f[3])
		if err != nil {
			return nil, fmt.Errorf("sinfo partition line %q: nodes: %w", line, err)
		}
		name, isDefault := strings.CutSuffix(f[0], "*")
		parts = append(parts, PartitionInfo{Name: name, Default: isDefault, State: f[1], TimeLimit: f[2], Nodes: n, NodeList: f[4]})
	}
	return parts, nil
}

// NodeStateTakesJobs reports whether slurmctld schedules jobs on a node in
// the state, as sinfo writes it: not when the node is down, drained or
// draining, failing, does not answer ("*"), or is powered down or in
// maintenance.
func NodeStateTakesJobs(state string) bool {
	if strings.HasSuffix(state, "*") {
		return false
	}
	for _, s := range []string{"down", "drain", "fail", "maint", "power", "inval", "unk", "future", "reboot"} {
		if strings.HasPrefix(state, s) {
			return false
		}
	}
	return true
}
