// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// jumpLines returns the entries of the jump box for q, without help and
// words, each as "group | label | sub | cli | href | step op | copy".
func jumpLines(sn store.Snapshot, ctx jumpContext, q string) []string {
	var out []string
	for _, it := range jump(help.Default(), sn, pgNow, ctx, q).Items {
		if it.rank == jumpRankHelp || it.rank == jumpRankWord {
			continue
		}
		out = append(out, it.groupName()+" | "+it.Label+" | "+it.Sub+" | "+it.CLI+" | "+it.Href+" | "+it.Step+" "+it.Op+" | "+it.Copy)
	}
	return out
}

func TestJumpVerbs(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	pgFillStatus(ts.st)
	ts.st.SetRuns(nil)
	sn := ts.st.Snapshot()
	dev, hpc := jumpContext{Realm: "sind", Cluster: "dev"}, jumpContext{Realm: "sind", Cluster: "hpc"}

	for _, c := range []struct {
		name string
		ctx  jumpContext
		q    string
		want []string
		not  []string
	}{
		{name: "a power op on a node set", q: "reboot worker-[1-2].dev", want: []string{
			"Actions | Reboot worker-[1-2].dev… | Opens Power with Reboot and worker-[1-2] ticked | sind power reboot worker-[1-2].dev | /dialogs/power?cluster=dev&nodes=worker-1%2Cworker-2&op=reboot&realm=sind |   | ",
			"Actions | Reboot other nodes… | Pick a cluster, then tick the nodes | sind power reboot NODES |  | power reboot | ",
			"Nodes | worker-1.dev | running |  | /nodes/sind/dev/worker-1 |   | ",
		}},
		{name: "a Slurm op that is off, after the first fix", q: "drain worker-1.dev", want: []string{
			"Actions | Restart slurmd on worker-1.dev… | The first fix to try | sind ssh worker-1.dev -- systemctl restart slurmd | /dialogs/restart-service?cluster=dev&nodes=worker-1&realm=sind&unit=slurmd |   | ",
			"Actions | Drain worker-1.dev… | sinfo does not list it. | sind exec dev -- scontrol update NodeName=worker-1 State=DRAIN Reason=WHY | /dialogs/power?cluster=dev&nodes=worker-1&op=drain&realm=sind |   | ",
			"Actions | Drain other nodes… | Pick a cluster, then tick the nodes | sind exec CLUSTER -- scontrol update NodeName=NODES State=DRAIN Reason=WHY |  | power drain | ",
			"More for worker-1.dev | Reboot worker-1.dev… | Stops, then starts the container. Running jobs on it fail. | sind power reboot worker-1.dev | /dialogs/power?cluster=dev&nodes=worker-1&op=reboot&realm=sind |   | ",
		}, not: []string{"More for worker-1.dev | Restart slurmd"}},
		{name: "a Slurm op on a cluster", q: "resume hpc", want: []string{
			"Actions | Resume nodes of hpc… | Opens Power with Resume; tick the nodes | sind exec hpc -- scontrol update NodeName=NODES State=RESUME | /dialogs/power?cluster=hpc&op=resume&realm=sind |   | ",
		}},
		{name: "a Slurm op of the scope", ctx: jumpContext{Realm: "ci"}, q: "drain", want: []string{
			"Actions | Drain nodes… | Pick a cluster, then tick the nodes | sind --realm ci exec CLUSTER -- scontrol update NodeName=NODES State=DRAIN Reason=WHY |  | power drain | ",
		}},
		{name: "the commands to pick next name the scope", ctx: jumpContext{Realm: "ci"}, q: "delete", want: []string{
			"Actions | Delete a cluster… | Pick the cluster, then confirm | sind --realm ci delete cluster CLUSTER |",
			"Actions | Delete workers… | Pick the cluster, then tick the workers | sind --realm ci delete worker NODES |",
		}},
		{name: "a power op to pick next names the scope", ctx: jumpContext{Realm: "ci"}, q: "stop", want: []string{
			"Actions | Shut down nodes… | Pick a cluster, then tick the nodes | sind --realm ci power shutdown NODES |",
		}},
		{name: "add to a cluster to pick next names the scope", ctx: jumpContext{Realm: "ci"}, q: "add", want: []string{
			"Actions | Add workers… | Pick the cluster first | sind --realm ci create worker CLUSTER |",
		}},
		{name: "restart a node whose service failed: its service first", q: "restart worker-1.dev", want: []string{
			"Actions | Restart slurmd on worker-1.dev… | Restarts one service; the container goes on | sind ssh worker-1.dev -- systemctl restart slurmd | /dialogs/restart-service?cluster=dev&nodes=worker-1&realm=sind&unit=slurmd |   | ",
			"Actions | Reboot worker-1.dev… | Opens Power with Reboot and worker-1 ticked | sind power reboot worker-1.dev | /dialogs/power?cluster=dev&nodes=worker-1&op=reboot&realm=sind |   | ",
			"Actions | Restart a service… | Pick a cluster, then the node and the service | sind ssh NODE -- systemctl restart UNIT |  | restart-service  | ",
		}, not: []string{"More for worker-1.dev | Reboot worker-1.dev…"}},
		{name: "restart a node that does not run", q: "restart worker-3.dev", not: []string{"Restarts one service"}},
		{name: "restart a cluster", q: "restart dev", want: []string{
			"Actions | Reboot nodes of dev… | Opens Power with Reboot; tick the nodes | sind power reboot NODES | /dialogs/power?cluster=dev&op=reboot&realm=sind |   | ",
			"Actions | Restart a service of a node of dev… | Restarts one service; the container goes on |  | /dialogs/restart-service?cluster=dev&realm=sind |   | ",
			"Clusters | dev |  |  | /clusters/sind/dev |   | ",
		}},
		{name: "a power op on the cluster of the page", ctx: dev, q: "restart", want: []string{
			"Actions | Reboot nodes of dev… | Opens Power with Reboot; tick the nodes | sind power reboot NODES | /dialogs/power?cluster=dev&op=reboot&realm=sind |   | ",
			"Actions | Reboot nodes… | Pick a cluster, then tick the nodes | sind power reboot NODES |  | power reboot | ",
		}},
		{name: "a command line", q: "sind power stop worker-3.dev", want: []string{
			"Actions | Start worker-3.dev… | The first fix to try | sind power on worker-3.dev | /dialogs/power?cluster=dev&nodes=worker-3&op=start&realm=sind |   | ",
			"Actions | Shut down worker-3.dev… | worker-3.dev does not run. | sind power shutdown worker-3.dev | /dialogs/power?cluster=dev&nodes=worker-3&op=shutdown&realm=sind |   | ",
		}, not: []string{"More for worker-3.dev | Reboot", "More for worker-3.dev | Start"}},
		{name: "the logs of a node", q: "logs worker-1.dev", want: []string{
			"Actions | Logs of slurmd on worker-1.dev | The Logs tab of the node | sind logs worker-1.dev slurmd | /nodes/sind/dev/worker-1?source=journal&tab=logs&unit=slurmd |   | ",
		}, not: []string{"More for worker-1.dev | Logs"}},
		{name: "logs alone: the nodes that need a look", q: "logs", want: []string{
			"Actions | Logs of worker-0 | The Logs tab of the node | sind --realm ci logs worker-0 | /nodes/ci/default/worker-0?tab=logs |   | ",
			"Actions | Logs of worker-3.dev | The Logs tab of the node | sind logs worker-3.dev | /nodes/sind/dev/worker-3?tab=logs |   | ",
		}, not: []string{"Logs of dev"}},
		{name: "logs on a cluster page", ctx: dev, q: "logs", want: []string{
			"Actions | Logs of dev | The Logs tab of the cluster | sind logs NODE | /clusters/sind/dev?tab=logs |   | ",
		}},
		{name: "the logs of a cluster", q: "log hpc", want: []string{
			"Actions | Logs of hpc | The Logs tab of the cluster | sind logs NODE | /clusters/sind/hpc?tab=logs |   | ",
		}},
		{name: "ssh a short name: in every cluster", q: "ssh worker-0", want: []string{
			"Actions | Copy the ssh command of worker-0 | Logs in to the node | sind --realm ci ssh worker-0 |  |   | sind --realm ci ssh worker-0",
			"Actions | Copy the ssh command of worker-0.hpc | Logs in to the node | sind ssh worker-0.hpc |  |   | sind ssh worker-0.hpc",
		}},
		{name: "delete workers, not a controller", q: "delete worker-[0-1].dev controller.dev", want: []string{
			"Actions | Delete worker-[0-1].dev… | Removes the worker containers. Jobs on them fail | sind delete worker worker-[0-1].dev | /dialogs/delete-worker?cluster=dev&nodes=worker-0%2Cworker-1&realm=sind |   | ",
			"Nodes | controller.dev | running |  | /nodes/sind/dev/controller |   | ",
		}},
		{name: "delete one worker", q: "delete worker-0.dev", want: []string{
			"Actions | Delete worker-0.dev… | Removes the worker container. Jobs on it fail |",
		}},
		{name: "delete a controller", q: "delete controller.dev", not: []string{"Actions"}},
		{name: "delete a cluster", q: "rm cluster hpc", want: []string{
			"Actions | Delete cluster hpc… | Asks first. This cannot be undone | sind delete cluster hpc | /dialogs/delete-cluster?cluster=hpc&realm=sind |   | ",
		}},
		{name: "create worker", q: "sind create worker hpc", want: []string{
			"Actions | Add workers to hpc… | New workers take the sizes of its newest worker | sind create worker hpc | /dialogs/create-worker?cluster=hpc&realm=sind |   | ",
		}},
		{name: "add to the cluster of a node", q: "add worker-0.hpc", want: []string{
			"Actions | Add workers to hpc… | New workers take the sizes of its newest worker | sind create worker hpc | /dialogs/create-worker?cluster=hpc&realm=sind |   | ",
			"More for worker-0.hpc | Reboot worker-0.hpc… | Stops, then starts the container. Running jobs on it fail. | sind power reboot worker-0.hpc | /dialogs/power?cluster=hpc&nodes=worker-0&op=reboot&realm=sind |   | ",
		}},
		{name: "add on a cluster page", ctx: hpc, q: "grow", want: []string{
			"Actions | Add workers to hpc… | New workers take the sizes of its newest worker | sind create worker hpc | /dialogs/create-worker?cluster=hpc&realm=sind |   | ",
		}},
		{name: "a job is no verb", q: "sbatch worker-0.hpc", not: []string{"Actions"}},
		{name: "a verb that is a word of a page or a dialog finds it too", q: "start", want: []string{
			"Actions | Start nodes… |",
			"Other actions | New cluster | Fill in its config, then create it | sind create cluster --config FILE | /clusters/new |",
		}},
		{name: "log finds Activity", q: "log", want: []string{"Pages | Activity |"}},
		{name: "the typed order of a node set", q: "reboot worker-1.hpc,worker-0.dev", want: []string{"Actions | Reboot worker-1.hpc… |"}},
		{name: "a shell on each cluster, the scope first", ctx: jumpContext{Realm: "ci"}, q: "shell", want: []string{
			"Actions | Copy the shell command of default | Opens a shell on its submitter or controller | sind --realm ci enter |  |   | sind --realm ci enter",
			"Actions | Copy the shell command of dev | Opens a shell on its submitter or controller | sind enter dev |  |   | sind enter dev",
		}},
		{name: "a shell on the cluster of the page", ctx: hpc, q: "enter", want: []string{
			"Actions | Copy the shell command of hpc | Opens a shell on its submitter or controller | sind enter hpc |  |   | sind enter hpc",
		}, not: []string{"of dev"}},
		{name: "a prefix names the nodes it starts", q: "reboot wor", want: []string{
			"Actions | Unfreeze worker-0… | The first fix to try | sind --realm ci power unfreeze worker-0 | /dialogs/power?cluster=default&nodes=worker-0&op=unpause&realm=ci |   | ",
			"Actions | Reboot worker-0… | worker-0 is frozen: unfreeze it first. | sind --realm ci power reboot worker-0 | /dialogs/power?cluster=default&nodes=worker-0&op=reboot&realm=ci |   | ",
			"Actions | Reboot worker-[0-3].dev… | Opens Power with Reboot and worker-[0-3] ticked | sind power reboot worker-[0-3].dev | /dialogs/power?cluster=dev&nodes=worker-0%2Cworker-1%2Cworker-2%2Cworker-3&op=reboot&realm=sind |   | ",
		}},
		{name: "a prefix of a cluster", q: "delete hp", want: []string{"Actions | Delete cluster hpc…"}},
		{name: "a node set that names nothing", q: "reboot zzz", want: []string{
			"Actions | Reboot nodes… | Pick a cluster, then tick the nodes | sind power reboot NODES |  | power reboot | ",
		}},
		{name: "a short name on its cluster page", ctx: hpc, q: "worker-0", want: []string{
			"More for worker-0.hpc | Logs of slurmd on worker-0.hpc | The Logs tab of the node | sind logs worker-0.hpc slurmd | /nodes/sind/hpc/worker-0?source=journal&tab=logs&unit=slurmd |   | ",
		}, not: []string{"More for worker-0 |"}},
		{name: "a name of the default cluster", q: "worker-0", want: []string{
			"More for worker-0 | Unfreeze worker-0… | The first fix to try | sind --realm ci power unfreeze worker-0 | /dialogs/power?cluster=default&nodes=worker-0&op=unpause&realm=ci |   | ",
		}},
		{name: "a short name that a page has not", ctx: dev, q: "controller", not: []string{"More for controller |"}},
		{name: "a cluster alone", q: "default", want: []string{
			"More for default | Delete cluster default… | Asks first. This cannot be undone | sind --realm ci delete cluster | /dialogs/delete-cluster?cluster=default&realm=ci |   | ",
		}},
		{name: "a node set alone is no name", q: "worker-[0-1].dev", not: []string{"More for"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			lines := jumpLines(sn, c.ctx, c.q)
			for _, w := range c.want {
				found := false
				for _, l := range lines {
					if len(l) >= len(w) && l[:len(w)] == w {
						found = true
					}
				}
				assert.True(t, found, "%q in %q", w, lines)
			}
			for _, n := range c.not {
				for _, l := range lines {
					assert.NotContains(t, l, n)
				}
			}
		})
	}

	// A node lists its Slurm reason.
	ts.st.SetSlurm("sind", "hpc", &store.Slurm{Info: &cluster.SlurmInfo{Nodes: []slurm.NodeInfo{{Name: "worker-1", State: "drained", Reason: "memory errors"}}}})
	assert.Contains(t, jumpLines(ts.st.Snapshot(), jumpContext{}, "worker-1.hpc"), "Nodes | worker-1.hpc | running · memory errors |  | /nodes/sind/hpc/worker-1 |   | ")
	// A cluster that a command creates has no More.
	ts.st.SetRuns([]store.Run{{ID: "c", Title: "create cluster hpc", Start: pgNow.Add(-1)}})
	assert.NotContains(t, jumpLines(ts.st.Snapshot(), jumpContext{}, "hpc"), "More for hpc")
	// The cluster of the page comes first, for nodes and clusters alike.
	assert.True(t, strings.HasPrefix(jumpLines(ts.st.Snapshot(), hpc, "reboot worker-0")[0], "Actions | Reboot worker-0.hpc… |"))
	fixtureStore(ts.st)
	lines := jumpLines(ts.st.Snapshot(), jumpContext{Realm: "ci", Cluster: "dev"}, "delete dev")
	assert.True(t, strings.HasPrefix(lines[0], "Actions | Delete cluster dev… | Asks first. This cannot be undone | sind --realm ci delete cluster dev |"), lines[0])
	// Two clusters of one name: the name names neither alone.
	assert.NotContains(t, jumpLines(ts.st.Snapshot(), jumpContext{}, "dev"), "More for dev")
	// Without clusters, a verb offers nothing to act on.
	assert.Empty(t, jumpLines(store.Snapshot{}, jumpContext{}, "reboot"))
	// A restart opens with the service that the query names or that
	// failed, else with the reboot; an op that is off for its node, with
	// the first fix of the node; a node set, with its first part.
	for q, first := range map[string]string{
		"sind power stop worker-3.dev":           "Actions | Start worker-3.dev… | The first fix to try |",
		"reboot worker-1.hpc,worker-0.dev":       "Actions | Reboot worker-1.hpc… |",
		"reboot worker-0.dev,worker-1.hpc":       "Actions | Reboot worker-0.dev… |",
		"restart worker-1.dev":                   "Actions | Restart slurmd on worker-1.dev… | Restarts one service; the container goes on | sind ssh worker-1.dev -- systemctl restart slurmd |",
		"restart worker-0.dev":                   "Actions | Reboot worker-0.dev… |",
		"restart munge worker-0.dev":             "Actions | Restart munge on worker-0.dev… | Restarts one service; the container goes on | sind ssh worker-0.dev -- systemctl restart munge |",
		"restart slurmctld service worker-0.dev": "Actions | Reboot worker-0.dev… |",
		"restart slurmd dev":                     "Actions | Restart slurmd on a node of dev… | Restarts one service; the container goes on |  | /dialogs/restart-service?cluster=dev&realm=sind&unit=slurmd |",
	} {
		lines := jumpLines(sn, jumpContext{}, q)
		assert.True(t, strings.HasPrefix(lines[0], first), "%s: %q", q, lines[0])
	}
	// Help alone has no actions of a node or a cluster.
	for _, q := range []string{"?dev", "?controller.dev"} {
		for _, it := range jump(help.Default(), ts.st.Snapshot(), pgNow, jumpContext{}, q).Items {
			assert.Contains(t, []int{jumpRankHelp, jumpRankWord}, it.rank, "%s: %s", q, it.Label)
		}
	}
	// Of the nodes of one name, those of the realm of the scope come first.
	lines = jumpLines(sn, jumpContext{Realm: "ci"}, "reboot worker-0")
	assert.Contains(t, lines[0], "realm=ci", lines[0])
	assert.Contains(t, jumpLines(sn, jumpContext{Realm: "sind"}, "reboot worker-0")[0], "Reboot worker-0.dev…")
}

func TestJumpVerbMiss(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)
	d := jump(help.Default(), ts.st.Snapshot(), pgNow, jumpContext{}, "reboot worker-99")
	assert.Equal(t, "worker-99", d.Miss)
	assert.True(t, strings.HasPrefix(d.Say(), "No node or cluster matches worker-99. "), d.Say())
	body := ts.get("/jump?q=reboot+worker-99").Body.String()
	assert.Contains(t, body, `<div class="pmiss">No node or cluster matches worker-99.</div>`)
	assert.Empty(t, jump(help.Default(), ts.st.Snapshot(), pgNow, jumpContext{}, "reboot").Miss)
	// An op that is off is an option the box does not pick.
	ts.st.SetNodes("ci", []cluster.NodeSummary{{Cluster: "dev", FQDN: "worker-0.dev.ci", Container: "ci-dev-worker-0", Role: config.RoleWorker, State: cluster.StateStopped}})
	body = ts.get("/jump?q=stop+worker-0.dev").Body.String()
	assert.Regexp(t, `<div class="pi off" role="option" id="jump-o\d+" aria-selected="false" aria-disabled="true"><span class="pim"><span class="pil">Shut down worker-0.dev…</span><em>sind creates it now.</em>`, body)
	// A query with a verb lists a few entries of help.
	helps := 0
	for _, it := range jump(help.Default(), ts.st.Snapshot(), pgNow, jumpContext{}, "start").Items {
		if it.rank == jumpRankHelp || it.rank == jumpRankWord {
			helps++
		}
	}
	assert.LessOrEqual(t, helps, jumpVerbHelp)
}

func TestJumpVerbOf(t *testing.T) {
	for q, want := range map[string]jumpVerbQuery{
		"reboot worker-1":            {Word: "reboot", Verb: "reboot", Words: []string{"worker-1"}},
		"sind power stop dev":        {Word: "stop", Verb: "shutdown", Words: []string{"dev"}},
		"SIND create workers to dev": {Word: "add", Verb: jumpVerbAdd, Words: []string{"dev"}},
		"shell":                      {Word: "shell", Verb: jumpVerbEnter},
		"restart slurmd munge dev":   {Word: "restart", Verb: jumpVerbRestart, Words: []string{"munge", "dev"}, Unit: "slurmd"},
	} {
		got, ok := jumpVerbOf(q)
		require.True(t, ok, q)
		assert.Equal(t, want, got, q)
	}
	for _, q := range []string{"", "  ", "sind", "power", "create cluster", "worker-1 reboot", "job dev"} {
		_, ok := jumpVerbOf(q)
		assert.False(t, ok, q)
	}
	for q, want := range map[string]string{
		"stop":                    "stop = sind power shutdown",
		"undrain":                 "undrain = sind exec -- scontrol update State=RESUME",
		"restart":                 "restart = sind power reboot",
		"journal":                 "journal = sind logs",
		"restart slurmd worker-4": "restart slurmd = sind ssh NODE -- systemctl restart slurmd",
	} {
		vq, _ := jumpVerbOf(q)
		assert.Equal(t, want, vq.note(), q)
	}
	_, ok := jumpTarget{Groups: [][]pgNode{{{}}}, Clusters: []pgCluster{{}}}.one()
	assert.False(t, ok, "a node and a cluster are not one node")
}

func TestJumpStepOp(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)
	body := ts.get("/jump?step=power&op=reboot").Body.String()
	assert.Contains(t, body, "Reboot the nodes of which cluster?")
	assert.Contains(t, body, `href="/dialogs/power?cluster=dev&amp;op=reboot&amp;realm=ci"`)
	assert.Equal(t, http.StatusOK, ts.get("/jump?step=power&op=nosuch").Code)
	assert.Empty(t, navGroupOf("nosuch"))
}
