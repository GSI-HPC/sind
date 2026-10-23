// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"io/fs"
	"net/url"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// node returns a node of the cluster in realm sind.
func node(clusterName, short string, role config.Role) cluster.NodeSummary {
	return cluster.NodeSummary{
		Container: "sind-" + clusterName + "-" + short, Cluster: clusterName, Role: role,
		FQDN: cluster.DNSName(short, clusterName, "sind"), State: cluster.StateRunning,
	}
}

// testContext holds realms sind (clusters dev and default) and ci (cluster
// ci1).
func testContext() Context { return testStore().ctx() }

// testStore is the store behind testContext.
func testStore() *testStoreT {
	st := store.New()
	st.SetRealms([]cluster.RealmSummary{{Name: "ci", Clusters: 1}, {Name: "sind", Clusters: 2}})
	st.SetClusters("sind", []cluster.Summary{{Name: "default"}, {Name: "dev"}})
	st.SetClusters("ci", []cluster.Summary{{Name: "ci1"}})
	st.SetNodes("sind", []cluster.NodeSummary{
		node("dev", "controller", config.RoleController),
		node("dev", "worker-0", config.RoleWorker), node("dev", "worker-1", config.RoleWorker),
		node("dev", "worker-2", config.RoleWorker), node("dev", "worker-3", config.RoleWorker),
		node("default", "controller", config.RoleController), node("default", "worker-0", config.RoleWorker),
	})
	st.SetNodes("ci", []cluster.NodeSummary{{Container: "ci-ci1-worker-0", Cluster: "ci1", Role: config.RoleWorker, FQDN: cluster.DNSName("worker-0", "ci1", "ci")}})
	return &testStoreT{st}
}

type testStoreT struct{ *store.Store }

func (s *testStoreT) ctx() Context {
	return Context{Snap: s.Snapshot(), Home: "/home/ada", ConfigDir: "/state/sind/ui/configs", Stat: testStat}
}

// testStat finds a directory at every path, but those with missing, file
// or denied in them.
func testStat(name string) (fs.FileInfo, error) {
	switch {
	case strings.Contains(name, "missing"):
		return nil, fs.ErrNotExist
	case strings.Contains(name, "denied"):
		return nil, fs.ErrPermission
	}
	return testFileInfo{dir: !strings.Contains(name, "file")}, nil
}

// testFileInfo is a file that testStat finds.
type testFileInfo struct {
	fs.FileInfo
	dir bool
}

func (fi testFileInfo) IsDir() bool { return fi.dir }

func form(kv ...string) Form {
	f := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Add(kv[i], kv[i+1])
	}
	return f
}

func build(t *testing.T, id string, f Form) (Command, error) {
	t.Helper()
	a, ok := Lookup(id)
	require.True(t, ok, id)
	return a.Build(f, testContext())
}

func TestIDs(t *testing.T) {
	assert.Equal(t, []string{
		"connect", "create-cluster", "create-worker", "delete-cluster", "delete-realm", "delete-worker", "disconnect", "power",
	}, IDs())
	_, ok := Lookup("nope")
	assert.False(t, ok)
}

func TestRegister_Twice(t *testing.T) {
	assert.PanicsWithValue(t, "action power registered twice", func() { register(Action{ID: "power"}) })
}

func TestQuote(t *testing.T) {
	assert.Equal(t, `sind power on 'worker-[0-1].dev' 'it'\''s' '' 'a b' 'ü'`, Quote([]string{"sind", "power", "on", "worker-[0-1].dev", "it's", "", "a b", "ü"}))
	assert.Equal(t, "sind --realm ci get cluster /a/b_c@d%e+f=g:h,i.j-k", Quote([]string{"sind", "--realm", "ci", "get", "cluster", "/a/b_c@d%e+f=g:h,i.j-k"}))
}

func TestCommand_Fingerprint(t *testing.T) {
	a := NewCommand("sind", "x")
	b := NewCommand("sind", "y")
	c := a
	c.Config = &File{Path: "/p", Content: "one"}
	d := c
	d.Config = &File{Path: "/p", Content: "two"}
	e := c
	e.Config = &File{Path: "/q", Content: "one"}
	fps := map[string]bool{a.Fingerprint(): true, b.Fingerprint(): true, c.Fingerprint(): true, d.Fingerprint(): true, e.Fingerprint(): true}
	assert.Len(t, fps, 5)
	assert.Equal(t, a.Fingerprint(), NewCommand("sind", "x").Fingerprint())
	assert.Len(t, a.Fingerprint(), 64)
}

func TestAction_Confirmation(t *testing.T) {
	del, _ := Lookup("delete-cluster")
	f := form("realm", "sind", "cluster", "dev")
	assert.Equal(t, "dev", del.Want(f))
	assert.False(t, del.Confirmed(f))
	assert.False(t, del.Confirmed(form("cluster", "dev", "confirm", "de")))
	assert.True(t, del.Confirmed(form("cluster", "dev", "confirm", "  dev ")))

	pw, _ := Lookup("power")
	assert.Equal(t, "", pw.Want(f))
	assert.True(t, pw.Confirmed(f))

	realm, _ := Lookup("delete-realm")
	assert.Equal(t, "sind", realm.Want(form()))
	assert.Equal(t, "ci", realm.Want(form("realm", " ci ")))
}

func TestAction_Target(t *testing.T) {
	del, _ := Lookup("delete-cluster")
	r, c := del.Target(form("cluster", " dev "))
	assert.Equal(t, "sind", r)
	assert.Equal(t, "dev", c)
	create, _ := Lookup("create-cluster")
	r, c = create.Target(form("realm", "ci", "name", "hpc"))
	assert.Equal(t, "ci", r)
	assert.Equal(t, "hpc", c)
	realm, _ := Lookup("delete-realm")
	r, c = realm.Target(form("realm", "ci"))
	assert.Equal(t, "ci", r)
	assert.Empty(t, c)
}

func TestSindArgv(t *testing.T) {
	assert.Equal(t, []string{"sind", "x"}, sindArgv("sind", "x"))
	assert.Equal(t, []string{"sind", "--realm", "ci", "x"}, sindArgv("ci", "x"))
}

func TestNumber(t *testing.T) {
	n, err := number(form("n", " 7 "), "n", "Label", 1, 9, -1)
	require.NoError(t, err)
	assert.Equal(t, 7, n)
	n, err = number(form(), "n", "Label", 1, 9, 3)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	for _, bad := range []string{"", "x", "0", "10", "1.5"} {
		_, err = number(form("n", bad), "n", "Label", 1, 9, -1)
		assert.EqualError(t, err, "Label: write a number from 1 to 9", bad)
	}
}

func TestFindCluster_Errors(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Form
		want string
	}{
		{"bad realm", form("realm", "Bad Realm", "cluster", "dev"), "realm: invalid realm name"},
		{"no cluster", form(), "cluster: say which cluster"},
		{"bad cluster", form("cluster", "Dev!"), "cluster: invalid cluster name"},
		{"unknown cluster", form("cluster", "nope"), "realm sind has no cluster nope"},
		{"other realm", form("realm", "ci", "cluster", "dev"), "realm ci has no cluster dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range []string{"create-worker", "delete-cluster", "delete-worker", "power"} {
				_, err := build(t, id, tc.f)
				require.Error(t, err, id)
				assert.Contains(t, err.Error(), tc.want, id)
			}
		})
	}
}

func TestBuildCreateWorker(t *testing.T) {
	cmd, err := build(t, "create-worker", form("cluster", "dev", "count", "2"))
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "create", "worker", "dev", "--count", "2"}, cmd.Argv)
	assert.Equal(t, "sind create worker dev --count 2", cmd.Display)
	assert.Equal(t, []string{"Adds worker-[4-5].dev and tells slurmctld about them."}, cmd.Notes)

	cmd, err = build(t, "create-worker", form("realm", "ci", "cluster", "ci1", "count", "1", "cpus", "4", "memory", "2g"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci create worker ci1 --cpus 4 --memory 2g", cmd.Display)
	assert.Equal(t, []string{"Adds worker-1.ci1 and tells slurmctld about them."}, cmd.Notes)

	cmd, err = build(t, "create-worker", form("cluster", "default", "count", "1"))
	require.NoError(t, err)
	assert.Equal(t, "sind create worker", cmd.Display)
	assert.Equal(t, []string{"Adds worker-1 and tells slurmctld about them."}, cmd.Notes)
}

func TestBuildCreateWorker_Errors(t *testing.T) {
	for field, tc := range map[string]struct{ f, want string }{
		"count":  {"count=0", "How many: write a number from 1 to 1024"},
		"count2": {"", "How many: write a number from 1 to 1024"},
		"cpus":   {"count=1&cpus=x", "CPUs: write a number from 1 to 1024"},
		"memory": {"count=1&memory=lots", `memory: invalid memory "lots"`},
	} {
		t.Run(field, func(t *testing.T) {
			f, _ := url.ParseQuery(tc.f)
			f.Set("cluster", "dev")
			_, err := build(t, "create-worker", f)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestBuildDeleteCluster(t *testing.T) {
	cmd, err := build(t, "delete-cluster", form("cluster", "dev"))
	require.NoError(t, err)
	assert.Equal(t, "sind delete cluster dev", cmd.Display)
	assert.Empty(t, cmd.Notes)

	cmd, err = build(t, "delete-cluster", form("realm", "ci", "cluster", "ci1"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci delete cluster ci1", cmd.Display)
	assert.Contains(t, cmd.Notes[0], "last cluster of the realm")

	cmd, err = build(t, "delete-cluster", form("cluster", "default"))
	require.NoError(t, err)
	assert.Equal(t, "sind delete cluster", cmd.Display)
}

func TestBuildDeleteWorker(t *testing.T) {
	cmd, err := build(t, "delete-worker", form("cluster", "dev", "nodes", "worker-3, worker-2", "nodes", "worker-2"))
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "delete", "worker", "worker-[2-3].dev"}, cmd.Argv)
	assert.Equal(t, "sind delete worker 'worker-[2-3].dev'", cmd.Display)

	cmd, err = build(t, "delete-worker", form("cluster", "default", "nodes", "worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "sind delete worker worker-0", cmd.Display)

	_, err = build(t, "delete-worker", form("cluster", "dev"))
	assert.EqualError(t, err, "nodes: pick at least one node of dev")
	_, err = build(t, "delete-worker", form("cluster", "dev", "nodes", "worker-9"))
	assert.EqualError(t, err, "nodes: cluster dev has no node worker-9")
	_, err = build(t, "delete-worker", form("cluster", "dev", "nodes", "controller"))
	assert.EqualError(t, err, "nodes: controller is the controller, and only workers can be deleted")
}

func TestBuildDeleteRealm(t *testing.T) {
	cmd, err := build(t, "delete-realm", form("realm", "ci"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci delete realm", cmd.Display)
	assert.Contains(t, cmd.Notes[0], "all 1 clusters of realm ci")

	cmd, err = build(t, "delete-realm", form())
	require.NoError(t, err)
	assert.Equal(t, "sind delete realm", cmd.Display)

	_, err = build(t, "delete-realm", form("realm", "gone"))
	assert.EqualError(t, err, "realm: sind ui sees no realm gone")
	_, err = build(t, "delete-realm", form("realm", "No"))
	assert.ErrorContains(t, err, "realm: invalid realm name")
}

func TestBuildPower(t *testing.T) {
	for _, tc := range []struct{ op, sub string }{
		{"shutdown", "shutdown"}, {"stop", "shutdown"}, {"off", "shutdown"}, {"on", "on"}, {"start", "on"},
		{"reboot", "reboot"}, {"restart", "reboot"}, {"cycle", "cycle"}, {"cut", "cut"},
		{"freeze", "freeze"}, {"pause", "freeze"}, {"unfreeze", "unfreeze"}, {"unpause", "unfreeze"}, {"resume", "unfreeze"},
	} {
		cmd, err := build(t, "power", form("cluster", "dev", "op", tc.op, "nodes", "worker-0,worker-1,worker-2"))
		require.NoError(t, err, tc.op)
		assert.Equal(t, "sind power "+tc.sub+" 'worker-[0-2].dev'", cmd.Display)
		danger := tc.sub == "cut" || tc.sub == "cycle"
		assert.Equal(t, danger, len(cmd.Warns) == 1, tc.op)
	}

	cmd, err := build(t, "power", form("realm", "ci", "cluster", "ci1", "op", "on", "nodes", "worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci power on worker-0.ci1", cmd.Display)

	_, err = build(t, "power", form("cluster", "dev", "op", "melt", "nodes", "worker-0"))
	assert.EqualError(t, err, "op: pick what to do with the nodes")
	_, err = build(t, "power", form("cluster", "dev", "op", "on"))
	assert.EqualError(t, err, "nodes: pick at least one node of dev")
	var missing *Missing
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "Pick at least one node of dev.", missing.Hint())
	cmd, err = build(t, "power", form("cluster", "dev", "op", "on", "nodes", "controller"))
	require.NoError(t, err)
	assert.Equal(t, "sind power on controller.dev", cmd.Display)
}

func TestPowerOpOf(t *testing.T) {
	op, ok := PowerOpOf("cut")
	assert.True(t, ok)
	assert.True(t, op.Danger)
	op, ok = PowerOpOf("unpause")
	assert.True(t, ok)
	assert.Equal(t, "unfreeze", op.ID)
	_, ok = PowerOpOf("")
	assert.False(t, ok)
	for _, op := range PowerOps {
		assert.NotEmpty(t, op.Label)
		assert.NotEmpty(t, op.Text)
	}
}

func TestBuildConnect(t *testing.T) {
	cmd, err := build(t, "connect", form("container", "kind-control-plane"))
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "connect", "container", "kind-control-plane"}, cmd.Argv)

	cmd, err = build(t, "disconnect", form("realm", "ci", "container", " kind-1 "))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci disconnect container kind-1", cmd.Display)

	for _, bad := range []string{"", " ", "-rf", "a b", "a;b", "$(x)"} {
		_, err = build(t, "connect", form("container", bad))
		assert.EqualError(t, err, "container: write the name of a container, such as kind-control-plane", bad)
		var missing *Missing
		assert.Equal(t, strings.TrimSpace(bad) == "", errors.As(err, &missing), bad)
	}
	_, err = build(t, "connect", form("realm", "No", "container", "x"))
	assert.ErrorContains(t, err, "realm: invalid realm name")
}

func TestNodeWords(t *testing.T) {
	assert.Equal(t, []string{"worker-[0-3].dev"}, nodeWords("dev", []string{"worker-3", "worker-0", "worker-1", "worker-2"}))
	assert.Equal(t, []string{"worker-[0-1]"}, nodeWords("default", []string{"worker-0", "worker-1"}))
	assert.Equal(t, []string{"controller.dev"}, nodeWords("dev", []string{"controller"}))
	// A name the folding cannot take back out unchanged stays as it is.
	odd := []string{"a b.dev"}
	assert.Equal(t, odd, nodeWords("dev", []string{"a b"}))
	assert.Equal(t, []string{"x[.dev"}, nodeWords("dev", []string{"x["}))
}

func TestNodeNames(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, nodeNames(form("nodes", "a, b", "nodes", "b\tc\n")))
	assert.Empty(t, nodeNames(form()))
	assert.True(t, strings.HasPrefix(ShortName(node("dev", "worker-0", config.RoleWorker)), "worker-0"))
}

func TestNextWorkers(t *testing.T) {
	assert.Equal(t, []string{"worker-0", "worker-1"}, nextWorkers(nil, 2))
	nodes := []cluster.NodeSummary{
		node("dev", "controller", config.RoleController), node("dev", "worker-0", config.RoleWorker),
		node("dev", "worker-7", config.RoleWorker), node("dev", "worker-x", config.RoleWorker), node("dev", "worker", config.RoleWorker),
	}
	assert.Equal(t, []string{"worker-8"}, nextWorkers(nodes, 1))
}
