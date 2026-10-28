// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// timedOut is a target of the node that ran out of a wait of d for svc.
func timedOut(node, svc string, d time.Duration) store.Span {
	return runSp("target", node, runEndedAt(0, 9, "failed"), runWith(func(s *store.Span) {
		s.Node, s.Role, s.Class, s.Err = node, "worker", "timeout", "node "+node+": timed out"
		s.Children = []store.Span{runSp("wait", "ready", runEndedAt(1, 9, "failed"), runWith(func(w *store.Span) {
			w.Message, w.Timeout, w.Class = svc, d, "timeout"
		}))}
	}))
}

func TestRunTimeoutSays(t *testing.T) {
	says := runTimeoutSays([]runTimeout{
		{Node: "worker-1", Service: "munge"}, {Node: "worker-2", Service: "munge"},
		{Node: "worker-3", Service: "slurmctld"}, {Node: "db", Service: "sshd"},
		{Node: "a", Service: "container"}, {Node: "b", Service: "systemd"}, {Node: "c"}, {Node: "d", Service: "dbus"},
	})
	assert.Equal(t, []runPlain{
		{Word: "munge", Text: " on worker-[1-2] did not start in time. Without munge, the nodes cannot talk to slurmctld."},
		{Word: "slurmctld", Text: " on worker-3 did not start in time. Without slurmctld, the cluster has no controller."},
		{Word: "sshd", Text: " on db did not start in time. Without sshd, the node takes no ssh logins."},
		{Text: "The container of a did not start in time."},
		{Text: "systemd on b did not finish booting in time."},
		{Text: "c did not get ready in time."},
		{Text: "dbus on d did not start in time."},
	}, says)
}

func TestRunWaits(t *testing.T) {
	assert.Equal(t, "1h", runWaitWord(time.Hour))
	assert.Equal(t, "5m", runWaitWord(5*time.Minute))
	assert.Equal(t, "1m30s", runWaitWord(90*time.Second))

	wait := func(d time.Duration) ([]string, string) {
		return runRetryWait(runOf(9, "failed", runSp("step", "nodes", runKids(timedOut("worker-1", "munge", d)))))
	}
	kv, word := wait(0)
	assert.Equal(t, []string{"wait", "10m"}, kv, "the log names no wait")
	assert.Equal(t, "10m", word)
	kv, word = wait(2 * time.Minute)
	assert.Equal(t, []string{"wait", "5m"}, kv)
	assert.Equal(t, "5m", word)
	kv, word = wait(10 * time.Minute)
	assert.Equal(t, []string{"wait", "other", "wait_other", "20m"}, kv, "twice a wait above the choices")
	assert.Equal(t, "20m", word)
}

func TestRunRollbackWord(t *testing.T) {
	word := func(rb store.Span) string { return runRollbackWord(runOf(9, "failed", rb)) }
	assert.Equal(t, "sind rolled back · nothing is left", word(runSp("step", "rollback", runEndedAt(1, 2, "ok"))))
	assert.Equal(t, "the rollback failed", word(runSp("step", "rollback", runEndedAt(1, 2, "failed"))))
	assert.Equal(t, "sind rolls back", runRollbackWord(runOf(0, "", runSp("step", "rollback", runStartedAt(1)))))
	assert.Equal(t, "the rollback did not finish", word(runSp("step", "rollback", runStartedAt(1))))
	assert.Empty(t, word(runSp("step", "nodes", runEndedAt(1, 2, "ok"))))
}

func TestRunDoctorChecks(t *testing.T) {
	checks, text := runDoctorChecks(store.Snapshot{})
	assert.Nil(t, checks)
	assert.Empty(t, text)
	checks, text = runDoctorChecks(store.Snapshot{Doctor: &store.DoctorReport{Checks: []store.DoctorCheck{{Name: "Docker daemon", Status: "ok"}}}})
	assert.Nil(t, checks, "every check passed")
	assert.Empty(t, text)
	st := store.New()
	pgFillStore(st)
	checks, text = runDoctorChecks(st.Snapshot())
	require.Len(t, checks, 2)
	assert.Equal(t, "inotify", checks[0].Name)
	assert.Equal(t, "Doctor: 1 failed, 1 warning", text)
	_, text = runDoctorChecks(store.Snapshot{Doctor: &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "warning"}, {Status: "warning"}}}})
	assert.Equal(t, "Doctor: 2 warnings", text)
}

func TestRunFailOutcome(t *testing.T) {
	st := store.New()
	pgFillStore(st)
	snap := st.Snapshot()
	at := func(run store.Run) *runOutcome {
		return runOutcomeOf(runCtx{Run: run, Found: true, Snap: snap, Now: runAt(30)})
	}

	// A create cluster whose nodes ran out of the wait: the cluster was
	// not created, the host checks that did not pass, and Try again.
	run := runOf(9, "failed", runSp("step", "nodes", runEndedAt(0, 9, "failed"), runKids(timedOut("worker-1", "munge", 5*time.Minute))))
	run.Title, run.Root.Node, run.Class, run.Err = "create cluster bench", "bench", "timeout", "setting up nodes: node worker-1: timed out"
	o := at(run)
	require.NotNil(t, o)
	assert.Equal(t, "bad", o.Class)
	assert.Equal(t, "✗", o.Mark)
	assert.Equal(t, "bench was not created", o.Title)
	assert.Equal(t, "worker-1 ran out of the 5m wait", o.Meta)
	assert.Empty(t, o.Realm)
	assert.Len(t, o.Why, 2)
	assert.Equal(t, "Doctor: 1 failed, 1 warning", o.DoctorText)
	assert.Equal(t, []pgAction{{Label: "Fix the host in Doctor", URL: "/doctor"}}, o.Next)
	assert.Equal(t, "setting up nodes: node worker-1: timed out", o.CopyErr)
	assert.Equal(t, "Try again with a 10m wait, at the top, opens New cluster, rebuilt from the event log.", o.NextNote)
	assert.Equal(t, []runPlain{{Word: "munge", Text: " on worker-1 did not start in time. Without munge, the node cannot talk to slurmctld."}}, o.Fail.Says)

	// Another command, a timeout without a wait, a host that passes every
	// check, and nothing to try again.
	snap.Doctor = nil
	power := runOf(9, "failed", runSp("step", "nodes", runEndedAt(0, 9, "failed"), runKids(timedOut("worker-1", "slurmd", 0))))
	power.Title, power.Root.Name = "power reboot worker-1.dev", "power reboot"
	o = at(power)
	assert.Equal(t, "power reboot worker-1.dev failed", o.Title)
	assert.Equal(t, "worker-1 timed out", o.Meta)
	assert.Empty(t, o.DoctorText)
	assert.Equal(t, []pgAction{{Label: "Check the host in Doctor", URL: "/doctor"}}, o.Next)
	assert.Empty(t, o.NextNote)

	// Warnings alone: Check the host. A create worker from a terminal
	// opens its dialog, and one of a task the form of the run.
	snap.Doctor = &store.DoctorReport{Checks: []store.DoctorCheck{{Name: "inotify", Status: "warning"}}}
	snap.Clusters = map[string][]cluster.Summary{"sind": {{Name: "dev"}}}
	grow := runOf(9, "failed", runSp("step", "nodes", runEndedAt(0, 9, "failed"), runKids(timedOut("worker-1", "slurmd", 0))))
	grow.Title, grow.Root.Name, grow.Root.Node = "create worker worker-1.dev", "create worker", "dev"
	o = at(grow)
	assert.Equal(t, []pgAction{{Label: "Check the host in Doctor", URL: "/doctor"}}, o.Next)
	assert.Equal(t, "Add a worker again, at the top, opens its dialog.", o.NextNote)
	assert.Equal(t, "opens the form of this run", runEditWhat(runCtx{Run: grow, Task: &store.Task{}}))

	// A failure without a target that timed out: how long it took, and
	// the rollback.
	plain := runOf(12, "failed", runSp("step", "rollback", runEndedAt(10, 11, "ok")))
	plain.Root.Name, plain.Title, plain.Err = "delete cluster", "", "boom"
	o = at(plain)
	assert.Equal(t, "r1 failed", o.Title)
	assert.Equal(t, "failed after 12s · sind rolled back · nothing is left", o.Meta)
	assert.Nil(t, o.Why)

	// A target failed while the run goes on.
	live := runOf(0, "", runSp("step", "nodes", runStartedAt(0), runKids(
		runSp("target", "w-1", runEndedAt(0, 1, "failed"), runWith(func(s *store.Span) { s.Err = "w-1: bad" })),
	)))
	o = at(live)
	assert.Equal(t, "What failed", o.Title)
	assert.Empty(t, o.Meta)

	// Stopped: no error to copy, and no host checks.
	stopped := runOf(5, "canceled", runSp("step", "nodes", runEndedAt(0, 5, "canceled")))
	snap.Doctor = st.Snapshot().Doctor
	o = at(stopped)
	assert.Equal(t, "warn", o.Class)
	assert.Equal(t, "~", o.Mark)
	assert.Equal(t, "Stopped from the terminal", o.Title)
	assert.Equal(t, "stopped after 5.0s", o.Meta)
	assert.Empty(t, o.CopyErr)
	assert.Nil(t, o.Doctor)
	assert.Nil(t, o.Next)
}

func TestRunPageOfAFailureWithDoctorChecks(t *testing.T) {
	ts := runTestServer(t, runNow)
	pgFillStore(ts.st)
	runStore(t, ts)
	body := ts.get("/runs/r-failed").Body.String()
	for _, want := range []string{
		`<div class="rout-doc"><b>Doctor: 1 failed, 1 warning</b>`,
		`<span class="pg-ck warn" aria-hidden="true">!</span><div><b>inotify</b> <span class="muted">fs.inotify.max_user_watches is 8192</span></div>`,
		`<span class="pg-ck bad" aria-hidden="true">✗</span><div><b>cgroupv2</b>`,
		`<a class="btn" href="/doctor">Fix the host in Doctor</a>`,
	} {
		assert.Contains(t, body, want)
	}
}
