// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateCluster_Sizes(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Form
		want string
	}{
		{"small", valid("size", "s"), "defaults:\n  cpus: 1\n  memory: 512m\nstorage:"},
		{"medium", valid("size", "m"), "defaults:\n  cpus: 2\n  memory: 1g\n  tmpSize: 512m\nstorage:"},
		{"large", valid("size", "l"), "defaults:\n  cpus: 4\n  memory: 4g\n  tmpSize: 1g\nstorage:"},
		{"custom", valid("size", "x", "cpus", "3", "memory", "1.5g", "tmp", "50%"), "defaults:\n  cpus: 3\n  memory: 1.5g\n  tmpSize: 50%\nstorage:"},
		{"custom empty", valid("size", "x"), "defaults:\n  cpus: 1\nstorage:"},
		{"link with a size of its own", valid("tmp", "2g"), "defaults:\n  cpus: 1\n  tmpSize: 2g\nstorage:"},
		{"custom ignored", valid("size", "m", "cpus", "7"), "defaults:\n  cpus: 2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			assert.Empty(t, cmd.Problems)
			assert.Contains(t, cmd.Config.Content, tc.want)
		})
	}
}

func TestBuildCreateCluster_NodesOfTheirOwn(t *testing.T) {
	cmd, err := build(t, "create-cluster", groups(valid(
		"size", "m", "db", "on", "submitter", "on",
		"controller-cpus", "2", "controller-memory", "2g", "controller-unmanaged", "",
		"db-image", "ghcr.io/gsi-hpc/sind-node:26.05", "db-tmp", "1g",
		"submitter-unmanaged", "on",
		"w0-count", "2", "wabc-count", "3",
		"wabc-cpus", "8", "wabc-memory", "16g", "wabc-unmanaged", "on",
		"wabc-capAdd", "SYS_ADMIN, NET_ADMIN", "wabc-capDrop", "NET_RAW", "wabc-devices", "/dev/fuse",
		"wabc-securityOpt", "seccomp=unconfined",
		"wg", "w0",
	), "wabc"))
	require.NoError(t, err)
	require.Empty(t, cmd.Problems)

	assert.Contains(t, cmd.Config.Content, `nodes:
  - role: controller
    cpus: 2
    memory: 2g
  - role: db
    image: ghcr.io/gsi-hpc/sind-node:26.05
    tmpSize: 1g
  - role: submitter
  - role: worker
    count: 2
  - role: worker
    count: 3
    cpus: 8
    memory: 16g
    managed: false
    capAdd:
      - SYS_ADMIN
      - NET_ADMIN
    capDrop:
      - NET_RAW
    devices:
      - /dev/fuse
    securityOpt:
      - seccomp=unconfined
`)
	var keys []string
	for _, n := range cmd.Nodes {
		keys = append(keys, n.Short+"@"+n.Key)
	}
	assert.Equal(t, []string{
		"controller@controller", "db@db", "submitter@submitter",
		"worker-0@w0", "worker-1@w0", "worker-2@wabc", "worker-3@wabc", "worker-4@wabc",
	}, keys)
	// 1 controller (2 CPUs, 2g), db and submitter (2, 1g), 2 workers
	// (2, 1g) and 3 (8, 16g).
	assert.Equal(t, "8 containers, 34 CPUs, 54 GiB of memory.", cmd.Summary)
}

func TestBuildCreateCluster_NodeProblems(t *testing.T) {
	for _, tc := range []struct {
		name         string
		f            Form
		field, want  string
		defaultsKept bool
	}{
		{"size", valid("size", "xl"), "size", "pick Small, Medium, Large or Custom", true},
		{"tmp", valid("size", "x", "tmp", "lots"), "tmp", `invalid /tmp size "lots"`, true},
		{"image", valid("controller-image", "a b"), "controller-image", "write an image reference", true},
		{"cpus", valid("db", "on", "db-cpus", "0"), "db-cpus", "write a number from 1 to 1024, or nothing", true},
		{"memory", valid("w0-memory", "1x"), "w0-memory", `invalid memory "1x"`, true},
		{"tmp of a node", valid("w0-tmp", "1x"), "w0-tmp", `invalid /tmp size "1x"`, true},
		{"unmanaged", valid("api", "on", "api-unmanaged", "maybe"), "api-unmanaged", "tick the box", true},
		{"capAdd", valid("w0-capAdd", "FLY"), "w0-capAdd", "FLY", true},
		{"devices", valid("w0-devices", "dev/fuse"), "w0-devices", "dev/fuse", true},
		{"securityOpt", valid("w0-securityOpt", "nope=1"), "w0-securityOpt", "nope=1", true},
		{"group key", groups(valid("wg", "w0"), "../x"), "", `the worker group "../x" is not one that the dialog made`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", tc.f)
			require.NoError(t, err)
			require.Len(t, cmd.Problems, 1, "%+v", cmd.Problems)
			assert.Equal(t, tc.field, cmd.Problems[0].Field)
			assert.Contains(t, cmd.Problems[0].Text, tc.want)
			assert.Equal(t, tc.defaultsKept, cmd.Problems[0].Default)
		})
	}
}

func TestBuildCreateCluster_WorkerGroups(t *testing.T) {
	f := groups(valid("wg", "w0", "w1-count", "2"), "w0", "w1")
	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 1)
	assert.Contains(t, cmd.Problems[0].Text, `the worker group "w0" is not one`, "a key twice")
	assert.Len(t, cmd.Nodes, 5)

	f = valid()
	for i := range maxGroups + 1 {
		g := fmt.Sprintf("w%d", i)
		f.Add("wg", g)
		f.Set(g+"-count", "1")
	}
	cmd, err = build(t, "create-cluster", f)
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, Problem{Field: "w16-count", Label: "Workers", Text: "a cluster here has at most 16 worker groups"}, cmd.Problems[0])
	assert.Equal(t, 1+maxGroups, strings.Count(cmd.Config.Content, "role:"))
}

func TestClusterFieldsOf(t *testing.T) {
	fields := ClusterFieldsOf(form("wg", "w0", "wg", "bad key", "wg", "wz"))
	assert.Equal(t, []string{"name", "image", "controller-image"}, fields[:3])
	assert.Contains(t, fields, "controller-unmanaged")
	assert.NotContains(t, fields, "submitter-unmanaged", "the submitter is always managed")
	assert.Contains(t, fields, "wz-securityOpt")
	assert.Less(t, indexOf(fields, "w0-count"), indexOf(fields, "wz-count"))
	assert.Less(t, indexOf(fields, "wz-securityOpt"), indexOf(fields, "monitoring"))
	assert.Equal(t, "yaml", fields[len(fields)-1])
	for _, f := range fields {
		assert.NotContains(t, f, " ", "a key that the dialog did not make has no fields")
	}
	assert.Equal(t, []string{"w0"}, WorkerGroups(form()))
}

// groups adds worker groups to the form.
func groups(f Form, keys ...string) Form {
	f["wg"] = append(f["wg"], keys...)
	return f
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestMemoryLabel(t *testing.T) {
	for mb, want := range map[int]string{512: "512 MiB", 1024: "1 GiB", 1536: "1.5 GiB", 54 * 1024: "54 GiB", 1100: "1.1 GiB"} {
		assert.Equal(t, want, MemoryLabel(mb))
	}
}
