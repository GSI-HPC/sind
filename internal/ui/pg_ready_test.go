// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readyNode returns a managed node of sind/dev that runs, with the
// services up.
func readyNode(short string, role config.Role, services ...string) pgNode {
	n := pgNode{Realm: "sind", Cluster: "dev", Short: short, Role: role, State: cluster.StateRunning, Managed: true, Known: true}
	for _, s := range services {
		n.Services = append(n.Services, pgSvc{s, true})
	}
	return n
}

// readyView returns the view of a cluster whose status is in, with nodes.
func readyView(nodes ...pgNode) *pgClusterView {
	return &pgClusterView{Realm: "sind", Name: "dev", Status: &cluster.Status{}, Slurm: &pgSlurmView{}, Cluster: pgCluster{Nodes: nodes}}
}

// down marks the service of the node down.
func down(n pgNode, svc string) pgNode {
	n.Services = append([]pgSvc(nil), n.Services...)
	for i := range n.Services {
		if n.Services[i].Name == svc {
			n.Services[i].OK = false
		}
	}
	return n
}

func TestPgReadyMarksAndWords(t *testing.T) {
	for state, want := range map[string][2]string{
		"ok": {"✓", "passed"}, "warn": {"!", "warning"}, "bad": {"✗", "failed"}, "none": {"–", "does not apply"},
	} {
		c := pgReadyCheck{State: state}
		assert.Equal(t, want[0], c.Mark(), state)
		assert.Equal(t, want[1], c.Word(), state)
	}
}

func TestPgReadyNotChecked(t *testing.T) {
	d := readyView(readyNode("submitter", config.RoleSubmitter))
	d.Status = nil
	r := pgReadyOf(d)
	assert.False(t, r.Checked)
	assert.Equal(t, "not checked yet", r.Verdict)
	assert.Empty(t, r.Checks)
	assert.Equal(t, []pgCmd{
		{"Open a shell on the submitter", "sind enter dev"},
		{"In that shell, submit a test job and show the queue", "sbatch --chdir=/data --wrap hostname && squeue"},
	}, r.Commands)
	assert.Contains(t, r.Note, "every node sees it")
}

func TestPgReadyAllGood(t *testing.T) {
	ctl := readyNode("controller", config.RoleController, "munge", "slurmctld")
	w := readyNode("worker-0", config.RoleWorker, "munge", "slurmd")
	d := readyView(ctl, w)
	d.Data = &cluster.MountPoint{Path: "/data", Source: "/home/u/sind/dev", Type: config.StorageHostPath, OK: true}
	r := pgReadyOf(d)
	assert.Equal(t, "yes", r.Verdict)
	assert.Equal(t, "ok", r.Class)
	require.Len(t, r.Checks, 4)
	assert.Equal(t, pgReadyCheck{State: "ok", Title: "The controller answers", Detail: "slurmctld runs on controller."}, r.Checks[0])
	assert.Equal(t, pgReadyCheck{State: "ok", Title: "The worker takes jobs"}, r.Checks[1])
	assert.Equal(t, pgReadyCheck{State: "none", Title: "No accounting", Detail: "The cluster has no db node, so sacct has no records."}, r.Checks[2])
	assert.Equal(t, pgReadyCheck{State: "ok", Title: "Your files", Detail: "/home/u/sind/dev on this host is /data on every node."}, r.Checks[3])
	assert.Contains(t, r.Note, "it is /home/u/sind/dev on this host")

	// A data mount at another path (storage.dataStorage.mountPath).
	d.Data = &cluster.MountPoint{Path: "/work", Source: "sind-dev-data", Type: config.StorageVolume, OK: true, Data: true}
	r = pgReadyOf(d)
	assert.Equal(t, "sbatch --chdir=/work --wrap hostname && squeue", r.Commands[1].Text)
	assert.Equal(t, "Put job scripts in /work: every node sees it, and the output of the test job lands there.", r.Note)
	assert.Equal(t, "The volume sind-dev-data is /work on every node.", r.Checks[3].Detail)

	// Two workers, a volume as data, and accounting.
	db := readyNode("db", config.RoleDB, "mariadb", "slurmdbd")
	d = readyView(ctl, w, readyNode("worker-1", config.RoleWorker, "slurmd"), db)
	d.Data = &cluster.MountPoint{Path: "/data", Source: "sind-dev-data", Type: config.StorageVolume, OK: true}
	r = pgReadyOf(d)
	assert.Equal(t, "All 2 workers take jobs", r.Checks[1].Title)
	assert.Equal(t, "Accounting works", r.Checks[2].Title)
	assert.Equal(t, "The volume sind-dev-data is /data on every node.", r.Checks[3].Detail)
	assert.Equal(t, "yes", r.Verdict)

	// The data volume is gone, accounting is down: yes, but.
	d.Data.OK = false
	d.Cluster.Nodes[3] = down(down(db, "slurmdbd"), "mariadb")
	r = pgReadyOf(d)
	assert.Equal(t, "yes, but see below", r.Verdict)
	assert.Equal(t, "warn", r.Class)
	assert.Equal(t, "Accounting is down", r.Checks[2].Title)
	assert.Equal(t, "db: mariadb is down, slurmdbd is down. Jobs run, but sacct does not record them.", r.Checks[2].Detail)
	assert.Equal(t, "The data volume is missing", r.Checks[3].Title)

	// A db node that sind does not manage.
	db.Managed = false
	d.Cluster.Nodes[3] = db
	assert.Equal(t, "none", pgReadyOf(d).Checks[2].State)
}

func TestPgReadyWorkers(t *testing.T) {
	ctl := readyNode("controller", config.RoleController, "slurmctld")
	ok := readyNode("worker-0", config.RoleWorker, "slurmd")
	svcDown := down(readyNode("worker-1", config.RoleWorker, "slurmd"), "slurmd")
	drained := readyNode("worker-2", config.RoleWorker, "slurmd")
	drained.Slurm = &slurm.NodeInfo{State: "drained", Reason: "disk check"}
	notResp := readyNode("worker-3", config.RoleWorker, "slurmd")
	notResp.Slurm = &slurm.NodeInfo{State: "down*"}
	unmanaged := readyNode("worker-4", config.RoleWorker)
	unmanaged.Managed = false

	r := pgReadyOf(readyView(ctl, ok, svcDown, drained, notResp, unmanaged))
	assert.Equal(t, "yes, with 1 of 4 workers", r.Verdict)
	assert.Equal(t, "warn", r.Class)
	c := r.Checks[1]
	assert.Equal(t, "1 of 4 workers take jobs", c.Title)
	assert.Equal(t, "worker-1: slurmd is down; worker-2: drained in Slurm (disk check); worker-3: down* in Slurm.", c.Detail)
	// The logs of the first, and a fix for each: Resume for a worker that
	// Slurm sends no jobs.
	var labels []string
	for _, a := range c.Actions {
		labels = append(labels, a.Label)
	}
	assert.Equal(t, []string{"Logs of worker-1", "Reboot worker-1…", "Resume worker-2…", "Resume worker-3…"}, labels)
	assert.Equal(t, "/clusters/sind/dev?node=worker-1&tab=logs", c.Actions[0].URL)
	assert.Contains(t, c.Actions[2].URL, "op=resume")

	// None takes jobs: no.
	stopped := readyNode("worker-5", config.RoleWorker, "slurmd")
	stopped.State = cluster.StateStopped
	r = pgReadyOf(readyView(ctl, stopped, drained))
	assert.Equal(t, "no", r.Verdict)
	assert.Equal(t, pgReadyCheck{State: "bad", Title: "No worker takes jobs", Detail: "worker-5: stopped; worker-2: drained in Slurm (disk check).", Actions: r.Checks[1].Actions}, r.Checks[1])
	assert.Equal(t, "Start worker-5…", r.Checks[1].Actions[1].Label)
	paused := stopped
	paused.State = cluster.StatePaused
	assert.Equal(t, "Unfreeze worker-5…", pgReadyOf(readyView(ctl, paused)).Checks[1].Actions[1].Label)
	r = pgReadyOf(readyView(ctl, drained))
	assert.Equal(t, "Resume worker-2…", r.Checks[1].Actions[1].Label)
	// A fix for the first three only.
	r = pgReadyOf(readyView(ctl, ok, svcDown, drained, notResp, stopped))
	assert.Len(t, r.Checks[1].Actions, 4)
	// A node that needs no look has no fix.
	_, has := pgPowerFix(ok)
	assert.False(t, has)

	// No worker at all.
	r = pgReadyOf(readyView(ctl))
	assert.Equal(t, "no", r.Verdict)
	assert.Equal(t, "No worker", r.Checks[1].Title)
	assert.Equal(t, "/dialogs/create-worker?cluster=dev&realm=sind", r.Checks[1].Actions[0].URL)
}

func TestPgReadyController(t *testing.T) {
	w := readyNode("worker-0", config.RoleWorker, "slurmd")
	ctl := readyNode("controller", config.RoleController, "slurmctld")
	backup := readyNode("controller-backup", config.RoleController, "slurmctld")

	// No controller.
	r := pgReadyOf(readyView(w))
	assert.Equal(t, "No controller", r.Checks[0].Title)
	assert.Equal(t, "no", r.Verdict)

	// An unmanaged one.
	um := ctl
	um.Managed = false
	assert.Equal(t, "none", pgReadyOf(readyView(um, w)).Checks[0].State)

	// The backup is down: no failover.
	c := pgReadyOf(readyView(ctl, down(backup, "slurmctld"), w)).Checks[0]
	assert.Equal(t, "warn", c.State)
	assert.Equal(t, "slurmctld runs on controller. controller-backup: slurmctld is down: no failover.", c.Detail)
	assert.Equal(t, "Logs of controller-backup", c.Actions[0].Label)

	// Both down.
	stopped := ctl
	stopped.State = cluster.StateStopped
	r = pgReadyOf(readyView(stopped, down(backup, "slurmctld"), w))
	assert.Equal(t, "no", r.Verdict)
	assert.Equal(t, pgReadyCheck{State: "bad", Title: "The controller does not answer", Detail: "controller: stopped; controller-backup: slurmctld is down.", Actions: r.Checks[0].Actions}, r.Checks[0])
	assert.Equal(t, "Start controller…", r.Checks[0].Actions[1].Label)

	// slurmctld runs, but sinfo fails.
	d := readyView(ctl, w)
	d.Slurm.Err = "slurm_load_node: Unable to contact slurm controller"
	c = pgReadyOf(d).Checks[0]
	assert.Equal(t, "bad", c.State)
	assert.Equal(t, "sinfo fails: slurm_load_node: Unable to contact slurm controller", c.Detail)
	assert.Len(t, c.Actions, 1, "slurmctld is up: no power fix to offer")
}

func TestPgReadyWhileTheClusterIsCreated(t *testing.T) {
	ctl := down(readyNode("controller", config.RoleController, "slurmctld"), "slurmctld")
	w := readyNode("worker-0", config.RoleWorker, "slurmd")
	w.State = cluster.StateStopped
	ctl.Creating, w.Creating = true, true
	r := pgReadyOf(readyView(ctl, w))
	assert.Equal(t, "not yet: the cluster is being created", r.Verdict)
	assert.Equal(t, "run", r.Class)
	assert.Equal(t, "bad", r.Checks[0].State, "the checks say what is not ready")
	for _, c := range r.Checks {
		assert.Empty(t, c.Actions, "no fix while the command runs")
	}

	// Once all is up, the verdict is yes, as it is.
	ctl = readyNode("controller", config.RoleController, "slurmctld")
	w = readyNode("worker-0", config.RoleWorker, "slurmd")
	ctl.Creating, w.Creating = true, true
	assert.Equal(t, "yes", pgReadyOf(readyView(ctl, w)).Verdict)
}
