// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// faultStatus is the status of a cluster dev with a stopped worker-1, a
// worker-4 whose slurmd and munge are down, a paused worker-6, a node
// without health and a running db with every service up.
func faultStatus() *cluster.Status {
	return &cluster.Status{Name: "dev", Nodes: []*cluster.NodeStatus{
		nil,
		{Name: "api.dev"},
		{Name: "db.dev", Health: &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{probe.ServiceMunge: true}}},
		{Name: "worker-1.dev", Health: &cluster.NodeHealth{State: cluster.StateStopped}},
		{Name: "worker-4.dev", Health: &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{
			probe.ServiceSlurmd: false, probe.ServiceMunge: false, probe.ServiceSSHD: true,
		}}},
		{Name: "worker-6.dev", Health: &cluster.NodeHealth{State: cluster.StatePaused}},
	}}
}

func TestReadFaults(t *testing.T) {
	var calls [][]string
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		calls = append(calls, args)
		switch {
		case args[0] == "inspect":
			return mock.Result{Stdout: `[{"Id":"w1","Name":"/sind-dev-worker-1","State":{"Status":"exited","ExitCode":137,"OOMKilled":true,"Error":"","FinishedAt":"2026-10-27T08:12:00Z"},"Config":{"Labels":{}}}]`}
		case args[2] == "systemctl" && args[4] == "slurmd":
			return mock.Result{Stdout: "ActiveState=failed\nResult=exit-code\nExecMainStatus=1\nStateChangeTimestamp=@1761555737\nOther=x\n", Err: testutil.ExitCode1(t)}
		case args[2] == "journalctl" && args[4] == "slurmd":
			return mock.Result{Stdout: "1761555731.5 worker-4 slurmd[212]: slurmd version 26.05.4 started\n-- No entries --\nnot-a-time worker-4 x\ntwo words\n1761555737 worker-4 systemd[1]: slurmd.service: Failed with result 'exit-code'.\n"}
		}
		// munge: the exec fails, so nothing more is known.
		return mock.Result{Stderr: "Error response from daemon: container is paused\n", Err: testutil.ExitCode1(t)}
	}}
	got := readFaults(t.Context(), docker.NewClient(m), "sind", "dev", faultStatus())

	assert.Equal(t, map[string]store.Fault{
		"worker-1": {Exit: &store.Exit{Code: 137, OOMKilled: true, At: time.Date(2026, 10, 27, 8, 12, 0, 0, time.UTC)}},
		"worker-4": {Units: []store.Unit{
			{Name: "munge"},
			{Name: "slurmd", State: "failed", Result: "exit-code", Status: 1, Since: time.Unix(1761555737, 0).UTC(), Journal: []store.JournalLine{
				{At: time.Unix(1761555731, 5e8).UTC(), Text: "slurmd[212]: slurmd version 26.05.4 started"},
				{At: time.Unix(1761555737, 0).UTC(), Text: "systemd[1]: slurmd.service: Failed with result 'exit-code'."},
			}},
		}},
	}, normalized(got))
	require.NotEmpty(t, calls)
	assert.Equal(t, []string{"inspect", "sind-dev-worker-1", "--type", "container"}, calls[0])
	assert.Contains(t, calls, []string{"exec", "sind-dev-worker-4", "systemctl", "show", "slurmd",
		"--property=ActiveState,Result,ExecMainStatus,StateChangeTimestamp", "--timestamp=unix"})
	assert.Contains(t, calls, []string{"exec", "sind-dev-worker-4", "journalctl", "-u", "slurmd", "-n", "5", "--no-pager", "-o", "short-unix"})
}

// normalized returns the faults with their times in UTC, which the
// comparison needs, as time.Unix gives local times.
func normalized(faults map[string]store.Fault) map[string]store.Fault {
	for k, f := range faults {
		if f.Exit != nil {
			e := *f.Exit
			e.At = e.At.UTC()
			f.Exit = &e
		}
		for i, u := range f.Units {
			if !u.Since.IsZero() {
				f.Units[i].Since = u.Since.UTC()
			}
			for j, l := range u.Journal {
				f.Units[i].Journal[j].At = l.At.UTC()
			}
		}
		faults[k] = f
	}
	return faults
}

func TestReadFaults_InspectFails(t *testing.T) {
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		return mock.Result{Stderr: testutil.NoSuchContainer(args[1]), Err: testutil.ExitCode1(t)}
	}}
	st := &cluster.Status{Name: "dev", Nodes: []*cluster.NodeStatus{{Name: "worker-1.dev", Health: &cluster.NodeHealth{State: cluster.StateStopped}}}}
	assert.Empty(t, readFaults(t.Context(), docker.NewClient(m), "sind", "dev", st))
}

func TestUnixTime(t *testing.T) {
	assert.Equal(t, time.Unix(1761555737, 123456000), unixTime("1761555737.123456"))
	assert.Equal(t, time.Unix(1761555737, 0), unixTime("1761555737.x"))
	for _, s := range []string{"", "0", "n/a", "-5"} {
		assert.True(t, unixTime(s).IsZero(), s)
	}
}

func TestStatusReadsTheFaults(t *testing.T) {
	f := newStatusFixture(t)
	f.fake.set(func() {
		w := node("sind", "dev", "worker-1", "worker")
		w.State = "exited"
		f.fake.nodes["sind"] = append(f.fake.nodes["sind"], node("sind", "dev", "controller", "controller"), w)
	})
	// The fake answers the inspect of the nodes, which the status makes
	// of both and the faults of the stopped one alone.
	var cancel context.CancelFunc
	f.fake.exec.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] != "inspect" || !strings.HasPrefix(args[1], "sind-dev-") {
			return f.fake.call(args, stdin)
		}
		if len(args) == 4 && cancel != nil {
			cancel()
		}
		var infos []string
		for _, name := range args[1 : len(args)-2] {
			state, role := "running", "controller"
			if strings.HasSuffix(name, "-worker-1") {
				state, role = "exited", "worker"
			}
			infos = append(infos, `{"Id":"`+name+`","Name":"/`+name+`","State":{"Status":"`+state+`","ExitCode":143},"Config":{"Labels":{"sind.realm":"sind","sind.cluster":"dev","sind.role":"`+role+`"}}}`)
		}
		return mock.Result{Stdout: "[" + strings.Join(infos, ",") + "]"}
	}
	f.st.SetFaults("sind", "dev", map[string]store.Fault{"old": {}})
	f.watched.Watch([]store.Topic{devKey.Topic()})
	f.status.read(t.Context(), devKey)
	got, ok := f.st.Snapshot().Fault("sind", "dev", "worker-1")
	require.True(t, ok, "%v", f.st.Snapshot().Faults)
	assert.Equal(t, 143, got.Exit.Code)
	_, ok = f.st.Snapshot().Fault("sind", "dev", "old")
	assert.False(t, ok, "a read replaces the faults")

	// A read that is cancelled while it reads the faults stores none.
	f.st.SetFaults("sind", "dev", map[string]store.Fault{"old": {}})
	var ctx context.Context
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	f.status.read(ctx, devKey)
	_, ok = f.st.Snapshot().Fault("sind", "dev", "old")
	assert.True(t, ok)
}
