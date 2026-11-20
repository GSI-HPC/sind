// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/GSI-HPC/sind/pkg/config"
)

// defaultKeys are the parameters that GenerateSlurmConf writes unless the
// main section sets them, and the ones that decide whether it writes
// another.
var defaultKeys = []string{
	"ProctrackType", "TaskPlugin", "MpiDefault", "ReturnToService",
	"DefMemPerCPU", "DefMemPerNode", "SlurmctldTimeout",
	"AccountingStorageType", "AccountingStorageHost", "JobAcctGatherType",
	"MetricsType", "AuthType", "CredType", "AuthAltTypes", "AuthAltParameters",
	"AuthInfo", "LaunchParameters", "SchedulerType", "MaxJobCount",
}

// mainLine draws one line of a main section: an assignment as Slurm takes
// it (any case, blanks around the "=", a comment after it), a line that
// only looks like one (in a comment, or after NodeName, where the pairs
// belong to the node), or a comment.
func mainLine() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		key := rapid.SampledFrom(defaultKeys).Draw(t, "key")
		key = rapid.SampledFrom([]string{key, strings.ToLower(key), strings.ToUpper(key)}).Draw(t, "case")
		value := rapid.SampledFrom([]string{"1", "none", "task/cgroup,task/affinity", `"a b"`, "x\\#y"}).Draw(t, "value")
		pair := key + rapid.SampledFrom([]string{"=", " = ", "\t=", "+="}).Draw(t, "equals") + value
		return rapid.SampledFrom([]string{
			pair,
			"  " + pair + " # a comment",
			pair + " SchedulerParameters=defer",
			"#" + pair,
			"NodeName=extra CPUs=2 " + pair,
			"PartitionName=debug Nodes=extra " + pair,
			"# a comment",
			"",
		}).Draw(t, "line")
	})
}

// mainSection draws a main section in one of its two forms: a string, or
// fragments, which sind writes to files that slurm.conf includes.
func mainSection() *rapid.Generator[config.Section] {
	content := rapid.Custom(func(t *rapid.T) string {
		lines := rapid.SliceOfN(mainLine(), 0, 6).Draw(t, "lines")
		text := strings.Join(lines, "\n")
		if text != "" && rapid.Bool().Draw(t, "newline") {
			text += "\n"
		}
		return text
	})
	return rapid.Custom(func(t *rapid.T) config.Section {
		if rapid.Bool().Draw(t, "fragments") {
			names := rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"00-base", "10-sched", "zz"}), 1, 3, rapid.ID).Draw(t, "names")
			fragments := make(map[string]string, len(names))
			for _, name := range names {
				fragments[name] = content.Draw(t, name)
			}
			return config.Section{Fragments: fragments}
		}
		return config.Section{Content: content.Draw(t, "content")}
	})
}

// confOptions draws the options of a slurm.conf.
func confOptions() *rapid.Generator[ConfOptions] {
	return rapid.Custom(func(t *rapid.T) ConfOptions {
		return ConfOptions{
			BackupController: rapid.Bool().Draw(t, "backup"),
			Accounting:       rapid.Bool().Draw(t, "accounting"),
			Identity: rapid.SampledFrom([]config.IdentityMode{
				config.IdentityLocal, config.IdentityNSSSlurm, config.IdentityClientIDs,
			}).Draw(t, "identity"),
			JWT:          rapid.Bool().Draw(t, "jwt"),
			DefMemPerCPU: rapid.SampledFrom([]int{0, 512}).Draw(t, "defMemPerCPU"),
			Metrics:      rapid.Bool().Draw(t, "metrics"),
		}
	})
}

// assignments counts the lines of conf that set key for the cluster.
func assignments(conf config.Section, key string) int {
	n := 0
	for line := range conf.Lines() {
		if _, ok := config.LineParameter(line, key); ok {
			n++
		}
	}
	return n
}

// TestGenerateSlurmConf_ReadsBackAsWritten checks the slurm.conf that sind
// writes for any main section and options, as the daemons read it
// (ReadSlurmConf, which sind create worker uses):
//   - the main section decides each parameter that it sets
//   - sind writes no parameter that the main section sets, in any case of
//     its name: Slurm logs an error for a parameter set twice
//   - DefMemPerCPU and DefMemPerNode, which Slurm does not take together,
//     are together only if the main section sets both
func TestGenerateSlurmConf_ReadsBackAsWritten(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		main := mainSection().Draw(t, "main")
		opts := confOptions().Draw(t, "opts")

		files := map[string]string{SlurmConfPath: GenerateSlurmConf("dev", main, opts)}
		for name, content := range main.Fragments {
			files[ConfDir+"/slurm.conf.d/"+name+".conf"] = content
		}
		conf, err := ReadSlurmConf(func(path string) (string, error) {
			content, ok := files[path]
			if !ok {
				return "", errors.New("no such file")
			}
			return content, nil
		})
		require.NoError(t, err, "slurm.conf:\n%s", files[SlurmConfPath])

		name, _ := conf.Parameter("ClusterName")
		assert.Equal(t, "dev", name)
		hosts := 1
		if opts.BackupController {
			hosts = 2
		}
		assert.Equal(t, hosts, assignments(conf, "SlurmctldHost"))

		for _, key := range defaultKeys {
			want, set := main.Parameter(key)
			got, _ := conf.Parameter(key)
			if set {
				assert.Equal(t, want, got, "%s is the main section's", key)
				assert.Equal(t, assignments(main, key), assignments(conf, key),
					"sind sets %s, which the main section sets:\n%s", key, conf.Content)
			} else {
				assert.LessOrEqual(t, assignments(conf, key), 1, "sind sets %s more than once:\n%s", key, conf.Content)
			}
		}
		if conf.SetsParameter("DefMemPerCPU") && conf.SetsParameter("DefMemPerNode") {
			assert.True(t, main.SetsParameter("DefMemPerCPU") && main.SetsParameter("DefMemPerNode"),
				"sind adds DefMemPerCPU to a DefMemPerNode:\n%s", conf.Content)
		}
		assert.Equal(t, TaskAffinity(main), EnablesTaskAffinity(conf),
			"the task plugins of the written file are those sind expects")
	})
}

// nodePool holds the node names of the sind-nodes.conf model.
var nodePool = []string{"worker-0", "worker-1", "worker-2", "worker-10", "compute-0", "gpu-0"}

// foreignLines are lines of sind-nodes.conf that are not sind's: the edits
// must keep each as it is.
var foreignLines = []string{
	"# Generated by sind",
	"# a comment with NodeName=worker-0",
	"",
	"NodeName=DEFAULT Sockets=1",
	"PartitionName=DEFAULT State=UP",
	"PartitionName=debug Nodes=ALL MaxTime=10",
}

// partitionStyles are lines of partition all as a user may have changed
// them, with %s for the node list.
var partitionStyles = []string{
	"PartitionName=all Nodes=%s Default=YES",
	"PartitionName=all Default=NO Nodes=%s",
	"PartitionName = all MaxTime=1:00:00 Nodes=\"%s\" # mine, Nodes=x",
	"partitionname=all AllowGroups=hpc nodes=%s",
}

// nodesConf is a sind-nodes.conf under edit, with what it must hold.
type nodesConf struct {
	text    string
	nodes   []string // the nodes with a NodeName line, in file order
	foreign []string // the lines that are not sind's, in file order
	style   string   // the line of partition all, if a user changed it
}

// newNodesConf draws a well-formed sind-nodes.conf: one line for each of
// its nodes, lines of other kinds between them, and the line of partition
// all with every node if there is a node.
func newNodesConf(t *rapid.T) *nodesConf {
	c := &nodesConf{
		nodes:   rapid.SliceOfNDistinct(rapid.SampledFrom(nodePool), 0, 3, rapid.ID).Draw(t, "nodes"),
		foreign: rapid.SliceOfN(rapid.SampledFrom(foreignLines), 0, 4).Draw(t, "foreign"),
	}
	lines := slices.Clone(c.foreign)
	for _, node := range c.nodes {
		at := rapid.IntRange(0, len(lines)).Draw(t, "at")
		lines = slices.Insert(lines, at, "NodeName="+node+" CPUs=1 RealMemory=512 Feature=mine")
	}
	// The order of the node lines is the order of their insertion points.
	c.nodes = c.nodes[:0]
	c.foreign = c.foreign[:0]
	for _, line := range lines {
		if name, ok := nodeName(line); ok {
			c.nodes = append(c.nodes, name)
		} else {
			c.foreign = append(c.foreign, line)
		}
	}
	if len(c.nodes) > 0 {
		c.style = rapid.SampledFrom(partitionStyles).Draw(t, "partition")
		lines = append(lines, fmt.Sprintf(c.style, strings.Join(c.nodes, ",")))
	}
	c.text = strings.Join(lines, "\n") + "\n"
	return c
}

// check compares the file with the model.
func (c *nodesConf) check(t *rapid.T) {
	assert.True(t, slices.Equal(c.nodes, defined(c.text)), "the nodes %q of:\n%s", c.nodes, c.text)

	var foreign, partitions []string
	for line := range strings.Lines(c.text) {
		line = strings.TrimSuffix(line, "\n")
		_, isNode := nodeName(line)
		switch {
		case isPartitionAll(line):
			partitions = append(partitions, line)
		case !isNode && line != "":
			foreign = append(foreign, line)
		}
	}
	// The edits drop the blank lines at the end of the file.
	want := slices.DeleteFunc(slices.Clone(c.foreign), func(line string) bool { return line == "" })
	assert.True(t, slices.Equal(want, foreign), "the lines %q that are not sind's, in:\n%s", want, c.text)

	if len(c.nodes) == 0 {
		assert.Empty(t, partitions, "partition all without a node, which Slurm refuses, in:\n%s", c.text)
		return
	}
	require.Len(t, partitions, 1, "the lines of partition all in:\n%s", c.text)
	assert.Equal(t, partitions[0], c.text[strings.LastIndex(strings.TrimSuffix(c.text, "\n"), "\n")+1:len(c.text)-1],
		"partition all comes after the nodes it names")
	var list []string
	for _, p := range config.LinePairs(partitions[0]) {
		if strings.EqualFold(p[0], "Nodes") {
			list = append(list, p[1])
		}
	}
	assert.Equal(t, []string{strings.Join(c.nodes, ",")}, list, "the nodes of partition all in:\n%s", c.text)
	if c.style != "" {
		assert.Equal(t, fmt.Sprintf(c.style, strings.Join(c.nodes, ",")), partitions[0],
			"the rest of the line of partition all stays")
	}
}

// unquoted returns a line of partition all as an edit leaves it: the
// Nodes= pair is sind's, which writes the key as Slurm documents it and
// quotes only a list that needs it.
func unquoted(style string) string {
	return strings.NewReplacer(`"%s"`, "%s", "nodes=", "Nodes=").Replace(style)
}

// add adds nodes to the file and to the model: a node that the file has
// keeps its place, and the others go after the last line.
func (c *nodesConf) add(t *rapid.T) {
	names := rapid.SliceOfNDistinct(rapid.SampledFrom(nodePool), 1, 3, rapid.ID).Draw(t, "names")
	entries := make([]NodeEntry, len(names))
	for i, name := range names {
		entries[i] = NodeEntry{Name: name, CPUs: 2 + i, MemoryMB: 1024}
	}
	fresh := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return slices.Contains(c.nodes, n) })
	before := c.text

	c.text = AddNodesToConf(c.text, entries, config.Section{})
	if len(c.nodes) == 0 {
		c.style = "" // sind writes the line anew
	}
	c.style = unquoted(c.style)
	c.nodes = append(c.nodes, fresh...)

	for _, e := range entries {
		assert.Contains(t, c.text, fmt.Sprintf("NodeName=%s CPUs=%d RealMemory=%d State=UNKNOWN\n", e.Name, e.CPUs, e.MemoryMB))
	}
	assert.Equal(t, c.text, AddNodesToConf(c.text, entries, config.Section{}), "a second add changes nothing")
	if len(fresh) == len(names) && len(c.nodes) > len(fresh) {
		assert.Equal(t, RemoveNodesFromConf(before, names), RemoveNodesFromConf(c.text, names),
			"remove undoes the add of new nodes")
	}
}

// remove removes nodes, which the file may not have, from the file and
// from the model.
func (c *nodesConf) remove(t *rapid.T) {
	names := rapid.SliceOfNDistinct(rapid.SampledFrom(nodePool), 1, 3, rapid.ID).Draw(t, "names")

	c.text = RemoveNodesFromConf(c.text, names)
	c.nodes = slices.DeleteFunc(c.nodes, func(n string) bool { return slices.Contains(names, n) })
	c.style = unquoted(c.style)
	if len(c.nodes) == 0 {
		c.style = "" // the line goes with the last node
	}

	// But for the blank lines that the removed lines leave at the end.
	assert.Equal(t, strings.TrimRight(c.text, "\n"), strings.TrimRight(RemoveNodesFromConf(c.text, names), "\n"),
		"a second remove changes nothing")
}

// TestNodesConf_EditsKeepTheFileWellFormed checks sind-nodes.conf over any
// sequence of sind create worker and sind delete worker, from any
// well-formed file: after each edit the file has one line for each node,
// partition all names exactly those nodes and is absent without one, and
// every other line that is not blank, as the rest of the line of partition
// all, is as it was.
func TestNodesConf_EditsKeepTheFileWellFormed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := newNodesConf(t)
		c.check(t)
		t.Repeat(map[string]func(*rapid.T){
			"add":    c.add,
			"remove": c.remove,
			"":       c.check,
		})
	})
}
