// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

// Size is a choice of the node defaults of the New cluster dialog.
type Size struct {
	ID, Label string
	CPUs      int
	Memory    string
	// TmpSize is empty for sind's default.
	TmpSize string
}

// Sizes are the node defaults that the New cluster dialog offers; the
// field "size" takes their IDs, or "x" for the fields "cpus", "memory" and
// "tmp".
var Sizes = []Size{
	{ID: "s", Label: "Small", CPUs: 1, Memory: "512m"},
	{ID: "m", Label: "Medium", CPUs: 2, Memory: "1g", TmpSize: "512m"},
	{ID: "l", Label: "Large", CPUs: 4, Memory: "4g", TmpSize: "1g"},
}

// NodeRoles are the keys of the nodes of the New cluster dialog that are
// one node, in order; a worker group has the key of the group.
var NodeRoles = []string{"controller", "db", "api", "submitter"}

// NodeAttrs are the fields of a node of the New cluster dialog, after its
// key and a dash, such as db-cpus. A worker group has KEY-count too.
var NodeAttrs = []string{"image", "cpus", "memory", "tmp", "unmanaged", "capAdd", "capDrop", "devices", "securityOpt"}

// Limits of the New cluster dialog: there are none in sind, but a form
// must not make the preview build a million nodes.
const (
	maxGroups = 16
	maxCount  = 1024
	maxCPUs   = 1024
)

// groupPattern is the key of a worker group, which the dialog makes.
var groupPattern = regexp.MustCompile(`^w[0-9a-z]{1,8}$`)

// WorkerGroups returns the keys of the worker groups of the form, in order:
// the values of the field "wg", or w0 for a form without one.
func WorkerGroups(f Form) []string {
	if len(f["wg"]) == 0 {
		return []string{"w0"}
	}
	return f["wg"]
}

// GroupLabel names the worker group g of the form for people: workers, or
// with more than one group workers 1, workers 2 and so on, in the order of
// the form.
func GroupLabel(f Form, g string) string {
	groups := WorkerGroups(f)
	if i := slices.Index(groups, g); i >= 0 && len(groups) > 1 {
		return "workers " + strconv.Itoa(i+1)
	}
	return "workers"
}

// ClusterFieldsOf returns the fields of the New cluster dialog of the form
// in the order the dialog shows them, which its problems follow.
func ClusterFieldsOf(f Form) []string {
	fields := []string{"name", "realm", "image"}
	fields = append(fields, nodeFields("controller")...)
	fields = append(fields, "backup")
	for _, role := range NodeRoles[1:] {
		fields = append(fields, role)
		fields = append(fields, nodeFields(role)...)
	}
	for _, g := range WorkerGroups(f) {
		if groupPattern.MatchString(g) {
			fields = append(fields, g+"-count")
			fields = append(fields, nodeFields(g)...)
		}
	}
	fields = append(fields, "monitoring", "size", "cpus", "memory", "tmp")
	fields = append(fields, UserFieldsOf(f)...)
	fields = append(fields, "identity", "ctlusers")
	fields = append(fields, SlurmFields()...)
	return append(fields, "dtype", "dir", "mkdir", "mount", "cvmfs", "pull", "wait", "wait_other", "yaml")
}

// nodeFields are the fields of the node key, but the count of a group.
func nodeFields(key string) []string {
	fields := make([]string, 0, len(NodeAttrs))
	for _, a := range NodeAttrs {
		if key == "submitter" && a == "unmanaged" {
			continue
		}
		fields = append(fields, key+"-"+a)
	}
	return fields
}

// nodeDefaults reads the node defaults: a size of Sizes, or the fields
// "cpus", "memory" and "tmp" for "x". A form without a size but with one of
// those fields, as a link gives, is "x". A wrong field takes the value of
// the small size.
func nodeDefaults(f Form, d *config.Defaults, ps *problems) {
	size := f.Get("size")
	if size == "" && (f.Has("cpus") || f.Has("memory") || f.Has("tmp")) {
		size = "x"
	}
	small := Sizes[0]
	if size != "x" {
		i := slices.IndexFunc(Sizes, func(s Size) bool { return s.ID == cmp.Or(size, small.ID) })
		if i < 0 {
			ps.add("size", "Node defaults", true, errors.New("pick Small, Medium, Large or Custom"))
			i = 0
		}
		d.CPUs, d.Memory, d.TmpSize = Sizes[i].CPUs, Sizes[i].Memory, Sizes[i].TmpSize
		return
	}
	var err error
	if d.CPUs, err = number(f, "cpus", "CPUs", 1, maxCPUs, small.CPUs); err != nil {
		ps.add("cpus", "CPUs per node", true, fmt.Errorf("write a number from 1 to %d", maxCPUs))
		d.CPUs = small.CPUs
	}
	if memory := strings.TrimSpace(f.Get("memory")); memory != "" {
		if err := config.CheckMemory("memory", memory); err != nil {
			ps.add("memory", "Memory per node", true, err)
		} else {
			d.Memory = memory
		}
	}
	if tmp := strings.TrimSpace(f.Get("tmp")); tmp != "" {
		if err := config.CheckTmpSize("/tmp size", tmp); err != nil {
			ps.add("tmp", "/tmp per node", true, err)
		} else {
			d.TmpSize = tmp
		}
	}
}

// clusterNodes reads the nodes of the New cluster dialog: the nodes of the
// config, and the same nodes one by one for the preview.
func clusterNodes(f Form, ps *problems) ([]config.Node, []PlannedNode) {
	controller := config.Node{Role: config.RoleController}
	nodeSize(f, "controller", "controller", &controller, ps)
	nodes := []config.Node{controller}
	planned := []PlannedNode{{"controller", "controller", "controller"}}
	if checkbox(f, "backup", "controller-backup", ps) {
		nodes[0].BackupController = true
		planned = append(planned, PlannedNode{"controller-backup", "controller-backup", "backup"})
	}
	for _, role := range NodeRoles[1:] {
		if !checkbox(f, role, role, ps) {
			continue
		}
		n := config.Node{Role: config.Role(role)}
		nodeSize(f, role, role, &n, ps)
		nodes = append(nodes, n)
		planned = append(planned, PlannedNode{role, role, role})
	}
	workers := 0
	var seen []string
	for _, g := range WorkerGroups(f) {
		switch {
		case !groupPattern.MatchString(g) || slices.Contains(seen, g):
			ps.add("", "Workers", false, fmt.Errorf("the worker group %q is not one that the dialog made; open the dialog again", g))
			continue
		case len(seen) == maxGroups:
			ps.add(g+"-count", "Workers", false, fmt.Errorf("a cluster here has at most %d worker groups", maxGroups))
			continue
		}
		seen = append(seen, g)
		label := GroupLabel(f, g)
		count, err := number(f, g+"-count", "W"+label[1:], 1, maxCount, -1)
		if err != nil {
			ps.add(g+"-count", "W"+label[1:], true, fmt.Errorf("write a number from 1 to %d", maxCount))
			count = 1
		}
		n := config.Node{Role: config.RoleWorker, Count: count}
		nodeSize(f, g, label, &n, ps)
		nodes = append(nodes, n)
		for range count {
			planned = append(planned, PlannedNode{"worker-" + strconv.Itoa(workers), "worker", g})
			workers++
		}
	}
	return nodes, planned
}

// nodeLists are the fields of a node that take a list, and their check.
var nodeLists = []struct {
	attr  string
	check func(field string, values []string) error
	set   func(n *config.Node, values []string)
}{
	{"capAdd", config.CheckCapabilities, func(n *config.Node, v []string) { n.CapAdd = v }},
	{"capDrop", config.CheckCapabilities, func(n *config.Node, v []string) { n.CapDrop = v }},
	{"devices", config.CheckDevices, func(n *config.Node, v []string) { n.Devices = v }},
	{"securityOpt", config.CheckSecurityOpts, func(n *config.Node, v []string) { n.SecurityOpt = v }},
}

// nodeSize reads the fields of the node key into n: its image, size and
// more. An empty field takes the node defaults; a wrong one too.
func nodeSize(f Form, key, label string, n *config.Node, ps *problems) {
	get := func(attr string) string { return strings.TrimSpace(f.Get(key + "-" + attr)) }
	if image := get("image"); image != "" {
		if err := imageError(image); err == nil {
			n.Image = image
		} else {
			ps.add(key+"-image", label+" image", true, err)
		}
	}
	if s := get("cpus"); s != "" {
		if cpus, err := strconv.Atoi(s); err == nil && cpus >= 1 && cpus <= maxCPUs {
			n.CPUs = cpus
		} else {
			ps.add(key+"-cpus", label+" CPUs", true, fmt.Errorf("write a number from 1 to %d, or nothing for the node defaults", maxCPUs))
		}
	}
	if memory := get("memory"); memory != "" {
		if err := config.CheckMemory("memory", memory); err != nil {
			ps.add(key+"-memory", label+" memory", true, err)
		} else {
			n.Memory = memory
		}
	}
	if tmp := get("tmp"); tmp != "" {
		if err := config.CheckTmpSize("/tmp size", tmp); err != nil {
			ps.add(key+"-tmp", label+" /tmp", true, err)
		} else {
			n.TmpSize = tmp
		}
	}
	if key != "submitter" && checkbox(f, key+"-unmanaged", label+" unmanaged", ps) {
		n.Managed = new(bool)
	}
	for _, l := range nodeLists {
		values := strings.FieldsFunc(get(l.attr), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
		if len(values) == 0 {
			continue
		}
		if err := l.check(l.attr, values); err != nil {
			ps.add(key+"-"+l.attr, label+" "+l.attr, true, err)
			continue
		}
		l.set(n, values)
	}
}

// clusterSize returns how many containers the nodes make, and the CPUs and
// the memory, in MiB, that they may use together.
func clusterSize(d config.Defaults, nodes []config.Node) (containers, cpus, memoryMB int) {
	for _, n := range nodes {
		count := max(n.Count, 1)
		if n.BackupController {
			count++
		}
		mb, _ := config.MemoryMB(cmp.Or(n.Memory, d.Memory, config.DefaultMemory)) // checked
		containers += count
		cpus += count * cmp.Or(n.CPUs, d.CPUs, config.DefaultCPUs)
		memoryMB += count * mb
	}
	return containers, cpus, memoryMB
}

// MemoryLabel writes a size in MiB for people: 512 MiB, 1 GiB, 1.5 GiB.
func MemoryLabel(mb int) string {
	if mb < 1024 {
		return strconv.Itoa(mb) + " MiB"
	}
	return strings.TrimSuffix(strconv.FormatFloat(float64(mb)/1024, 'f', 1, 64), ".0") + " GiB"
}
