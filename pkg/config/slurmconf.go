// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"regexp"
	"slices"
	"strings"
)

// keyValuePattern matches the first key=value pair of a slurm.conf line as
// Slurm's parser (parse_config.c) does: blanks around the "=", an optional
// operator before it, and a value in double quotes or up to the next blank.
var keyValuePattern = regexp.MustCompile(`^\s*([[:alnum:]_.]+)\s*[-*+/]?=\s*(?:"([^"]*)"|(\S+))(?:\s|$)`)

// lineKeys are the slurm.conf keys that take the rest of their line: the
// key=value pairs after them describe the nodes, the partition, ..., not
// the cluster.
var lineKeys = []string{"DownNodes", "FrontendName", "NodeName", "NodeSet", "PartitionName", "PowerAction"}

// LineParameter returns the value one slurm.conf-style line assigns to a
// parameter, and whether it assigns it. It reads the line as Slurm does: a
// "#" starts a comment, a line may hold several key=value pairs, and keys
// match case-insensitively. Pairs after a key such as NodeName or
// PartitionName belong to that node or partition, not to the cluster, so
// they do not count. Of several assignments the last one wins, as in Slurm.
func LineParameter(line, key string) (string, bool) {
	line, _, _ = strings.Cut(line, "#")
	var value string
	var found bool
	for {
		m := keyValuePattern.FindStringSubmatch(line)
		if m == nil {
			return value, found
		}
		if strings.EqualFold(m[1], key) {
			value, found = m[2]+m[3], true
		}
		if slices.ContainsFunc(lineKeys, func(k string) bool { return strings.EqualFold(k, m[1]) }) {
			return value, found
		}
		line = line[len(m[0]):]
	}
}

// Parameter returns the value the section gives a slurm.conf-style
// parameter, and whether it sets it (see LineParameter). Like Slurm, it
// takes the last assignment, in the order sind writes the section: the
// string form, then the fragments by name.
func (s Section) Parameter(key string) (string, bool) {
	contents := []string{s.Content}
	for _, name := range s.FragmentNames() {
		contents = append(contents, s.Fragments[name])
	}
	var value string
	var found bool
	for _, content := range contents {
		for line := range strings.Lines(content) {
			if v, ok := LineParameter(line, key); ok {
				value, found = v, true
			}
		}
	}
	return value, found
}

// SetsParameter reports whether the section sets a slurm.conf-style
// parameter (see Parameter).
func (s Section) SetsParameter(key string) bool {
	_, ok := s.Parameter(key)
	return ok
}

// ListsValue reports whether a comma-separated parameter value, such as
// LaunchParameters or AuthInfo, lists item.
func ListsValue(value, item string) bool {
	for v := range strings.SplitSeq(value, ",") {
		if strings.TrimSpace(v) == item {
			return true
		}
	}
	return false
}
