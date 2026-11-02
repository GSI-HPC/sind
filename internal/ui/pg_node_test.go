// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixWorker returns worker-4 of sind/dev, managed and probed, that runs
// with slurmd and munge up.
func fixWorker() pgNode {
	return pgNode{Realm: "sind", Cluster: "dev", Short: "worker-4", Arg: "worker-4.dev", Role: config.RoleWorker,
		State: cluster.StateRunning, Known: true, Managed: true, Services: []pgSvc{{"munge", true}, {"slurmd", true}}}
}

// fixFailed returns worker-4 with slurmd failed at 09:02:17, as sind ui
// read it.
func fixFailed(at time.Time) pgNode {
	n := fixWorker()
	n.Services[1].OK = false
	n.Fault = &store.Fault{Units: []store.Unit{{Name: "slurmd", State: "failed", Result: "exit-code", Status: 1, Since: at, Journal: []store.JournalLine{
		{At: at.Add(-2 * time.Second), Text: "slurmd[212]: slurmd version 26.05.4 started"},
		{At: at.Add(-time.Second), Text: "slurmd[212]: error: Unable to register with slurm controller"},
		{At: at, Text: "systemd[1]: slurmd.service: Warning: unit restarts too fast"},
	}}}}
	return n
}

func TestPgLadder(t *testing.T) {
	// A node that needs no look has no fix.
	n := fixWorker()
	assert.Empty(t, pgLadder(n))
	assert.Nil(t, pgFix(n, "sm"))
	assert.Nil(t, n.RowFix())

	// A service down: restart it, then reboot.
	n.Services[1].OK = false
	l := pgLadder(n)
	require.Len(t, l, 2)
	assert.Equal(t, []string{"Restart slurmd", "Reboot worker-4"}, []string{l[0].Title, l[1].Title})
	assert.Equal(t, "sind ssh worker-4.dev -- systemctl restart slurmd", l[0].Cmd)
	assert.Equal(t, "sind power reboot worker-4.dev", l[1].Cmd)
	assert.Equal(t, []int{1, 2}, []int{l[0].N, l[1].N})
	assert.True(t, l[0].First)
	assert.False(t, l[1].First)
	assert.Equal(t, "primary", l[0].Button().Class, "the first fix is the primary button of the page")
	assert.Empty(t, l[1].Button().Class)
	assert.Equal(t, "Reboot…", l[1].Act.Label)
	assert.Equal(t, "Reboot worker-4.dev…", l[1].Act.Name)
	fix := n.RowFix()
	assert.Equal(t, "sm", fix.Class)
	assert.Equal(t, "Restart slurmd…", fix.Label)
	u, err := url.Parse(fix.URL)
	require.NoError(t, err)
	assert.Equal(t, "/dialogs/restart-service", u.Path)
	assert.Equal(t, url.Values{"realm": {"sind"}, "cluster": {"dev"}, "nodes": {"worker-4"}, "unit": {"slurmd"}}, u.Query())

	// munge alone down: its restart.
	n = fixWorker()
	n.Services[0].OK = false
	assert.Equal(t, "Restart munge", pgLadder(n)[0].Title)
	assert.Equal(t, "munge", n.FailedUnit())
	// Both down: the Slurm service tells the most, but it needs munge, so
	// munge restarts first.
	n.Services[1].OK = false
	assert.Equal(t, "slurmd", n.FailedUnit())
	l = pgLadder(n)
	require.Len(t, l, 3)
	assert.Equal(t, []string{"Restart munge", "Restart slurmd", "Reboot worker-4"}, []string{l[0].Title, l[1].Title, l[2].Title})
	assert.Equal(t, "slurmd needs munge, so munge comes first. The other processes of worker-4 go on.", l[0].Text)
	assert.Equal(t, "Restarts slurmd once munge runs.", l[1].Text)
	assert.True(t, n.CanRestart())

	// sshd down: sind ssh cannot log in, so a reboot is the only fix.
	n = fixWorker()
	n.Services = append(n.Services, pgSvc{"sshd", false})
	assert.False(t, n.CanRestart())
	l = pgLadder(n)
	require.Len(t, l, 1)
	assert.Equal(t, "Reboot worker-4", l[0].Title)
	assert.Contains(t, l[0].Text, "sind ssh needs sshd")
	n.State = cluster.StateStopped
	assert.False(t, n.CanRestart(), "a node that does not run")

	// Paused, stopped, and a state that sind does not know.
	n = fixWorker()
	n.State = cluster.StatePaused
	assert.Equal(t, "sind power unfreeze worker-4.dev", pgLadder(n)[0].Cmd)
	n.State = cluster.StateStopped
	assert.Equal(t, "Start worker-4", pgLadder(n)[0].Title)
	n.State = cluster.StateUnknown
	assert.Empty(t, pgLadder(n))

	// A worker that Slurm sends no jobs: resume it.
	n = fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "drained"}
	assert.Equal(t, "sind exec dev -- scontrol update NodeName=worker-4 State=RESUME", pgLadder(n)[0].Cmd)
	n.Cluster = config.DefaultClusterName
	assert.Equal(t, "sind exec -- scontrol update NodeName=worker-4 State=RESUME", pgLadder(n)[0].Cmd)
}

func TestPgNodeLogs(t *testing.T) {
	n := fixWorker()
	assert.Equal(t, "slurmd", n.LogUnit(), "the journal of its Slurm service")
	assert.Equal(t, "Logs of slurmd", n.LogsLabel())
	assert.Equal(t, "Logs of slurmd on worker-4.dev", n.LogsName())
	assert.Equal(t, "/nodes/sind/dev/worker-4?source=journal&tab=logs&unit=slurmd", n.LogsLink())
	a := n.LogsAction("Logs of worker-4")
	assert.Equal(t, pgAction{Label: "Logs of worker-4", URL: n.LogsLink(), Class: "sm ghost", Name: "Logs of slurmd on worker-4.dev"}, a)
	n.Services[0].OK = false
	assert.Equal(t, "munge", n.LogUnit(), "the service that failed")
	// A node whose status lists other services: the output of its container.
	n.Services = []pgSvc{{"sshd", true}}
	assert.Empty(t, n.LogUnit())
	assert.Equal(t, "Logs", n.LogsLabel())
	assert.Equal(t, "Logs of worker-4.dev", n.LogsName())
	assert.Equal(t, "/nodes/sind/dev/worker-4?tab=logs", n.LogsLink())
	// A node that does not run: its container output, which Docker keeps.
	n = fixWorker()
	n.State = cluster.StateStopped
	assert.Empty(t, n.LogUnit())
}

func TestPgNodeFaults(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 2, 17, 0, time.Local)
	n := fixWorker()
	_, ok := n.Unit("slurmd")
	assert.False(t, ok, "sind ui read nothing")
	assert.Nil(t, n.Exit())
	assert.Empty(t, n.Cause())

	n = fixFailed(at)
	u, ok := n.Unit("slurmd")
	require.True(t, ok)
	assert.Equal(t, 1, u.Status)
	assert.Equal(t, "since 09:02", n.Cause())
	assert.Nil(t, n.Exit(), "it runs")

	// A stopped one: how its container stopped.
	n = fixWorker()
	n.State = cluster.StateStopped
	n.Fault = &store.Fault{Exit: &store.Exit{Code: 137, OOMKilled: true, At: at}}
	assert.Equal(t, "exit code 137, out of memory", n.Cause())
	// Slurm's reason.
	n = fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "drained", Reason: "memory errors"}
	assert.Equal(t, "memory errors", n.Cause())

	assert.Equal(t, "exit code 1", pgExitShort(store.Exit{Code: 1}))
	for e, want := range map[store.Exit]string{
		{Code: 137, OOMKilled: true}: "the kernel killed it, as it ran out of memory",
		{Code: 0}:                    "its main process ended, as after a shutdown",
		{Code: 137}:                  "it was killed (SIGKILL), as by Cut power or docker kill",
		{Code: 143}:                  "it was stopped (SIGTERM), as by docker stop",
		{Code: 1}:                    "its main process failed",
	} {
		assert.Equal(t, want, pgExitText(e))
	}
	assert.Equal(t, "the Slurm daemon of a worker: it starts and watches the jobs", pgUnitRole("slurmd"))
	assert.Equal(t, "a service of the node", pgUnitRole("chronyd"))
	assert.Equal(t, "09:02", pgHM(at))
	assert.Equal(t, "09:02:17", pgHMS(at))
}

func TestPgSlurmActions(t *testing.T) {
	n := fixWorker()
	assert.Nil(t, n.SlurmActions(), "sinfo did not list it")
	n.Slurm = &slurm.NodeInfo{State: "idle"}
	acts := n.SlurmActions()
	require.Len(t, acts, 1)
	assert.Equal(t, "Drain…", acts[0].Label)
	assert.Equal(t, "Drain worker-4.dev…", acts[0].Name)
	assert.Equal(t, "sm ghost", acts[0].Class)

	// Drained: its fix alone, as its journal does not say why.
	n.Slurm.State = "drained"
	acts = n.SlurmActions()
	require.Len(t, acts, 1)
	assert.Equal(t, "Resume…", acts[0].Label)
	assert.Nil(t, n.RowLogs())
	// Down: its log first.
	n.Slurm.State = "down*"
	acts = n.SlurmActions()
	require.Len(t, acts, 2)
	assert.Equal(t, "Logs of slurmd", acts[0].Label)
	assert.Equal(t, "Resume…", acts[1].Label)

	n.Creating = true
	assert.Nil(t, n.SlurmActions(), "a command creates it")
}

func TestPgNodeMenus(t *testing.T) {
	labels := func(m pgMenu) (on, off []string) {
		for _, g := range m.Groups {
			for _, it := range g.Items {
				if it.Off != "" {
					off = append(off, it.Label+": "+it.Hint)
				} else {
					on = append(on, it.Label)
				}
			}
		}
		return on, off
	}
	n := fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "idle"}
	menus := pgNodeMenus(n)
	require.Len(t, menus, 3)
	assert.Equal(t, []string{"slurm", "power", "more"}, []string{menus[0].ID, menus[1].ID, menus[2].ID})
	on, off := labels(menus[0])
	assert.Equal(t, []string{"Drain…"}, on)
	assert.Equal(t, []string{"Resume: It takes jobs already"}, off)
	on, off = labels(menus[1])
	assert.Equal(t, []string{"Reboot…", "Shut down…", "Freeze…", "Power-cycle…", "Cut power…"}, on)
	assert.Equal(t, []string{"Start: It runs already", "Unfreeze: It is not frozen"}, off)
	assert.True(t, menus[1].Groups[1].Danger)
	assert.Equal(t, "⋯", menus[2].Icon)
	assert.Contains(t, menus[2].Groups[0].Items[0].URL, "/dialogs/delete-worker?")

	n.Slurm.State = "drained*"
	_, off = labels(pgNodeMenus(n)[0])
	assert.Equal(t, []string{"Drain: It is drained already"}, off)
	n.Slurm.State = "down*"
	on, _ = labels(pgNodeMenus(n)[0])
	assert.Equal(t, []string{"Drain…", "Resume…"}, on)
	n.Managed = false
	_, off = labels(pgNodeMenus(n)[0])
	assert.Equal(t, []string{"Drain: sind runs no Slurm on it", "Resume: sind runs no Slurm on it"}, off)

	n = fixWorker()
	n.State = cluster.StatePaused
	on, _ = labels(pgNodeMenus(n)[1])
	assert.Equal(t, []string{"Unfreeze…"}, on)
	_, off = labels(pgNodeMenus(n)[0])
	assert.Equal(t, []string{"Drain: It is frozen: unfreeze it first", "Resume: It is frozen: unfreeze it first"}, off)
	// Slurm cannot drain or resume a node whose container does not run.
	n.State = cluster.StateStopped
	_, off = labels(pgNodeMenus(n)[0])
	assert.Equal(t, []string{"Drain: It does not run", "Resume: It does not run"}, off)

	// A controller has Power alone.
	n.Role = config.RoleController
	assert.Len(t, pgNodeMenus(n), 1)
}

// pgOpOff is the one rule of which power ops fit a node, for the menus, the
// Power dialog and the selection bar.
func TestPgOpOff(t *testing.T) {
	n := fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "idle"}
	off := func(ops ...string) []string {
		var out []string
		for _, op := range ops {
			out = append(out, op+": "+pgOpOff(n, op))
		}
		return out
	}
	all := []string{"drain", "resume", "on", "shutdown", "reboot", "freeze", "unfreeze", "cycle", "cut"}
	assert.Equal(t, []string{"drain: ", "resume: It takes jobs already", "on: It runs already", "shutdown: ", "reboot: ", "freeze: ", "unfreeze: It is not frozen", "cycle: ", "cut: "}, off(all...))
	n.Creating = true
	for _, o := range off(all...) {
		assert.Contains(t, o, ": sind creates it now")
	}
	n.Creating, n.Deleting = false, true
	assert.Equal(t, []string{"on: sind deletes it now"}, off("on"))
	n.Deleting = false
	n.State = cluster.StatePaused
	assert.Equal(t, []string{"on: It is frozen: unfreeze it first", "reboot: It is frozen: unfreeze it first", "unfreeze: "}, off("on", "reboot", "unfreeze"))
	n.State = cluster.StateStopped
	assert.Equal(t, []string{"on: ", "cut: It does not run"}, off("on", "cut"))
	n.Role = config.RoleController
	assert.Equal(t, []string{"drain: Only workers take jobs"}, off("drain"))
	n = fixWorker()
	assert.Equal(t, []string{"drain: sinfo does not list it"}, off("drain"))
}

func TestPgNodeOps(t *testing.T) {
	n := fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "idle"}
	assert.Equal(t, "drain shutdown reboot freeze cycle cut delete", n.Ops())
	n.Creating = true
	assert.Empty(t, n.Ops(), "sind creates it now")
	n.Creating = false
	n.Role = config.RoleController
	assert.Equal(t, "shutdown reboot freeze cycle cut", n.Ops())
}

func TestPgNodeActs(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	end := now.Add(-time.Minute)
	n := fixWorker()
	sn := store.Snapshot{
		Runs: []store.Run{
			{ID: "r1", Title: "power reboot worker-[3-4].dev", Start: now.Add(-3 * time.Minute), End: &end, Status: store.StatusOK},
			{ID: "r2", Title: "power", Start: now},
			{ID: "r3", Title: "power reboot worker-5.dev", Start: now},
			{ID: "r4", Title: "power reboot [x worker-4.dev", Start: now.Add(-time.Second)},
			{ID: "r5", Title: "power stop worker-4.dev", Trace: "t-other", Start: now},
			{ID: "r6", Title: "power shutdown worker-4.dev", Trace: "t-run", Start: now.Add(-2 * time.Minute)},
		},
		Tasks: []store.Task{
			{ID: "t1", Action: "restart-service", Realm: "sind", Cluster: "dev", Form: map[string][]string{"nodes": {"worker-4"}, "unit": {"slurmd"}},
				Start: now.Add(-10 * time.Minute), End: &end, Status: store.TaskOK},
			{ID: "t2", Action: "restart-service", Realm: "sind", Cluster: "dev", Form: map[string][]string{"nodes": {"worker-4"}, "unit": {"munge"}},
				Start: now.Add(-20 * time.Minute), Status: store.TaskRunning},
			{ID: "t3", Action: "power", Realm: "sind", Cluster: "dev", Form: map[string][]string{"nodes": {"worker-4"}, "op": {"drain"}},
				Start: now.Add(-30 * time.Minute), End: &end, Status: store.TaskCanceled},
			{ID: "t4", Action: "power", Display: "sind power x worker-4.dev", Realm: "sind", Cluster: "dev", Form: map[string][]string{"nodes": {"worker-4"}, "op": {"x"}},
				Start: now.Add(-40 * time.Minute), End: &end, Status: store.TaskFailed},
			{ID: "t5", Action: "restart-service", Realm: "sind", Cluster: "dev", Form: map[string][]string{"nodes": {"worker-3"}}, Start: now},
			{ID: "t6", Action: "power", Realm: "ci", Cluster: "dev", Trace: "t-other", Form: map[string][]string{"nodes": {"worker-4"}}, Start: now},
			{ID: "t7", Action: "power", Realm: "sind", Cluster: "dev", Trace: "t-run", Form: map[string][]string{"nodes": {"worker-4"}, "op": {"stop"}}, Start: now},
		},
	}
	acts := pgNodeActs(sn, n, now)
	var got []string
	for _, a := range acts {
		got = append(got, a.Title+" · "+a.Text()+" · "+a.SourceText())
	}
	assert.Equal(t, []string{
		"power reboot [x worker-4.dev · runs · from a terminal",
		"power shutdown worker-4.dev · runs · from sind ui",
		"power reboot worker-[3-4].dev · ended ok · from a terminal",
		"Restart slurmd · ended ok · from sind ui",
		"Restart munge · runs · from sind ui",
		"Drain · was stopped · from sind ui",
		"power x worker-4.dev · failed · from sind ui",
	}, got, "the newest first; a run of another realm, a task with a run and a task of another node are left out")
	assert.Equal(t, "/tasks/t1", acts[3].Link)
	assert.Equal(t, "9m00s", acts[3].Took)
	assert.Equal(t, "11:50:00", acts[3].When)
	assert.Empty(t, acts[0].Took)

	assert.Equal(t, "Restart slurmd", pgTaskTitle(sn.Tasks[0]))
	assert.Equal(t, "Drain", pgTaskTitle(sn.Tasks[2]))
	assert.Equal(t, "power x worker-4.dev", pgTaskTitle(sn.Tasks[3]))
	assert.Equal(t, "connect kind", pgTaskTitle(store.Task{Action: "connect", Display: "sind connect kind"}))

	assert.True(t, pgNamesNode("power reboot worker-4", pgNode{Short: "worker-4", Arg: "worker-4", Cluster: "default"}))
	assert.True(t, pgNamesNode("power reboot worker-4.dev", pgNode{Short: "worker-4", Arg: "worker-4", Cluster: "dev"}))
	assert.False(t, pgNamesNode("power reboot", n))
}

func TestPgChangeRows(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	n := fixFailed(now.Add(-5 * time.Minute))
	n.Fault.Exit = &store.Exit{Code: 137, At: now.Add(-50 * time.Minute)}
	n.Fault.Units = append(n.Fault.Units, store.Unit{Name: "munge", State: "inactive"}, store.Unit{Name: "sshd", Since: now.Add(-40 * time.Minute)})
	key := store.NodeKey{Realm: "sind", Cluster: "dev", Node: "worker-4"}
	sn := store.Snapshot{NodeChanges: map[store.NodeKey][]store.Change{key: {
		{At: now.Add(-1 * time.Minute), Source: store.SourceSystemd, Subject: "sshd", State: "active", OK: true},
		{At: now.Add(-90 * time.Second), Source: store.SourceSystemd, Subject: "munge", State: "down"},
		{At: now.Add(-2 * time.Minute), Source: store.SourceSlurm, Subject: "slurm", State: "drained", Reason: "disk check"},
		{At: now.Add(-3 * time.Minute), Source: store.SourceSlurm, Subject: "slurm", State: "idle", OK: true},
		{At: now.Add(-4 * time.Minute), Source: store.SourceDocker, Subject: "container", State: "paused"},
		{At: now.Add(-6 * time.Minute), Source: store.SourceDocker, Subject: "container", State: "running", OK: true},
		{At: now.Add(-7 * time.Minute), Source: store.SourceSystemd, Subject: "sshd", State: "inactive"},
	}}}
	end := now.Add(-30 * time.Second)
	acts := []pgNodeAct{
		{Title: "Restart slurmd", Link: "/tasks/t1", Source: runFromUI, Class: "ok", Start: now.Add(-time.Minute), End: &end},
		{Title: "power reboot worker-4.dev", Link: "/runs/r1", Source: runFromTerminal, Class: "run", Start: now.Add(-8 * time.Minute), Running: true},
	}
	rows := pgChangeRows(sn, n, acts, now)
	var got []string
	for _, r := range rows {
		got = append(got, r.Class+" "+r.Pre+r.Strong+r.Post+" ("+r.Source+")")
	}
	assert.Equal(t, []string{
		"ok Restart slurmd ended ok (sind ui)",
		"ok sshd is active (systemd)",
		"bad munge inactive (systemd)",
		"warn Slurm: drained, disk check (sinfo)",
		"ok Slurm: idle (sinfo)",
		"warn Container: paused (docker)",
		"bad slurmd failed, exit status 1 (systemd)",
		"ok Container: running (docker)",
		"bad sshd went down (systemd)",
		"run power reboot worker-4.dev runs (terminal)",
	}, got, "what sind ui saw, read and ran, a unit in the word of its pill; the faults of what it saw change are not repeated")
	assert.Equal(t, "11:59:30", rows[0].When)
	assert.Equal(t, "/tasks/t1", rows[0].Link)

	// The exit of a container whose change sind ui did not see.
	sn.NodeChanges = nil
	rows = pgChangeRows(sn, n, nil, now)
	assert.Equal(t, "Container: stopped, exit code 137", rows[len(rows)-1].Pre+rows[len(rows)-1].Strong+rows[len(rows)-1].Post)
	assert.Equal(t, "sshd stopped", rows[len(rows)-2].Strong+rows[len(rows)-2].Post)

	// Ten at most.
	var many []store.Change
	for i := range 15 {
		many = append(many, store.Change{At: now.Add(-time.Duration(i) * time.Minute), Source: store.SourceSlurm, State: "idle", OK: true})
	}
	sn.NodeChanges = map[store.NodeKey][]store.Change{key: many}
	assert.Len(t, pgChangeRows(sn, fixWorker(), nil, now), pgChangesShown)
	assert.Equal(t, "other program", pgSourceWord(runFromProgram))
}

func TestPgSvcRows(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 2, 17, 0, time.Local)
	n := fixFailed(at)
	key := store.NodeKey{Realm: "sind", Cluster: "dev", Node: "worker-4"}
	sn := store.Snapshot{NodeChanges: map[store.NodeKey][]store.Change{key: {
		{At: at.Add(time.Minute), Source: store.SourceSystemd, Subject: "munge", State: "active", OK: true},
		{At: at.Add(-time.Hour), Source: store.SourceSystemd, Subject: "munge", State: "inactive"},
		{At: at, Source: store.SourceSlurm, Subject: "slurm", State: "idle"},
	}}}
	rows := pgSvcRows(sn, n)
	require.Len(t, rows, 2)
	assert.Equal(t, pgSvcRow{Name: "munge", Pill: pgPill{"active", "ok"}, Note: "since " + pgHMS(at.Add(time.Minute)), Logs: "/nodes/sind/dev/worker-4?source=journal&tab=logs&unit=munge"}, rows[0])
	assert.Equal(t, pgPill{"failed", "bad"}, rows[1].Pill)
	assert.Equal(t, "exit status 1 at 09:02:17", rows[1].Note)
	require.NotNil(t, rows[1].Restart)
	assert.Equal(t, "Restart…", rows[1].Restart.Label)
	assert.Equal(t, "Restart slurmd on worker-4.dev…", rows[1].Restart.Name)

	// A unit that ended without a status; one whose time is not known; one
	// that sind ui read nothing of.
	n.Fault.Units[0].Status = 0
	assert.Equal(t, "since 09:02:17", pgSvcRows(sn, n)[1].Note)
	n.Fault.Units[0].Since = time.Time{}
	n.Fault.Units[0].State = ""
	assert.Equal(t, pgPill{"down", "bad"}, pgSvcRows(sn, n)[1].Pill)
	assert.Empty(t, pgSvcRows(sn, n)[1].Note)
	n.Fault = nil
	assert.Empty(t, pgSvcRows(sn, n)[1].Note)
	// A command works on its cluster: no restart.
	n.Busy = true
	assert.Nil(t, pgSvcRows(sn, n)[1].Restart)
	// One that a command creates, and one that does not run.
	n.Busy, n.Creating = false, true
	assert.Equal(t, pgPill{n.ServiceWord(), "run"}, pgSvcRows(sn, n)[1].Pill)
	assert.Nil(t, pgSvcRows(sn, n)[1].Restart)
	n.Creating, n.State = false, cluster.StateStopped
	assert.Equal(t, pgSvcRow{Name: "slurmd", Pill: pgPill{"not running", ""}}, pgSvcRows(sn, n)[1])
}

func TestPgDiag(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 31, 17, 0, time.Local)
	at := now.Add(-29 * time.Minute)
	sn := store.Snapshot{Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev", State: cluster.StateRunning}}}}

	assert.Nil(t, pgDiagOf(sn, fixWorker(), nil, now), "nothing is wrong")
	n := fixFailed(at)
	n.Busy = true
	assert.Nil(t, pgDiagOf(sn, n, nil, now), "a command works on it")

	// slurmd failed: when, how, its journal, and the fixes in order.
	n.Busy = false
	end := now.Add(-time.Hour)
	acts := []pgNodeAct{{Title: "power reboot worker-4.dev", Running: true}, {Title: "Restart slurmd", Class: "bad", End: &end}}
	d := pgDiagOf(sn, n, acts, now)
	require.NotNil(t, d)
	assert.Equal(t, "bad", d.Sev)
	assert.Equal(t, "slurmd failed, so worker-4 takes no jobs", d.Title)
	assert.Equal(t, "since 09:02 · 29 min", d.Since)
	assert.Equal(t, "slurmd is the Slurm daemon of a worker: it starts and watches the jobs. It stopped with exit status 1 at 09:02:17. No job starts on it. The container runs; munge is active.", d.Text)
	assert.Equal(t, []pgFact{{"failed", "09:02:17, exit status 1", true}}, d.Facts)
	assert.Equal(t, "Restart slurmd", d.Last.Title, "the last command that ended")
	require.NotNil(t, d.Journal)
	assert.Equal(t, "/nodes/sind/dev/worker-4?source=journal&tab=logs&unit=slurmd", d.Journal.Link)
	assert.Equal(t, []string{"", "err", "warn"}, []string{d.Journal.Lines[0].Class, d.Journal.Lines[1].Class, d.Journal.Lines[2].Class})
	assert.Equal(t, "09:02:15 slurmd[212]: slurmd version 26.05.4 started", strings.Split(d.Journal.Text, "\n")[0])
	assert.Len(t, d.Ladder, 2)

	// Slurm marks it down, and munge is down too.
	n.Slurm = &slurm.NodeInfo{State: "down*", Reason: "Not responding"}
	n.Services = append(n.Services, pgSvc{"sshd", true})
	n.Services[0].OK = false
	d = pgDiagOf(sn, n, nil, now)
	assert.Equal(t, "slurmd and munge are down, so worker-4 takes no jobs", d.Title)
	assert.Contains(t, d.Text, "Slurm now marks worker-4 down* (Not responding), and no job starts on it.")
	assert.Contains(t, d.Text, "The container runs; sshd is active.")
	n.Slurm.Reason = ""
	n.Services[2].OK = false
	assert.NotContains(t, pgDiagOf(sn, n, nil, now).Text, "The container runs")
	assert.Contains(t, pgDiagOf(sn, n, nil, now).Text, "Slurm now marks worker-4 down*, and")
	// A unit that is inactive, read without a time or status.
	n = fixWorker()
	n.Services = append(n.Services, pgSvc{"sshd", true})
	n.Services[1].OK = false
	n.Fault = &store.Fault{Units: []store.Unit{{Name: "slurmd", State: "inactive"}}}
	d = pgDiagOf(sn, n, nil, now)
	assert.Equal(t, "slurmd is inactive, so worker-4 takes no jobs", d.Title)
	assert.Contains(t, d.Text, "It stopped. No job starts on it. The container runs; munge and sshd are active.")
	assert.Empty(t, d.Since)
	assert.Nil(t, d.Journal)

	// A controller: what it means for the cluster.
	ctl := pgNode{Realm: "sind", Cluster: "dev", Short: "controller", Arg: "controller.dev", Role: config.RoleController,
		State: cluster.StateRunning, Known: true, Managed: true, Services: []pgSvc{{"slurmctld", false}}}
	d = pgDiagOf(sn, ctl, nil, now)
	assert.Equal(t, "slurmctld failed", d.Title)
	assert.Contains(t, d.Text, "slurmctld is the Slurm controller: it schedules the jobs.")

	// Frozen, and a state that sind does not know.
	n = fixWorker()
	n.State = cluster.StatePaused
	assert.Equal(t, "warn", pgDiagOf(sn, n, nil, now).Sev)
	assert.Equal(t, "worker-4 is frozen, so it takes no jobs", pgDiagOf(sn, n, nil, now).Title)
	assert.Equal(t, "since before sind ui started", pgDiagOf(sn, n, nil, now).Since)
	paused := store.Snapshot{NodeChanges: map[store.NodeKey][]store.Change{{Realm: "sind", Cluster: "dev", Node: "worker-4"}: {
		{At: now.Add(-4 * time.Minute), Source: store.SourceDocker, Subject: "container", State: "paused"},
	}}}
	assert.Equal(t, "since 09:27 · 4 min", pgDiagOf(paused, n, nil, now).Since, "since sind ui saw it pause")
	n.State = cluster.StateUnknown
	d = pgDiagOf(sn, n, nil, now)
	assert.Equal(t, "Docker reports a state of worker-4 that sind does not know", d.Title)
	assert.Empty(t, d.Ladder)
}

func TestPgDiagStopped(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 31, 17, 0, time.Local)
	sn := store.Snapshot{}
	n := fixWorker()
	n.State = cluster.StateStopped
	d := pgDiagOf(sn, n, nil, now)
	assert.Equal(t, "worker-4 is stopped, so it takes no jobs", d.Title)
	assert.Equal(t, "The container does not run. Start brings it back; systemd then starts its services.", d.Text)
	assert.Empty(t, d.Facts)

	n.Fault = &store.Fault{Exit: &store.Exit{Code: 137, OOMKilled: true, At: now.Add(-time.Hour), Error: "oci runtime error"}}
	d = pgDiagOf(sn, n, nil, now)
	assert.Equal(t, "The container stopped at 08:31:17 with exit code 137: the kernel killed it, as it ran out of memory. Start brings it back; systemd then starts its services.", d.Text)
	assert.Equal(t, "since 08:31 · 1 h", d.Since)
	assert.Equal(t, []pgFact{{"stopped", "08:31:17, exit code 137, out of memory", true}, {"Docker said", "oci runtime error", false}}, d.Facts)
	n.Fault.Exit = &store.Exit{Code: 143}
	d = pgDiagOf(sn, n, nil, now)
	assert.Equal(t, []pgFact{{"stopped", "exit code 143", true}}, d.Facts)
	assert.Empty(t, d.Since)
}

func TestPgDiagSlurm(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 31, 17, 0, time.Local)
	key := store.NodeKey{Realm: "sind", Cluster: "dev", Node: "worker-4"}
	sn := store.Snapshot{NodeChanges: map[store.NodeKey][]store.Change{key: {
		{At: now.Add(-time.Minute), Source: store.SourceSystemd, Subject: "munge", OK: true},
		{At: now.Add(-3 * time.Minute), Source: store.SourceSlurm, Subject: "slurm", State: "drained"},
	}}}
	n := fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "drained", Reason: "memory errors"}
	d := pgDiagOf(sn, n, nil, now)
	assert.Equal(t, "warn", d.Sev)
	assert.Equal(t, "worker-4 is drained: Slurm starts no new job on it", d.Title)
	assert.Equal(t, "Reason: memory errors. Its services are active and its container runs. A drain is often on purpose: resume it when the cause is gone.", d.Text)
	assert.Equal(t, "since 09:28 · 3 min", d.Since)
	n.Slurm = &slurm.NodeInfo{State: "draining"}
	assert.Equal(t, "worker-4 is draining: it ends its jobs and takes no new ones", pgDiagOf(sn, n, nil, now).Title)
	n.Slurm = &slurm.NodeInfo{State: "down*"}
	d = pgDiagOf(store.Snapshot{}, n, nil, now)
	assert.Equal(t, "warn", d.Sev, "its services run: amber")
	assert.Equal(t, "Slurm marks worker-4 down*: it takes no jobs", d.Title)
	assert.Equal(t, "Its services are active and its container runs. slurmctld does not count it as up. Resume asks Slurm to take it back.", d.Text)
	assert.Equal(t, "since before sind ui started", d.Since, "sind ui saw no change of it")
}

func TestPgFixed(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 31, 17, 0, time.Local)
	end := now.Add(-10 * time.Second)
	restart := store.Task{ID: "t1", Action: "restart-service", Display: "sind ssh worker-4.dev -- systemctl restart slurmd", Realm: "sind", Cluster: "dev",
		Form: map[string][]string{"nodes": {"worker-4"}, "unit": {"slurmd"}}, Fixes: []string{"worker-4"}, Start: end.Add(-1200 * time.Millisecond), End: &end, Status: store.TaskOK}
	key := store.NodeKey{Realm: "sind", Cluster: "dev", Node: "worker-4"}
	sn := store.Snapshot{Tasks: []store.Task{restart}, NodeChanges: map[store.NodeKey][]store.Change{key: {
		{At: end.Add(2 * time.Second), Source: store.SourceSystemd, Subject: "slurmd", OK: true},
		{At: end.Add(-time.Hour), Source: store.SourceSystemd, Subject: "slurmd"},
	}}}
	n := fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "idle"}
	f := pgFixedOf(sn, n, now)
	require.NotNil(t, f)
	assert.False(t, f.Wait)
	assert.Equal(t, "ok", f.Class())
	assert.Equal(t, "Fixed: slurmd is active again, and worker-4 takes jobs", f.Title)
	assert.Equal(t, "Restart slurmd ended ok at 09:31:07, in 1.2s.", f.What)
	assert.Equal(t, []pgFixCheck{
		{"slurmd is active since 09:31:09.", true},
		{"Slurm state is idle: new jobs can start on worker-4.", true},
	}, f.Checks)
	assert.Equal(t, "8.0s", f.QuietFor, "no change since slurmd came up")
	assert.Equal(t, end.Add(2*time.Second).UnixMilli(), f.Quiet)
	assert.Equal(t, "slurmd", f.Unit)
	assert.Equal(t, "/tasks/t1", f.Link)
	assert.Equal(t, 20, f.Left)
	assert.Equal(t, end.Add(fixedFor).UnixMilli(), f.HideAt)
	assert.False(t, f.back)

	// Its run's page, once the task wrote one; a change a moment ago.
	sn.Tasks[0].Trace = "tr"
	sn.Runs = []store.Run{{ID: "r9", Trace: "tr"}}
	f = pgFixedOf(sn, n, end.Add(2*time.Second))
	assert.Equal(t, "/runs/r9", f.Link)
	assert.Equal(t, "0.0s", f.QuietFor)

	// The status does not show the fix yet: each check says what is still
	// wrong, and the card waits.
	n.Services[1].OK = false
	n.Slurm.State = "down*"
	f = pgFixedOf(sn, n, now)
	assert.True(t, f.Wait)
	assert.Equal(t, "run", f.Class())
	assert.Equal(t, "Restart slurmd ended ok: sind ui checks worker-4 again", f.Title)
	assert.Equal(t, []pgFixCheck{
		{"slurmd is not active yet.", false},
		{"Slurm state is down*: no new job starts on worker-4 yet.", false},
	}, f.Checks)
	n = fixWorker()
	n.Slurm = &slurm.NodeInfo{State: "idle"}

	// Resume.
	sn.Tasks[0] = store.Task{ID: "t2", Action: "power", Realm: "sind", Cluster: "dev", Form: map[string][]string{"nodes": {"worker-4"}, "op": {"resume"}},
		Fixes: []string{"worker-4"}, Start: end.Add(-time.Second), End: &end, Status: store.TaskOK}
	f = pgFixedOf(sn, n, now)
	assert.Equal(t, "Fixed: worker-4 takes jobs again", f.Title)
	assert.Equal(t, pgFixCheck{"munge is active.", true}, f.Checks[0])
	assert.Equal(t, "worker-4", f.Unit)

	// Start: the node runs again and takes jobs, named once.
	sn.Tasks[0].Form["op"] = []string{"start"}
	f = pgFixedOf(sn, n, now)
	assert.Equal(t, "Fixed: worker-4 runs again and takes jobs", f.Title)
	assert.Equal(t, pgFixCheck{"The container runs.", true}, f.Checks[0])
	// Of a node that sinfo does not list.
	n.Slurm = nil
	f = pgFixedOf(sn, n, now)
	assert.Equal(t, "Fixed: worker-4 runs again", f.Title)
	// Of a node that does not run yet.
	n.State = cluster.StateStopped
	f = pgFixedOf(sn, n, now)
	assert.True(t, f.Wait)
	assert.Equal(t, []pgFixCheck{{"The container does not run yet.", false}, {"munge is not active yet.", false}, {"slurmd is not active yet.", false}}, f.Checks)

	// Too long ago, failed, or of another node: none.
	assert.Nil(t, pgFixedOf(sn, n, now.Add(fixedFor)))
	sn.Tasks[0].Status = store.TaskFailed
	assert.Nil(t, pgFixedOf(sn, n, now))
}

func TestPgCameBack(t *testing.T) {
	start := time.Date(2026, 10, 21, 9, 31, 0, 0, time.Local)
	end := start.Add(2 * time.Second)
	restart := store.Task{Action: "restart-service", Form: map[string][]string{"unit": {"slurmd"}}, Start: start, End: &end}
	at := func(d time.Duration) time.Time { return end.Add(d) }
	svc := func(name string, d time.Duration, ok bool) store.Change {
		return store.Change{At: at(d), Source: store.SourceSystemd, Subject: name, OK: ok}
	}
	// The newest first, as the store keeps them.
	before := svc("slurmd", -time.Hour, false)
	assert.False(t, pgCameBack([]store.Change{before}, restart), "it never came up")
	assert.False(t, pgCameBack([]store.Change{svc("slurmd", time.Second, true), before}, restart), "it holds")
	assert.True(t, pgCameBack([]store.Change{svc("slurmd", 5*time.Second, false), svc("slurmd", time.Second, true), before}, restart), "up, then down again")
	assert.False(t, pgCameBack([]store.Change{svc("slurmd", -time.Second, false), svc("slurmd", -1500*time.Millisecond, true)}, restart),
		"what changed while it ran is no come-back")
	assert.False(t, pgCameBack([]store.Change{svc("munge", 5*time.Second, false), svc("munge", time.Second, true)}, restart), "another unit")

	resume := store.Task{Action: "power", Form: map[string][]string{"op": {"resume"}}, Start: start, End: &end}
	sl := func(d time.Duration, ok bool) store.Change {
		return store.Change{At: at(d), Source: store.SourceSlurm, Subject: "Slurm", OK: ok}
	}
	assert.True(t, pgCameBack([]store.Change{sl(5*time.Second, false), sl(time.Second, true)}, resume))
	assert.False(t, pgCameBack([]store.Change{svc("slurmd", 5*time.Second, false), svc("slurmd", time.Second, true)}, resume), "a service is not what a resume fixed")

	reboot := store.Task{Action: "power", Form: map[string][]string{"op": {"reboot"}}, Start: start, End: &end}
	box := func(d time.Duration, ok bool) store.Change {
		return store.Change{At: at(d), Source: store.SourceDocker, Subject: "container", OK: ok}
	}
	assert.True(t, pgCameBack([]store.Change{box(5*time.Second, false), box(-time.Second, true)}, reboot), "it ran, then stopped again")
	assert.True(t, pgCameBack([]store.Change{svc("slurmd", 5*time.Second, false), svc("slurmd", time.Second, true)}, reboot))
	assert.False(t, pgCameBack([]store.Change{sl(5*time.Second, false), sl(time.Second, true)}, reboot), "Slurm takes a node back on its own time")
}

func TestPgNodeLogsEnd(t *testing.T) {
	d := &pgNodeView{Node: fixWorker()}
	assert.Empty(t, d.LogsEnd(), "a node that runs")
	assert.Empty(t, d.LogsNote())
	assert.Empty(t, d.LogsEnded())
	d.Node.State = cluster.StateStopped
	assert.Equal(t, "worker-4 is stopped. Its container output ends where it stopped, and its journal can be read only while it runs.", d.LogsNote())
	// The strip of the problem says it, unless it waits for a fix.
	d.Diag = &pgDiag{}
	assert.Empty(t, d.LogsNote())
	assert.NotEmpty(t, d.LogsEnd())
	d.Diag.ShowAt = 1
	assert.NotEmpty(t, d.LogsNote())
}

func TestPgLineClass(t *testing.T) {
	assert.Equal(t, "err", pgLineClass("fatal: no config"))
	assert.Equal(t, "err", pgLineClass("slurmd.service: Failed with result 'exit-code'."))
	assert.Equal(t, "warn", pgLineClass("WARNING: low memory"))
	assert.Empty(t, pgLineClass("slurmd started"))
}

func TestPgNodeWorkspace(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	pgFillStatus(ts.st)
	ts.st.SetRuns(nil)
	failed := pgNow.Add(-29 * time.Minute)
	ts.st.SetFaults("sind", "dev", map[string]store.Fault{
		"worker-1": {Units: []store.Unit{{Name: "slurmd", State: "failed", Status: 1, Since: failed, Journal: []store.JournalLine{
			{At: failed, Text: "slurmd[212]: error: Unable to register"},
		}}}},
		"worker-3": {Exit: &store.Exit{Code: 137, At: pgNow.Add(-time.Hour)}},
	})

	// The card of the problem, with the fixes in order and the end of the
	// journal; the restart beside the service.
	body := pgBody(t, ts, "/nodes/sind/dev/worker-1", 200)
	assert.Contains(t, body, `<h2 id="pg-diag-t">slurmd failed, so worker-1 takes no jobs</h2>`)
	assert.Contains(t, body, "since "+pgHM(failed)+" · 29 min")
	assert.Contains(t, body, `<div class="cmdlabel">Try in this order</div>`)
	assert.Contains(t, body, `<code class="pg-rc">sind ssh worker-1.dev -- systemctl restart slurmd</code>`)
	assert.Contains(t, body, `<code class="pg-rc">sind power reboot worker-1.dev</code>`)
	assert.Contains(t, body, `<a class="btn primary" href="/dialogs/restart-service?cluster=dev&amp;nodes=worker-1&amp;realm=sind&amp;unit=slurmd"`)
	assert.Contains(t, body, "slurmd[212]: error: Unable to register")
	assert.Contains(t, body, `href="/nodes/sind/dev/worker-1?source=journal&amp;tab=logs&amp;unit=slurmd">Open the full journal</a>`)
	assert.Contains(t, body, `hx-push-url="false">Restart…</a>`)
	assert.Contains(t, body, `<span class="pill bad" data-say="worker-1.dev is running · takes no jobs.">running · takes no jobs</span>`)

	// Its Logs tab: the log of the node of the page, with the first fix
	// at hand, and a stream that no render ends.
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1?tab=logs&source=journal&unit=slurmd", 200)
	assert.Contains(t, body, "<title>Logs · worker-1.dev · sind ui</title>")
	assert.Contains(t, body, `<a class="tab on" href="/nodes/sind/dev/worker-1?source=journal&amp;tab=logs&amp;unit=slurmd" aria-current="page">Logs</a>`)
	assert.Contains(t, body, "data-logs-fixed")
	assert.Contains(t, body, `pg-strip`)
	assert.Contains(t, body, `<div class="fld logs-fld" hidden><label for="logs-node">Node</label>`, "no picker of the node")
	var watch func(Params) []store.Topic
	for _, f := range pgNodeFragments() {
		if f.Name == "pg-node-body" {
			watch = f.Watch
		}
	}
	require.NotNil(t, watch)
	assert.Nil(t, watch(Params{"tab": {"logs"}, "realm": {"sind"}, "cluster": {"dev"}}))
	assert.NotEmpty(t, watch(Params{"realm": {"sind"}, "cluster": {"dev"}}))

	// A node that does not run: where its log ends.
	body = pgBody(t, ts, "/nodes/sind/dev/worker-3?tab=logs", 200)
	stop := pgHMS(pgNow.Add(-time.Hour))
	assert.Contains(t, body, `<span class="pg-strip-x">Its container output ends at `+stop+`, where it stopped, and its journal can be read only while it runs.</span>`,
		"the strip of the problem says where the log ends")
	assert.NotContains(t, body, `callout warn pg-note`)
	assert.Contains(t, body, `data-logs-ended="Stopped at `+stop+`. The log ends there."`, "in place of Follow")
	ts.st.SetFaults("sind", "dev", nil)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-3?tab=logs", 200)
	assert.Contains(t, body, `<span class="pg-strip-x">Its container output ends where it stopped`)
	assert.Contains(t, body, `data-logs-ended="The container is stopped. The log ends there."`)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1?tab=logs", 200)
	assert.NotContains(t, body, "Its container output ends")
	assert.NotContains(t, body, "data-logs-ended")

	// A restart ended ok a moment ago, but the status does not show slurmd
	// up yet: the card of the fix waits, and the card of the problem shows
	// once it hides.
	ts.st.SetClock(func() time.Time { return pgNow })
	end := pgNow.Add(-5 * time.Second)
	hide := strconv.FormatInt(end.Add(fixedFor).UnixMilli(), 10)
	ts.st.AddTask(store.Task{ID: "t1", Action: "restart-service", Display: "sind ssh worker-1.dev -- systemctl restart slurmd", Realm: "sind", Cluster: "dev",
		Form: map[string][]string{"nodes": {"worker-1"}, "unit": {"slurmd"}}, Fixes: []string{"worker-1"}, Start: end.Add(-time.Second), End: &end, Status: store.TaskOK})
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1", 200)
	assert.Contains(t, body, `<section class="panel pg-diag run" data-hide-at="`+hide+`"`)
	assert.Contains(t, body, "Restart slurmd ended ok: sind ui checks worker-1 again")
	assert.Contains(t, body, "slurmd is not active yet.")
	assert.Contains(t, body, `<section class="panel pg-diag bad" data-show-at="`+hide+`"`)
	assert.Contains(t, body, "Restart slurmd ended ok at "+pgHMS(end)+", in 1.0s. The problem is still there.")
	assert.NotContains(t, body, "came back")
	assert.Contains(t, body, `<a href="/tasks/t1">Restart slurmd</a> ended ok`, "Recent changes has it")
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-1?tab=logs", 200), `pg-strip" data-show-at="`+hide+`"`)

	// slurmd is up: the green card, which counts down until the page
	// hides it.
	setSlurmd := func(up bool) {
		st := *ts.st.Snapshot().Status("sind", "dev")
		st.Nodes = slices.Clone(st.Nodes)
		for i, n := range st.Nodes {
			if n != nil && n.Name == "worker-1.dev" {
				c := *n
				h := *n.Health
				h.Services = maps.Clone(h.Services)
				h.Services[pgProbe("slurmd")] = up
				c.Health = &h
				st.Nodes[i] = &c
			}
		}
		ts.st.SetStatus("sind", "dev", &st)
	}
	setSlurmd(true)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1", 200)
	assert.Contains(t, body, `<section class="panel pg-diag ok" data-hide-at="`+hide+`"`)
	assert.Contains(t, body, "Fixed: slurmd is active again")
	assert.Contains(t, body, "slurmd is active since "+pgHMS(pgNow)+".")
	assert.Contains(t, body, `Shows for <span data-until="`+hide+`">25 s</span> more`)
	assert.Contains(t, body, `No change for <span data-since="`+strconv.FormatInt(pgNow.UnixMilli(), 10)+`">0.0s</span>.`)
	assert.NotContains(t, body, "Try in this order")

	// slurmd failed again: the red card says that the problem came back.
	setSlurmd(false)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1", 200)
	assert.Contains(t, body, "Restart slurmd ended ok at "+pgHMS(end)+", in 1.0s. Since then, the problem came back.")
	assert.NotContains(t, body, "Fixed: ")
	assert.NotContains(t, body, "data-show-at")
}
