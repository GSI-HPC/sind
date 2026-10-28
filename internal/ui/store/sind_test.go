// SPDX-License-Identifier: LGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopics(t *testing.T) {
	assert.Equal(t, Topic("clusters/r"), ClustersTopic("r"))
	assert.Equal(t, Topic("nodes/r"), NodesTopic("r"))
	assert.Equal(t, Topic("networks/r"), NetworksTopic("r"))
	assert.Equal(t, Topic("volumes/r"), VolumesTopic("r"))
	assert.Equal(t, Topic("mesh/r"), MeshTopic("r"))
	assert.Equal(t, Topic("lock/r"), LockTopic("r"))
	assert.Equal(t, Topic("monitoring/r"), MonitoringTopic("r"))
	assert.Equal(t, Topic("status/r/c"), StatusTopic("r", "c"))
	assert.Equal(t, Topic("status/r/c"), StatusKey{"r", "c"}.Topic())
}

// setter is one Set call with a value, and its topic.
type setter struct {
	name  string
	topic Topic
	a, b  func(*Store) // two different values
	empty func(*Store) // removes the part
	view  func(Snapshot) any
}

func setters() []setter {
	return []setter{
		{"realms", TopicRealms,
			func(s *Store) { s.SetRealms([]cluster.RealmSummary{{Name: "b"}, {Name: "a"}}) },
			func(s *Store) { s.SetRealms([]cluster.RealmSummary{{Name: "a", Clusters: 2}}) },
			func(s *Store) { s.SetRealms(nil) },
			func(sn Snapshot) any { return sn.Realms }},
		{"clusters", ClustersTopic("r"),
			func(s *Store) { s.SetClusters("r", []cluster.Summary{{Name: "b"}, {Name: "a"}}) },
			func(s *Store) { s.SetClusters("r", []cluster.Summary{{Name: "a", Workers: 1}}) },
			func(s *Store) { s.SetClusters("r", nil) },
			func(sn Snapshot) any { return sn.Clusters["r"] }},
		{"nodes", NodesTopic("r"),
			func(s *Store) { s.SetNodes("r", []cluster.NodeSummary{{Container: "x"}}) },
			func(s *Store) { s.SetNodes("r", []cluster.NodeSummary{{Container: "x", IP: "1"}}) },
			func(s *Store) { s.SetNodes("r", nil) },
			func(sn Snapshot) any { return sn.Nodes["r"] }},
		{"networks", NetworksTopic("r"),
			func(s *Store) { s.SetNetworks("r", []cluster.NetworkSummary{{Name: "n"}}) },
			func(s *Store) { s.SetNetworks("r", []cluster.NetworkSummary{{Name: "n", Subnet: "s"}}) },
			func(s *Store) { s.SetNetworks("r", nil) },
			func(sn Snapshot) any { return sn.Networks["r"] }},
		{"volumes", VolumesTopic("r"),
			func(s *Store) { s.SetVolumes("r", []cluster.VolumeSummary{{Name: "v"}}) },
			func(s *Store) { s.SetVolumes("r", []cluster.VolumeSummary{{Name: "v", Driver: "d"}}) },
			func(s *Store) { s.SetVolumes("r", nil) },
			func(sn Snapshot) any { return sn.Volumes["r"] }},
		{"mesh", MeshTopic("r"),
			func(s *Store) {
				s.SetMesh("r", Mesh{Info: &mesh.Info{Network: "r-mesh"}, External: []string{"b", "a"}})
			},
			func(s *Store) { s.SetMesh("r", Mesh{Err: "boom"}) },
			func(s *Store) { s.SetMesh("r", Mesh{Records: []mesh.DNSRecord{}, External: []string{}}) },
			func(sn Snapshot) any { return sn.Meshes["r"] }},
		{"status", StatusTopic("r", "c"),
			func(s *Store) { s.SetStatus("r", "c", &cluster.Status{Name: "c"}) },
			func(s *Store) { s.SetStatus("r", "c", &cluster.Status{Name: "c", State: cluster.StateRunning}) },
			func(s *Store) { s.SetStatus("r", "c", nil) },
			func(sn Snapshot) any { return sn.Status("r", "c") }},
		{"lock", LockTopic("r"),
			func(s *Store) { s.SetLock("r", &state.LockHolder{Command: "a"}) },
			func(s *Store) { s.SetLock("r", &state.LockHolder{Command: "b"}) },
			func(s *Store) { s.SetLock("r", nil) },
			func(sn Snapshot) any { return sn.Locks["r"] }},
		{"monitoring", MonitoringTopic("r"),
			func(s *Store) { s.SetMonitoring("r", &cluster.Monitoring{Realm: "r"}) },
			func(s *Store) {
				s.SetMonitoring("r", &cluster.Monitoring{Realm: "r", Clusters: []cluster.MonitoredCluster{{Name: "c"}}})
			},
			func(s *Store) { s.SetMonitoring("r", nil) },
			func(sn Snapshot) any { return sn.Monitorings["r"] }},
		{"doctor", TopicDoctor,
			func(s *Store) { s.SetDoctor(&DoctorReport{Checks: []DoctorCheck{{Name: "x"}}}) },
			func(s *Store) { s.SetDoctor(&DoctorReport{Running: true}) },
			func(s *Store) { s.SetDoctor(nil) },
			func(sn Snapshot) any { return sn.Doctor }},
		{"docker", TopicDocker,
			func(s *Store) { s.SetDocker(DockerState{Checked: true}) },
			func(s *Store) { s.SetDocker(DockerState{Checked: true, Err: "down"}) },
			func(s *Store) { s.SetDocker(DockerState{}) },
			func(sn Snapshot) any { return sn.Docker }},
	}
}

func TestSettersNotifyOnlyOnChange(t *testing.T) {
	for _, tc := range setters() {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			sub := s.Notifier().Subscribe()
			defer sub.Close()

			tc.empty(s)
			assert.Empty(t, sub.Take(), "removing what is not there")

			tc.a(s)
			assert.Equal(t, []Topic{tc.topic}, sub.Take())
			first := tc.view(s.Snapshot())

			tc.a(s)
			assert.Empty(t, sub.Take(), "an equal value")

			tc.b(s)
			assert.Equal(t, []Topic{tc.topic}, sub.Take())
			assert.NotEqual(t, first, tc.view(s.Snapshot()))
			assert.Equal(t, first, tc.view(snapshotAfter(tc.a)), "same input, same value")

			tc.empty(s)
			assert.Equal(t, []Topic{tc.topic}, sub.Take())
			tc.empty(s)
			assert.Empty(t, sub.Take())
		})
	}
}

func snapshotAfter(fn func(*Store)) Snapshot {
	s := New()
	fn(s)
	return s.Snapshot()
}

func TestPublishedSnapshotNeverChanges(t *testing.T) {
	for _, tc := range setters() {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			tc.a(s)
			held := s.Snapshot()
			want := tc.view(held)
			tc.b(s)
			assert.Equal(t, want, tc.view(held))
			tc.empty(s)
			assert.Equal(t, want, tc.view(held))
		})
	}

	t.Run("maps of other keys", func(t *testing.T) {
		s := New()
		s.SetClusters("a", []cluster.Summary{{Name: "x"}})
		held := s.Snapshot()
		s.SetClusters("b", []cluster.Summary{{Name: "y"}})
		s.SetClusters("a", nil)
		assert.Len(t, held.Clusters, 1)
		assert.Equal(t, "x", held.Clusters["a"][0].Name)
		assert.Len(t, s.Snapshot().Clusters, 1)
	})

	t.Run("input slices", func(t *testing.T) {
		s := New()
		ext := []string{"b", "a"}
		recs := []mesh.DNSRecord{{Hostname: "h"}}
		s.SetMesh("r", Mesh{External: ext, Records: recs})
		ext[0], recs[0].Hostname = "z", "z"
		m, ok := s.Snapshot().Meshes["r"]
		require.True(t, ok)
		assert.Equal(t, []string{"a", "b"}, m.External)
		assert.Equal(t, "h", m.Records[0].Hostname)

		checks := []DoctorCheck{{Name: "x"}}
		s.SetDoctor(&DoctorReport{Checks: checks})
		checks[0].Name = "z"
		assert.Equal(t, "x", s.Snapshot().Doctor.Checks[0].Name)

		cs := []cluster.Summary{{Name: "x"}}
		s.SetClusters("r", cs)
		cs[0].Name = "z"
		assert.Equal(t, "x", s.Snapshot().Clusters["r"][0].Name)
	})
}

func TestSetRealmsEmptyIsEqual(t *testing.T) {
	s := New()
	sub := s.Notifier().Subscribe()
	defer sub.Close()
	s.SetRealms([]cluster.RealmSummary{})
	assert.Empty(t, sub.Take())
}

func TestKeepStatuses(t *testing.T) {
	s := New()
	s.SetStatus("r", "a", &cluster.Status{Name: "a"})
	s.SetStatus("r", "b", &cluster.Status{Name: "b"})
	s.SetStatus("q", "a", &cluster.Status{Name: "qa"})
	sub := s.Notifier().Subscribe()
	defer sub.Close()
	held := s.Snapshot()

	s.KeepStatuses(StatusKey{"r", "a"}, StatusKey{"q", "a"})
	assert.Empty(t, sub.Take(), "dropping notifies nothing")
	sn := s.Snapshot()
	assert.NotNil(t, sn.Status("r", "a"))
	assert.NotNil(t, sn.Status("q", "a"))
	assert.Nil(t, sn.Status("r", "b"))
	assert.NotNil(t, held.Status("r", "b"), "a published snapshot never changes")

	s.KeepStatuses(StatusKey{"r", "a"}, StatusKey{"q", "a"}, StatusKey{"x", "y"})
	assert.Len(t, s.Snapshot().Statuses, 2)

	s.KeepStatuses()
	assert.Empty(t, s.Snapshot().Statuses)
}

func TestSetSlurmAndKeepStatuses(t *testing.T) {
	s := New()
	sub := s.Notifier().Subscribe()
	defer sub.Close()
	s.SetSlurm("r", "a", &Slurm{Info: &cluster.SlurmInfo{}})
	assert.Equal(t, []Topic{StatusTopic("r", "a")}, sub.Take(), "what Slurm says is part of the status")
	s.SetSlurm("r", "b", &Slurm{Err: "sinfo on the controller: boom"})
	assert.Equal(t, "sinfo on the controller: boom", s.Snapshot().Slurm("r", "b").Err)
	assert.Nil(t, s.Snapshot().Slurm("r", "c"))

	s.KeepStatuses(StatusKey{"r", "a"})
	assert.NotNil(t, s.Snapshot().Slurm("r", "a"))
	assert.Nil(t, s.Snapshot().Slurm("r", "b"))
	s.SetSlurm("r", "a", nil)
	assert.Empty(t, s.Snapshot().Slurms)
}

func TestSortingAndReaders(t *testing.T) {
	s := New()
	s.SetRealms([]cluster.RealmSummary{{Name: "sind-10"}, {Name: "sind-2"}, {Name: "ci"}})
	s.SetClusters("r", []cluster.Summary{{Name: "dev10"}, {Name: "dev2"}, {Name: "a"}})
	s.SetNodes("r", []cluster.NodeSummary{
		{Cluster: "dev", Container: "r-dev-worker-10", Role: config.RoleWorker},
		{Cluster: "dev", Container: "r-dev-worker-2", Role: config.RoleWorker},
		{Cluster: "dev", Container: "r-dev-x", Role: "other"},
		{Cluster: "dev", Container: "r-dev-y", Role: "another"},
		{Cluster: "dev", Container: "r-dev-sub", Role: config.RoleSubmitter},
		{Cluster: "dev", Container: "r-dev-controller", Role: config.RoleController},
		{Cluster: "a", Container: "r-a-controller", Role: config.RoleController},
	})
	s.SetNetworks("r", []cluster.NetworkSummary{{Name: "n10"}, {Name: "n2"}})
	s.SetVolumes("r", []cluster.VolumeSummary{{Name: "v10"}, {Name: "v2"}})
	sn := s.Snapshot()

	var names []string
	for _, r := range sn.Realms {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"ci", "sind-2", "sind-10"}, names)

	r, ok := sn.Realm("sind-2")
	require.True(t, ok)
	assert.Equal(t, "sind-2", r.Name)
	_, ok = sn.Realm("nope")
	assert.False(t, ok)

	assert.Equal(t, "a", sn.Clusters["r"][0].Name)
	assert.Equal(t, "dev2", sn.Clusters["r"][1].Name)
	c, ok := sn.Cluster("r", "dev10")
	require.True(t, ok)
	assert.Equal(t, "dev10", c.Name)
	_, ok = sn.Cluster("r", "nope")
	assert.False(t, ok)
	_, ok = sn.Cluster("nope", "a")
	assert.False(t, ok)

	var got []string
	for _, n := range sn.ClusterNodes("r", "dev") {
		got = append(got, n.Container)
	}
	assert.Equal(t, []string{"r-dev-controller", "r-dev-sub", "r-dev-worker-2", "r-dev-worker-10", "r-dev-y", "r-dev-x"}, got)
	assert.Len(t, sn.Nodes["r"], 7)
	assert.Equal(t, "r-a-controller", sn.Nodes["r"][0].Container)
	assert.Empty(t, sn.ClusterNodes("r", "nope"))
	assert.Empty(t, sn.ClusterNodes("nope", "dev"))

	assert.Equal(t, "n2", sn.Networks["r"][0].Name)
	assert.Equal(t, "v2", sn.Volumes["r"][0].Name)

	_, ok = sn.Meshes["r"]
	assert.False(t, ok)
	assert.Nil(t, sn.Status("r", "a"))
	assert.Nil(t, sn.Locks["r"])
	assert.Nil(t, sn.Monitorings["r"])
}

func TestDoctorTimeEqual(t *testing.T) {
	s := New()
	sub := s.Notifier().Subscribe()
	defer sub.Close()
	now := time.Now()
	s.SetDoctor(&DoctorReport{Time: now})
	assert.Equal(t, []Topic{TopicDoctor}, sub.Take())
	s.SetDoctor(&DoctorReport{Time: now})
	assert.Empty(t, sub.Take())
}
