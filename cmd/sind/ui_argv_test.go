// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uiActionContext holds realm sind, with cluster dev of a controller and
// two workers that run their services, and realm ci, with cluster ci1.
func uiActionContext(t *testing.T) action.Context {
	t.Helper()
	st := store.New()
	st.SetRealms([]cluster.RealmSummary{{Name: "ci", Clusters: 1}, {Name: "sind", Clusters: 1}})
	st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	st.SetClusters("ci", []cluster.Summary{{Name: "ci1"}})
	node := func(realm, cl, short string, role config.Role) cluster.NodeSummary {
		return cluster.NodeSummary{Container: realm + "-" + cl + "-" + short, Cluster: cl, Role: role,
			FQDN: cluster.DNSName(short, cl, realm), State: cluster.StateRunning}
	}
	st.SetNodes("sind", []cluster.NodeSummary{
		node("sind", "dev", "controller", config.RoleController),
		node("sind", "dev", "worker-0", config.RoleWorker), node("sind", "dev", "worker-1", config.RoleWorker),
	})
	st.SetNodes("ci", []cluster.NodeSummary{node("ci", "ci1", "controller", config.RoleController), node("ci", "ci1", "worker-0", config.RoleWorker)})
	health := &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{probe.ServiceSlurmd: true, probe.ServiceSSHD: true}}
	st.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{
		{Name: "controller.dev", Role: config.RoleController, Managed: true},
		{Name: "worker-0.dev", Role: config.RoleWorker, Managed: true, Health: health},
		{Name: "worker-1.dev", Role: config.RoleWorker, Managed: true},
	}})
	dir, err := os.Stat(t.TempDir())
	require.NoError(t, err)
	return action.Context{Snap: st.Snapshot(), Home: "/home/ada", ConfigDir: "/state/sind/ui/configs",
		Stat: func(string) (fs.FileInfo, error) { return dir, nil }}
}

// uiForm is a form of the fields and values kv.
func uiForm(kv ...string) action.Form {
	f := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Add(kv[i], kv[i+1])
	}
	return f
}

// uiActionForms are forms of every action of sind ui, which together give
// every flag that the actions put on a command line.
func uiActionForms() map[string][]action.Form {
	forms := map[string][]action.Form{
		"create-cluster": {
			uiForm("name", "hpc", "w0-count", "2", "dir", "/scratch/hpc"),
			uiForm("realm", "ci", "name", "hpc", "w0-count", "2", "dir", "/scratch/hpc", "pull", "on", "wait", "10m"),
			uiForm("yaml_on", "1", "yaml", "kind: Cluster\nname: lab\n", "wait", "0"),
		},
		"create-worker": {
			uiForm("cluster", "dev", "count", "2"),
			uiForm("realm", "ci", "cluster", "ci1", "count", "1", "cpus", "4", "memory", "2g"),
		},
		"delete-cluster":  {uiForm("cluster", "dev"), uiForm("realm", "ci", "cluster", "ci1")},
		"delete-clusters": {uiForm("clusters", "sind/dev,ci/ci1")},
		"delete-worker":   {uiForm("cluster", "dev", "nodes", "worker-1"), uiForm("realm", "ci", "cluster", "ci1", "nodes", "worker-0")},
		"delete-realm":    {uiForm("realm", "ci")},
		"connect":         {uiForm("container", "kind-control-plane"), uiForm("realm", "ci", "container", "kind-1")},
		"disconnect":      {uiForm("container", "kind-control-plane"), uiForm("realm", "ci", "container", "kind-1")},
		"restart-service": {uiForm("cluster", "dev", "nodes", "worker-0", "unit", "slurmd")},
	}
	for _, op := range action.PowerOps {
		forms["power"] = append(forms["power"],
			uiForm("cluster", "dev", "op", op.ID, "nodes", "worker-0,worker-1", "reason", "disk test"),
			uiForm("realm", "ci", "cluster", "ci1", "op", op.ID, "nodes", "worker-0", "reason", "disk test"))
	}
	return forms
}

// checkSindArgv checks that argv is a command line of sind: a command, not
// a group, with flags that it has, values that they take, and arguments
// that it takes.
func checkSindArgv(t *testing.T, argv []string) {
	t.Helper()
	require.NotEmpty(t, argv)
	require.Equal(t, "sind", argv[0], argv)
	root := NewRootCommand()
	found, rest, err := root.Find(argv[1:])
	require.NoError(t, err, argv)
	require.NotEqual(t, root, found, argv)
	assert.False(t, found.HasSubCommands(), "%q names a group, not a command", argv)
	require.NoError(t, found.ParseFlags(rest), argv)
	assert.NoError(t, found.ValidateArgs(found.Flags().Args()), argv)
}

// TestUICommandsExist checks that every command line that the actions of
// sind ui run is a command of sind, as TestHelpCommandsExist does for the
// help texts. sind ui cannot reach the command tree of this package.
func TestUICommandsExist(t *testing.T) {
	c := uiActionContext(t)
	forms := uiActionForms()
	assert.ElementsMatch(t, action.IDs(), slices.Collect(maps.Keys(forms)), "a form for each action")
	var seen []string
	for id, list := range forms {
		a, ok := action.Lookup(id)
		require.True(t, ok, id)
		for _, f := range list {
			cmd, err := a.Build(f, c)
			require.NoError(t, err, "%s %v", id, f)
			require.Empty(t, cmd.Problems, "%s %v", id, f)
			argvs := [][]string{cmd.Argv}
			if len(cmd.Parts) > 0 {
				argvs = nil
				for _, p := range cmd.Parts {
					argvs = append(argvs, p.Argv)
				}
			}
			for _, argv := range argvs {
				checkSindArgv(t, argv)
				seen = append(seen, argv...)
			}
		}
	}

	// Each flag that the actions name comes up in a command line above.
	dir := filepath.Join("..", "..", "internal", "ui", "action")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	flag := regexp.MustCompile(`"(--[a-z][a-z-]*)"`)
	var flags []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for _, m := range flag.FindAllSubmatch(src, -1) {
			flags = append(flags, string(m[1]))
		}
	}
	require.NotEmpty(t, flags)
	for _, fl := range flags {
		assert.True(t, slices.ContainsFunc(seen, func(w string) bool { return w == fl || strings.HasPrefix(w, fl+"=") }),
			"no form gives %s: add one to uiActionForms", fl)
	}
}

func TestCheckSindArgv(t *testing.T) {
	for _, argv := range [][]string{
		{"sind", "--realm", "ci", "create", "cluster", "--config", "x.yaml", "--wait", "0"},
		{"sind", "ssh", "worker-0.dev", "--", "systemctl", "restart", "slurmd"},
	} {
		checkSindArgv(t, argv)
	}
}
