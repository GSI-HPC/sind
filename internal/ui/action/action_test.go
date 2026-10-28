// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"io/fs"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
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
		"connect", "create-cluster", "create-worker", "delete-cluster", "delete-clusters", "delete-realm", "delete-worker", "disconnect", "power", "restart-service",
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

	// One worker is confirmed with its name, more with the cluster's.
	workers, _ := Lookup("delete-worker")
	assert.Equal(t, "worker-1", workers.Want(form("cluster", "dev", "nodes", "worker-1")))
	assert.Equal(t, "dev", workers.Want(form("cluster", "dev", "nodes", "worker-1,worker-2")))
	assert.Equal(t, "dev", workers.Want(form("cluster", "dev")))
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
		{"bad realm", form("realm", "Bad Realm", "cluster", "dev"), "realm: use "},
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
	assert.Equal(t, []string{"sind adds the workers, then tells slurmctld about them."}, cmd.Notes)
	assert.Equal(t, "2 × the CPUs of worker-3, the memory of worker-3: worker-4 … worker-5", cmd.Summary)
	assert.Equal(t, []PlannedNode{{Short: "worker-4", Role: "worker"}, {Short: "worker-5", Role: "worker"}}, cmd.Nodes)
	assert.Equal(t, "Add 2 workers", cmd.OK)

	cmd, err = build(t, "create-worker", form("realm", "ci", "cluster", "ci1", "count", "1", "cpus", "4", "memory", "2g"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci create worker ci1 --cpus 4 --memory 2g", cmd.Display)
	assert.Equal(t, "1 × 4 CPUs, 2 GiB: worker-1", cmd.Summary)
	assert.Equal(t, "Add worker-1", cmd.OK)

	cmd, err = build(t, "create-worker", form("cluster", "default", "count", "1"))
	require.NoError(t, err)
	assert.Equal(t, "sind create worker", cmd.Display)
}

func TestBuildCreateWorker_Sizes(t *testing.T) {
	st := testStore()
	c := st.ctx()
	var asked []string
	c.Worker = func(container string) WorkerFacts {
		asked = append(asked, container)
		if container == "sind-default-worker-0" {
			return WorkerFacts{Err: "docker: gone"}
		}
		return WorkerFacts{CPUs: 1, Memory: 1536 << 20}
	}
	a, _ := Lookup("create-worker")

	cmd, err := a.Build(form("cluster", "dev", "count", "3"), c)
	require.NoError(t, err)
	assert.Equal(t, []string{"sind-dev-worker-3"}, asked, "the newest worker")
	assert.Equal(t, "3 × 1 CPU, 1.5 GiB: worker-4 … worker-6", cmd.Summary)
	cmd, err = a.Build(form("cluster", "dev", "count", "1", "cpus", "2"), c)
	require.NoError(t, err)
	assert.Equal(t, "1 × 2 CPUs, 1.5 GiB: worker-4", cmd.Summary)

	cmd, err = a.Build(form("cluster", "default", "count", "1"), c)
	require.NoError(t, err)
	assert.Equal(t, "1 × the CPUs of worker-0, the memory of worker-0: worker-1", cmd.Summary)
	assert.Equal(t, []string{"sind ui cannot look at the size of worker-0: docker: gone. sind adds the workers, then tells slurmctld about them."}, cmd.Notes)

	// A worker without limits gives new workers sind's defaults, as sind
	// does.
	c.Worker = func(string) WorkerFacts { return WorkerFacts{} }
	cmd, err = a.Build(form("cluster", "dev", "count", "1"), c)
	require.NoError(t, err)
	assert.Equal(t, "1 × 1 CPU, 512 MiB: worker-4", cmd.Summary)

	// New workers of a managed cluster are like its newest managed worker.
	st.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{
		{Name: "controller.dev", Role: config.RoleController, Managed: true},
		{Name: "worker-2.dev", Role: config.RoleWorker, Managed: true},
		{Name: "worker-3.dev", Role: config.RoleWorker, Managed: false},
	}})
	asked = nil
	c = st.ctx()
	c.Worker = func(container string) WorkerFacts {
		asked = append(asked, container)
		return WorkerFacts{CPUs: 2, Memory: 1 << 30}
	}
	cmd, err = a.Build(form("cluster", "dev", "count", "1"), c)
	require.NoError(t, err)
	assert.Equal(t, []string{"sind-dev-worker-2"}, asked)
	assert.Equal(t, "1 × 2 CPUs, 1 GiB: worker-4", cmd.Summary, "the names still follow the highest number")

	// A cluster without a worker takes sind's defaults.
	st.SetNodes("ci", nil)
	cmd, err = a.Build(form("realm", "ci", "cluster", "ci1", "count", "2"), st.ctx())
	require.NoError(t, err)
	assert.Equal(t, "2 × 1 CPU, 512 MiB: worker-0 … worker-1", cmd.Summary)
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
	st := testStore()
	st.SetVolumes("sind", []cluster.VolumeSummary{
		{Name: "sind-dev-data"}, {Name: "sind-dev-config"}, {Name: "sind-default-config"},
		{Name: "sind-dev-gpu-config"}, {Name: "sind-dev-gpu-munge"}, {Name: "sind-dev-other"},
	})
	st.SetStatus("sind", "dev", &cluster.Status{Mounts: []cluster.MountPoint{
		{Path: "/etc/slurm", Source: "sind-dev-config", Type: config.StorageVolume},
		{Path: "/data", Source: "/home/ada/dev", Type: config.StorageHostPath},
	}})
	a, _ := Lookup("delete-cluster")

	cmd, err := a.Build(form("cluster", "dev"), st.ctx())
	require.NoError(t, err)
	assert.Equal(t, "sind delete cluster dev", cmd.Display)
	assert.Equal(t, "Delete cluster dev", cmd.OK)
	assert.Equal(t, []string{
		"5 containers: controller, worker-0, worker-1, worker-2, worker-3", "the network sind-dev-net",
		"the volumes sind-dev-config, sind-dev-data", "its jobs, its accounting and its Slurm state",
	}, cmd.Goes)
	assert.Equal(t, []string{"the host directory /home/ada/dev, mounted at /data", "the other cluster of realm sind, and its mesh"}, cmd.Stays)

	cmd, err = build(t, "delete-cluster", form("realm", "ci", "cluster", "ci1"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci delete cluster ci1", cmd.Display)
	assert.Equal(t, []string{"1 container: worker-0", "the network ci-ci1-net", "its jobs, its accounting and its Slurm state",
		"the mesh, the DNS server and the SSH relay of realm ci, as it is its last cluster"}, cmd.Goes)
	assert.Equal(t, []string{"the host directories it mounts, if any"}, cmd.Stays, "sind ui has not read its status")

	st.SetVolumes("sind", []cluster.VolumeSummary{{Name: "sind-dev-data"}})
	st.SetClusters("sind", []cluster.Summary{{Name: "default"}, {Name: "dev"}, {Name: "hpc"}})
	cmd, err = a.Build(form("cluster", "dev"), st.ctx())
	require.NoError(t, err)
	assert.Contains(t, cmd.Goes, "the volume sind-dev-data")
	assert.Contains(t, cmd.Stays, "the other 2 clusters of realm sind, and its mesh")

	cmd, err = build(t, "delete-cluster", form("cluster", "default"))
	require.NoError(t, err)
	assert.Equal(t, "sind delete cluster", cmd.Display)
}

func TestBuildDeleteClusters(t *testing.T) {
	cmd, err := build(t, "delete-clusters", form("clusters", "sind/dev,ci/ci1", "clusters", "sind/default sind/dev"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci delete cluster ci1\nsind delete cluster\nsind delete cluster dev", cmd.Display)
	assert.Empty(t, cmd.Argv)
	require.Len(t, cmd.Parts, 3)
	assert.Equal(t, Part{Action: "delete-cluster", Realm: "ci", Cluster: "ci1", Argv: []string{"sind", "--realm", "ci", "delete", "cluster", "ci1"}, Display: "sind --realm ci delete cluster ci1"}, cmd.Parts[0])
	assert.Equal(t, []string{"sind", "delete", "cluster"}, cmd.Parts[1].Argv)
	assert.Equal(t, "Delete 3 clusters", cmd.OK)
	assert.Contains(t, cmd.Notes[0], "one command for each cluster")
	assert.Equal(t, []string{
		"These are all the clusters of realm ci, so sind also removes its mesh, DNS server and SSH relay.",
		"These are all the clusters of realm sind, so sind also removes its mesh, DNS server and SSH relay.",
	}, cmd.Notes[1:])
	a, _ := Lookup("delete-clusters")
	assert.Equal(t, "3", a.Want(form("clusters", "sind/dev,ci/ci1", "clusters", "sind/default sind/dev")))
	assert.Empty(t, a.Want(form()), "without clusters there is nothing to confirm")
	realm, cl := a.Target(form("clusters", "sind/dev"))
	assert.Equal(t, "sind", realm)
	assert.Empty(t, cl)

	cmd, err = build(t, "delete-clusters", form("clusters", "sind/dev"))
	require.NoError(t, err)
	assert.Len(t, cmd.Notes, 1, "sind keeps a realm that has a cluster left")
	assert.Equal(t, "Delete 1 cluster", cmd.OK)

	_, err = build(t, "delete-clusters", form())
	var missing *Missing
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "clusters", missing.Field)

	many := make([]string, 33)
	for i := range many {
		many[i] = "sind/c" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	for _, tc := range []struct{ value, want string }{
		{strings.Join(many, ","), "pick at most 32 clusters"},
		{"dev", `"dev" is not REALM/CLUSTER`},
		{"Sind/dev", "clusters: "},
		{"sind/Dev", "clusters: "},
		{"sind/gone", "realm sind has no cluster gone"},
	} {
		_, err := build(t, "delete-clusters", form("clusters", tc.value))
		require.Error(t, err, tc.value)
		assert.Contains(t, err.Error(), tc.want, tc.value)
	}
}

func TestBuildDeleteWorker(t *testing.T) {
	cmd, err := build(t, "delete-worker", form("cluster", "dev", "nodes", "worker-3, worker-2", "nodes", "worker-2"))
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "delete", "worker", "worker-[2-3].dev"}, cmd.Argv)
	assert.Equal(t, "sind delete worker 'worker-[2-3].dev'", cmd.Display)
	assert.Equal(t, "Delete 2 workers", cmd.OK)
	assert.Equal(t, []string{"the containers sind-dev-worker-3, sind-dev-worker-2 and their DNS records", "their Slurm nodes: jobs on them fail"}, cmd.Goes)
	assert.Equal(t, []string{"dev with 2 workers, its controller and its data"}, cmd.Stays)

	cmd, err = build(t, "delete-worker", form("cluster", "default", "nodes", "worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "sind delete worker worker-0", cmd.Display)
	assert.Equal(t, "Delete worker-0", cmd.OK)
	assert.Equal(t, []string{"the container sind-default-worker-0 and its DNS record", "its Slurm node: jobs on it fail"}, cmd.Goes)
	assert.Equal(t, []string{"default with no worker, its controller and its data: jobs wait until you add workers"}, cmd.Stays)

	cmd, err = build(t, "delete-worker", form("cluster", "dev", "nodes", "worker-1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"dev with 3 workers, its controller and its data"}, cmd.Stays)

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
	assert.Equal(t, "Delete realm ci", cmd.OK)
	assert.Equal(t, []string{"1 cluster: ci1", "the mesh network, the DNS server and the SSH relay", "the exported SSH config"}, cmd.Goes)
	assert.Equal(t, []string{"the host directories that its clusters mount, if any", "the containers that sind did not make, if any: sind detaches them"}, cmd.Stays,
		"sind ui has read neither the status of ci1 nor the mesh")
	assert.Empty(t, cmd.Notes)

	// The default realm is named too, so that a copy deletes this realm.
	st := testStore()
	st.SetMonitoring("sind", &cluster.Monitoring{})
	st.SetMesh("sind", store.Mesh{External: []string{"kind-control-plane"}})
	st.SetStatus("sind", "default", &cluster.Status{Mounts: []cluster.MountPoint{
		{Path: "/etc/slurm", Source: "sind-default-config", Type: config.StorageVolume},
		{Path: "/data", Source: "/home/ada/data", Type: config.StorageHostPath},
	}})
	st.SetStatus("sind", "dev", &cluster.Status{})
	a, _ := Lookup("delete-realm")
	cmd, err = a.Build(form(), st.ctx())
	require.NoError(t, err)
	assert.Equal(t, "sind --realm sind delete realm", cmd.Display)
	assert.Equal(t, []string{"2 clusters: default, dev", "the mesh network, the DNS server and the SSH relay", "the monitoring stack", "the exported SSH config"}, cmd.Goes)
	assert.Equal(t, []string{
		"the host directory /home/ada/data, mounted at /data in default",
		"the containers that sind did not make, kind-control-plane: sind detaches them",
	}, cmd.Stays)

	// No line about containers that sind did not make where there are none.
	st.SetMesh("sind", store.Mesh{Info: &mesh.Info{}})
	cmd, err = a.Build(form(), st.ctx())
	require.NoError(t, err)
	assert.Equal(t, []string{"the host directory /home/ada/data, mounted at /data in default"}, cmd.Stays)
	st.SetMesh("sind", store.Mesh{Err: "docker: gone"})
	cmd, err = a.Build(form(), st.ctx())
	require.NoError(t, err)
	assert.Contains(t, cmd.Stays, "the containers that sind did not make, if any: sind detaches them", "sind ui could not read the mesh")

	st.SetClusters("ci", nil)
	cmd, err = a.Build(form("realm", "ci"), st.ctx())
	require.NoError(t, err)
	assert.Equal(t, []string{"the mesh network, the DNS server and the SSH relay", "the exported SSH config"}, cmd.Goes, "a realm without clusters")

	_, err = build(t, "delete-realm", form("realm", "gone"))
	assert.EqualError(t, err, "realm: sind ui sees no realm gone")
	_, err = build(t, "delete-realm", form("realm", "No"))
	assert.ErrorContains(t, err, "realm: use ")
}

func TestBuildPower(t *testing.T) {
	for _, tc := range []struct{ op, sub string }{
		{"shutdown", "shutdown"}, {"stop", "shutdown"}, {"off", "shutdown"}, {"on", "on"}, {"start", "on"},
		{"reboot", "reboot"}, {"restart", "reboot"}, {"cycle", "cycle"}, {"cut", "cut"},
		{"freeze", "freeze"}, {"pause", "freeze"}, {"unfreeze", "unfreeze"}, {"unpause", "unfreeze"},
	} {
		cmd, err := build(t, "power", form("cluster", "dev", "op", tc.op, "nodes", "worker-0,worker-1,worker-2"))
		require.NoError(t, err, tc.op)
		assert.Equal(t, "sind power "+tc.sub+" 'worker-[0-2].dev'", cmd.Display)
		danger := tc.sub == "cut" || tc.sub == "cycle"
		assert.Equal(t, danger, len(cmd.Warns) == 1, tc.op)
		op, _ := PowerOpOf(tc.sub)
		assert.Equal(t, op.Label+" 3 nodes", cmd.OK)
	}

	cmd, err := build(t, "power", form("realm", "ci", "cluster", "ci1", "op", "on", "nodes", "worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci power on worker-0.ci1", cmd.Display)
	assert.Equal(t, "Start worker-0", cmd.OK)

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

func TestBuildPower_Warnings(t *testing.T) {
	st := store.New()
	st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 1}})
	st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	backup := node("dev", "controller-backup", config.RoleController)
	st.SetNodes("sind", []cluster.NodeSummary{
		node("dev", "controller", config.RoleController), backup, node("dev", "db", config.RoleDB),
		node("dev", "worker-0", config.RoleWorker), node("dev", "worker-1", config.RoleWorker),
	})
	a, _ := Lookup("power")
	warns := func(op, nodes string) []string {
		t.Helper()
		cmd, err := a.Build(form("cluster", "dev", "op", op, "nodes", nodes), (&testStoreT{st}).ctx())
		require.NoError(t, err)
		return cmd.Warns
	}

	assert.Equal(t, []string{"controller runs slurmctld. controller-backup takes over after SlurmctldTimeout; until then dev takes no jobs."}, warns("shutdown", "controller"))
	assert.Equal(t, []string{"controller and controller-backup run slurmctld. While they are down, dev takes no jobs, and sbatch, squeue and sinfo fail."},
		warns("freeze", "controller,controller-backup"))
	assert.Equal(t, []string{"controller-backup is the standby controller. controller stays in control and dev keeps taking jobs, but has no failover while controller-backup is down."},
		warns("shutdown", "controller-backup"))
	assert.Equal(t, []string{"db runs slurmdbd and MariaDB. While it is down, sacct and sacctmgr fail; slurmctld keeps the accounting records until it is back."}, warns("reboot", "db"))
	assert.Equal(t, []string{"These are all the running workers of dev: new jobs wait until a worker is back."}, warns("shutdown", "worker-0,worker-1"))
	assert.Empty(t, warns("shutdown", "worker-0"))
	assert.Empty(t, warns("on", "controller,db,worker-0,worker-1"), "start stops nothing")
	assert.Len(t, warns("cut", "worker-0,worker-1"), 2, "the last workers and the danger")

	// The status says which controller is in control.
	st.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{
		{Name: "controller.dev", Role: config.RoleController, Health: &cluster.NodeHealth{HA: &cluster.HAStatus{Position: "primary"}}},
		{Name: "controller-backup.dev", Role: config.RoleController, Health: &cluster.NodeHealth{HA: &cluster.HAStatus{Position: "backup", InControl: true}}},
		{Name: "worker-0.dev", Role: config.RoleWorker},
	}})
	assert.Equal(t, []string{"controller-backup runs slurmctld. controller takes over after SlurmctldTimeout; until then dev takes no jobs."}, warns("shutdown", "controller-backup"))
	assert.Equal(t, []string{"controller is the standby controller. controller-backup stays in control and dev keeps taking jobs, but has no failover while controller is down."},
		warns("reboot", "controller"))
	st.SetStatus("sind", "dev", nil)

	// Without the primary, the backup is in control.
	primary := node("dev", "controller", config.RoleController)
	primary.State = cluster.StateStopped
	st.SetNodes("sind", []cluster.NodeSummary{primary, backup, node("dev", "worker-0", config.RoleWorker)})
	assert.Equal(t, []string{"controller-backup runs slurmctld. While it is down, dev takes no jobs, and sbatch, squeue and sinfo fail."}, warns("shutdown", "controller-backup"))
	assert.Empty(t, warns("shutdown", "controller"), "a stopped controller serves nothing")

	// A stopped worker does not count as one that runs jobs.
	stopped := node("dev", "worker-1", config.RoleWorker)
	stopped.State = cluster.StateStopped
	st.SetNodes("sind", []cluster.NodeSummary{node("dev", "controller", config.RoleController), node("dev", "worker-0", config.RoleWorker), stopped})
	assert.Equal(t, []string{
		"controller runs slurmctld. While it is down, dev takes no jobs, and sbatch, squeue and sinfo fail.",
		"These are all the running workers of dev: new jobs wait until a worker is back.",
	}, warns("shutdown", "controller,worker-0"))
	assert.Empty(t, warns("shutdown", "worker-1"))
}

func TestBuildPower_SlurmState(t *testing.T) {
	cmd, err := build(t, "power", form("cluster", "dev", "op", "drain", "nodes", "worker-0,worker-1", "reason", " disk test "))
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "exec", "dev", "--", "scontrol", "update", "NodeName=worker-[0-1]", "State=DRAIN", "Reason=disk test"}, cmd.Argv)
	assert.Equal(t, "sind exec dev -- scontrol update 'NodeName=worker-[0-1]' State=DRAIN 'Reason=disk test'", cmd.Display)
	assert.Equal(t, "Drain 2 nodes", cmd.OK)
	assert.Contains(t, cmd.Notes[0], "Running jobs go on")
	assert.Empty(t, cmd.Warns)

	cmd, err = build(t, "power", form("realm", "ci", "cluster", "ci1", "op", "resume", "nodes", "worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm ci exec ci1 -- scontrol update NodeName=worker-0 State=RESUME", cmd.Display)
	assert.Equal(t, "Resume worker-0", cmd.OK)
	assert.Equal(t, []string{"No controller of ci1 runs, so scontrol fails. Start the controller first."}, cmd.Warns)

	cmd, err = build(t, "power", form("cluster", "default", "op", "resume", "nodes", "worker-0"))
	require.NoError(t, err)
	assert.Equal(t, "sind exec -- scontrol update NodeName=worker-0 State=RESUME", cmd.Display)

	_, err = build(t, "power", form("cluster", "dev", "op", "drain", "nodes", "worker-0"))
	var missing *Missing
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "Write why the nodes drain, such as disk test.", missing.Hint())
	cmd, err = build(t, "power", form("cluster", "dev", "op", "drain", "nodes", "worker-0", "reason", strings.Repeat("ä", 200)))
	require.NoError(t, err, "the length counts characters, not bytes")
	assert.Contains(t, cmd.Display, "Reason="+strings.Repeat("ä", 200))
	for _, bad := range []string{strings.Repeat("x", 201), strings.Repeat("ä", 201), "a\nb"} {
		_, err = build(t, "power", form("cluster", "dev", "op", "drain", "nodes", "worker-0", "reason", bad))
		assert.EqualError(t, err, "reason: write one line of at most 200 characters")
	}
	_, err = build(t, "power", form("cluster", "dev", "op", "drain", "nodes", "controller", "reason", "x"))
	assert.EqualError(t, err, "nodes: controller is the controller, and only workers can be drained or resumed")

	// sind runs no Slurm on an unmanaged cluster or node.
	st := testStore()
	a, _ := Lookup("power")
	st.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{
		{Name: "controller.dev", Role: config.RoleController, Managed: true},
		{Name: "worker-0.dev", Role: config.RoleWorker, Managed: false},
		{Name: "worker-1.dev", Role: config.RoleWorker, Managed: true},
	}})
	_, err = a.Build(form("cluster", "dev", "op", "resume", "nodes", "worker-0"), st.ctx())
	assert.EqualError(t, err, "nodes: worker-0 is unmanaged: it runs no slurmd")
	_, err = a.Build(form("cluster", "dev", "op", "resume", "nodes", "worker-1"), st.ctx())
	require.NoError(t, err)
	st.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{{Name: "controller.dev", Role: config.RoleController}}})
	_, err = a.Build(form("cluster", "dev", "op", "resume", "nodes", "worker-1"), st.ctx())
	assert.EqualError(t, err, "cluster dev is unmanaged: sind runs no Slurm on it")
}

func TestPowerOpOf(t *testing.T) {
	op, ok := PowerOpOf("cut")
	assert.True(t, ok)
	assert.True(t, op.Danger)
	op, ok = PowerOpOf("unpause")
	assert.True(t, ok)
	assert.Equal(t, "unfreeze", op.ID)
	op, ok = PowerOpOf("resume")
	assert.True(t, ok)
	assert.True(t, op.Slurm(), "resume is Slurm's, not unfreeze")
	_, ok = PowerOpOf("")
	assert.False(t, ok)
	for _, op := range PowerOps {
		assert.NotEmpty(t, op.Label)
		assert.NotEmpty(t, op.Text)
	}
}

func TestPowerGroups(t *testing.T) {
	var ids []string
	n := 0
	for _, g := range PowerGroups() {
		ids = append(ids, g.ID)
		assert.NotEmpty(t, g.Label)
		for _, op := range g.Ops {
			assert.Equal(t, g.ID, op.Group)
			assert.Equal(t, g.ID == GroupDanger, op.Danger, op.ID)
			n++
		}
	}
	assert.Equal(t, []string{GroupSlurm, GroupContainers, GroupDanger}, ids)
	assert.Equal(t, len(PowerOps), n, "every op has a group")
}

func TestBuildConnect(t *testing.T) {
	cmd, err := build(t, "connect", form("container", "kind-control-plane"))
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "connect", "container", "kind-control-plane"}, cmd.Argv)
	assert.Empty(t, cmd.Warns, "a realm that sind ui sees")

	cmd, err = build(t, "connect", form("realm", "new", "container", "kind-control-plane"))
	require.NoError(t, err)
	assert.Equal(t, "sind --realm new connect container kind-control-plane", cmd.Display)
	assert.Equal(t, []string{"sind ui sees no realm new. sind connects a container only to a realm with a cluster, whose mesh it joins; create a cluster in new first."}, cmd.Warns)

	cmd, err = build(t, "disconnect", form("realm", "new", "container", "kind-1"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Warns, "disconnect leaves the realm to sind")

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
	assert.ErrorContains(t, err, "realm: use ")
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
	assert.Equal(t, []string{"a", "b", "c"}, NodeNames(form("nodes", "a, b", "nodes", "b\tc\n")))
	assert.Empty(t, NodeNames(form()))
	assert.True(t, strings.HasPrefix(ShortName(node("dev", "worker-0", config.RoleWorker)), "worker-0"))
}

func TestNewestWorker(t *testing.T) {
	nodes := []cluster.NodeSummary{
		node("dev", "controller", config.RoleController), node("dev", "worker-0", config.RoleWorker),
		node("dev", "worker-1", config.RoleWorker), node("dev", "worker-2", config.RoleWorker),
	}
	short := func(st *cluster.Status) string {
		t.Helper()
		n, ok := NewestWorker(nodes, st)
		require.True(t, ok)
		return ShortName(n)
	}
	status := func(ctlManaged bool, managed ...string) *cluster.Status {
		st := &cluster.Status{Nodes: []*cluster.NodeStatus{{Name: "controller.dev", Role: config.RoleController, Managed: ctlManaged}}}
		for _, w := range []string{"worker-0", "worker-1", "worker-2"} {
			st.Nodes = append(st.Nodes, &cluster.NodeStatus{Name: w + ".dev", Role: config.RoleWorker, Managed: slices.Contains(managed, w)})
		}
		return st
	}
	assert.Equal(t, "worker-2", short(nil), "without a status, the highest number")
	assert.Equal(t, "worker-2", short(&cluster.Status{}), "a status without the controller")
	assert.Equal(t, "worker-1", short(status(true, "worker-0", "worker-1")), "the newest managed worker")
	assert.Equal(t, "worker-2", short(status(false, "worker-0", "worker-1")), "the newest unmanaged worker")
	assert.Equal(t, "worker-2", short(status(true)), "no managed worker: the newest of all")
	_, ok := NewestWorker(nodes[:1], status(true))
	assert.False(t, ok)
}

func TestNextWorkers(t *testing.T) {
	assert.Equal(t, []string{"worker-0", "worker-1"}, nextWorkers(nil, 2))
	nodes := []cluster.NodeSummary{
		node("dev", "controller", config.RoleController), node("dev", "worker-0", config.RoleWorker),
		node("dev", "worker-7", config.RoleWorker), node("dev", "worker-x", config.RoleWorker), node("dev", "worker", config.RoleWorker),
	}
	assert.Equal(t, []string{"worker-8"}, nextWorkers(nodes, 1))
}
