// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"fmt"
	"iter"
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

// LinePairs returns the key=value pairs of one slurm.conf-style line, in
// order, as Slurm's parser reads them: a "#" that no backslash escapes
// starts a comment, a backslash escapes the next character (\# is #, \\
// is \), a line may hold several pairs, and a value may be quoted. A line
// continued with a trailing backslash is not joined to the next.
func LinePairs(line string) [][2]string {
	line = unescape(StripComment(line))
	var pairs [][2]string
	for {
		m := keyValuePattern.FindStringSubmatch(line)
		if m == nil {
			return pairs
		}
		pairs = append(pairs, [2]string{m[1], m[2] + m[3]})
		line = line[len(m[0]):]
	}
}

// StripComment cuts a slurm.conf-style line at its comment: a # that an
// even number of backslashes precedes.
func StripComment(line string) string {
	backslashes := 0
	for i := range len(line) {
		switch {
		case line[i] == '\\':
			backslashes++
			continue
		case line[i] == '#' && backslashes%2 == 0:
			return line[:i]
		}
		backslashes = 0
	}
	return line
}

// unescape removes the backslash before each escaped character of a
// slurm.conf-style line, as Slurm's parser does: \# is #, \\ is \.
func unescape(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			if i == len(line) {
				break
			}
		}
		b.WriteByte(line[i])
	}
	return b.String()
}

// LineParameter returns the value one slurm.conf-style line assigns to a
// parameter, and whether it assigns it (see LinePairs); keys match
// case-insensitively, as Slurm matches them. Pairs after a key such as
// NodeName or PartitionName belong to that node or partition, not to the
// cluster, so they do not count. Of several assignments the last one
// wins, as in Slurm.
func LineParameter(line, key string) (string, bool) {
	var value string
	var found bool
	for _, p := range LinePairs(line) {
		if strings.EqualFold(p[0], key) {
			value, found = p[1], true
		}
		if slices.ContainsFunc(lineKeys, func(k string) bool { return strings.EqualFold(k, p[0]) }) {
			break
		}
	}
	return value, found
}

// Lines returns the lines of the section in the order sind writes them:
// the string form, then the fragments by name.
func (s Section) Lines() iter.Seq[string] {
	return func(yield func(string) bool) {
		contents := []string{s.Content}
		for _, name := range s.FragmentNames() {
			contents = append(contents, s.Fragments[name])
		}
		for _, content := range contents {
			for line := range strings.Lines(content) {
				if !yield(line) {
					return
				}
			}
		}
	}
}

// Parameter returns the value the section gives a slurm.conf-style
// parameter, and whether it sets it (see LineParameter). Like Slurm, it
// takes the last assignment, in the order of Lines.
func (s Section) Parameter(key string) (string, bool) {
	var value string
	var found bool
	for line := range s.Lines() {
		if v, ok := LineParameter(line, key); ok {
			value, found = v, true
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
// AuthInfo, lists item.
func ListsValue(value, item string) bool {
	return listsWith(value, item, func(v, item string) bool { return v == item })
}

// ListsFlag reports whether a comma-separated list of flags that Slurm
// reads without regard to case, such as LaunchParameters, lists flag, in
// any case.
func ListsFlag(value, flag string) bool {
	return listsWith(value, flag, strings.EqualFold)
}

// IsPlugin reports whether a plugin parameter value, such as AuthType or
// MetricsType, names plugin, which has the form <type>/<name>: Slurm also
// takes the name without its type prefix. A plugin without a type prefix
// matches only itself.
func IsPlugin(value, plugin string) bool {
	value = strings.TrimSpace(value)
	if _, name, ok := strings.Cut(plugin, "/"); ok && value == name {
		return true
	}
	return value == plugin
}

// ListsPlugin reports whether a comma-separated list of plugins, such as
// AuthAltTypes or TaskPlugin, lists plugin (see IsPlugin).
func ListsPlugin(value, plugin string) bool {
	return listsWith(value, plugin, IsPlugin)
}

// listsWith reports whether an entry of the comma-separated value matches
// item.
func listsWith(value, item string, match func(v, item string) bool) bool {
	for v := range strings.SplitSeq(value, ",") {
		if match(strings.TrimSpace(v), item) {
			return true
		}
	}
	return false
}

// fixedSlurmParameters are the slurm.conf parameters that sind always
// writes, whatever the main section sets: the cluster name, the
// controller, SlurmUser, the spool directories and PlugStackConfig, which
// sind's names, volumes and images depend on. Slurm takes the last value
// of a parameter, so a main section that sets one of them replaces sind's.
var fixedSlurmParameters = []string{"ClusterName", "SlurmctldHost", "SlurmUser", "StateSaveLocation", "SlurmdSpoolDir", "PlugStackConfig"}

// FixedSlurmParameters returns the slurm.conf parameters that sind always
// writes, whatever the main section sets.
func FixedSlurmParameters() []string { return slices.Clone(fixedSlurmParameters) }

// SlurmWarnings warns of what sind takes in the Slurm config of a managed
// cluster but likely breaks it: the main section setting a parameter that
// sind writes itself (FixedSlurmParameters), which Slurm takes in place of
// sind's, and lines that are no Key=Value pair. Validate refuses
// SlurmctldHost and StateSaveLocation itself with a backup controller.
// They need no valid config: sind ui shows them while the config has
// errors.
func (c *Cluster) SlurmWarnings() []string {
	if !c.Managed() {
		return nil
	}
	var warns, fixed []string
	for _, key := range fixedSlurmParameters {
		refused := c.HasBackupController() && slices.Contains(backupControllerManagedKeys, key)
		if !refused && c.Slurm.Main.SetsParameter(key) {
			fixed = append(fixed, key)
		}
	}
	if len(fixed) > 0 {
		warns = append(warns, fmt.Sprintf("The lines of slurm.conf set %s, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start.", strings.Join(fixed, ", ")))
	}
	for _, s := range c.Slurm.sections() {
		if s.name == "plugstack" {
			// Its lines are SPANK plugins, no Key=Value pairs.
			continue
		}
		if line := pairlessLine(s.section); line != "" {
			warns = append(warns, fmt.Sprintf("The lines of %s have %q, which is no Key=Value pair. Slurm may refuse it and not start.", s.file, line))
		}
	}
	return warns
}

// pairlessLine returns the first line of s that is neither empty, a
// comment, an include nor a Key=Value pair.
func pairlessLine(s Section) string {
	for line := range s.Lines() {
		text, _, _ := strings.Cut(line, "#")
		text = strings.TrimSpace(text)
		if text == "" || strings.Contains(text, "=") {
			continue
		}
		if first, _, _ := strings.Cut(text, " "); strings.EqualFold(first, "include") {
			continue
		}
		return text
	}
	return ""
}
