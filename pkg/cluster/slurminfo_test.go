// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slurmInfoPs lists the controllers of cluster dev with their states.
func slurmInfoPs(primary, backup string) string {
	entries := []testutil.PsEntry{{ID: "c1", Names: "sind-dev-controller", State: primary,
		Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller"}}
	if backup != "" {
		entries = append(entries, testutil.PsEntry{ID: "c2", Names: "sind-dev-controller-backup", State: backup,
			Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller"})
	}
	return testutil.NDJSON(entries...)
}

// slurmInfoExec answers the commands of GetSlurmInfo: the listing of the
// cluster's containers, and on sind-dev-controller sinfo with nodes and
// partitions, and cat of the files in files.
func slurmInfoExec(t *testing.T, nodes, partitions string, files map[string]string) *mock.Executor {
	t.Helper()
	return slurmInfoExecOn(t, "sind-dev-controller", slurmInfoPs("running", ""), nodes, partitions, files)
}

// slurmInfoExecOn is slurmInfoExec with the listing ps, which must lead
// GetSlurmInfo to controller.
func slurmInfoExecOn(t *testing.T, controller, ps, nodes, partitions string, files map[string]string) *mock.Executor {
	t.Helper()
	return &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			assert.Equal(t, []string{"label=sind.realm=sind", "label=sind.cluster=dev"}, testutil.ArgValues(args, "--filter"))
			return mock.Result{Stdout: ps}
		}
		require.Equal(t, []string{"exec", controller}, args[:2])
		cmd := strings.Join(args[2:], " ")
		switch {
		case cmd == strings.Join(slurm.SinfoNodesCommand, " "):
			return mock.Result{Stdout: nodes}
		case cmd == strings.Join(slurm.SinfoPartitionsCommand, " "):
			return mock.Result{Stdout: partitions}
		case args[2] == "cat":
			if c, ok := files[args[3]]; ok {
				return mock.Result{Stdout: c}
			}
		}
		return mock.Result{Stderr: "no such file\n", Err: testutil.ExitCode1(t)}
	}}
}

func TestGetSlurmInfo(t *testing.T) {
	m := slurmInfoExec(t, "worker-0|idle|0/2/0/2|1024|all*|none\n", "all*|up|infinite|1|worker-0\n",
		map[string]string{slurm.SlurmConfPath: "ClusterName=dev\n"})
	info, err := GetSlurmInfo(t.Context(), docker.NewClient(m), "sind", "dev")
	require.NoError(t, err)
	assert.Equal(t, &SlurmInfo{
		Nodes:      []slurm.NodeInfo{{Name: "worker-0", State: "idle", CPUs: 2, MemoryMB: 1024, Partitions: []string{"all"}}},
		Partitions: []slurm.PartitionInfo{{Name: "all", Default: true, State: "up", TimeLimit: "infinite", Nodes: 1, NodeList: "worker-0"}},
		Files:      []SlurmFile{{Path: slurm.SlurmConfPath, Content: "ClusterName=dev\n"}},
	}, info, "sind-nodes.conf could not be read")
}

func TestGetSlurmInfoAfterAFailover(t *testing.T) {
	m := slurmInfoExecOn(t, "sind-dev-controller-backup", slurmInfoPs("exited", "running"),
		"worker-0|idle|0/2/0/2|1024|all*|none\n", "all*|up|infinite|1|worker-0\n",
		map[string]string{slurm.SlurmConfPath: "ClusterName=dev\n"})
	info, err := GetSlurmInfo(t.Context(), docker.NewClient(m), "sind", "dev")
	require.NoError(t, err, "the backup is in control while the primary is stopped")
	assert.Len(t, info.Nodes, 1)
	assert.Len(t, info.Files, 1)
}

func TestGetSlurmInfoWithoutAController(t *testing.T) {
	m := slurmInfoExecOn(t, "sind-dev-controller", "", "", "", nil)
	_, err := GetSlurmInfo(t.Context(), docker.NewClient(m), "sind", "dev")
	require.NoError(t, err, "a listing without controllers tries the primary")
}

func TestGetSlurmInfoErrors(t *testing.T) {
	ok := "worker-0|idle|0/2/0/2|1024|all*|none\n"
	for name, tc := range map[string]struct {
		m    *mock.Executor
		want string
	}{
		"listing fails": {&mock.Executor{OnCall: func([]string, string) mock.Result {
			return mock.Result{Stderr: "Cannot connect to the Docker daemon\n", Err: testutil.ExitCode1(t)}
		}}, "listing containers: Docker is not reachable: Cannot connect to the Docker daemon"},
		"no controller": {&mock.Executor{OnCall: func(args []string, _ string) mock.Result {
			if args[0] == "ps" {
				return mock.Result{Stdout: slurmInfoPs("exited", "")}
			}
			return mock.Result{Stderr: "Error response from daemon: container is not running\n", Err: testutil.ExitCode1(t)}
		}}, "sinfo on the controller: exit status 1: Error response from daemon: container is not running"},
		"bad nodes":      {slurmInfoExec(t, "garbage\n", "", nil), `sinfo node line "garbage": want 6 fields, got 1`},
		"bad partitions": {slurmInfoExec(t, ok, "garbage\n", nil), `sinfo partition line "garbage": want 5 fields, got 1`},
		"no partitions": {&mock.Executor{OnCall: func(args []string, _ string) mock.Result {
			if args[0] == "ps" {
				return mock.Result{Stdout: slurmInfoPs("running", "")}
			}
			if strings.Contains(strings.Join(args, " "), "--Node") {
				return mock.Result{Stdout: ok}
			}
			return mock.Result{Stderr: "slurm_load_partitions: Unable to contact slurm controller\n", Err: testutil.ExitCode1(t)}
		}}, "sinfo on the controller: exit status 1: slurm_load_partitions: Unable to contact slurm controller"},
	} {
		info, err := GetSlurmInfo(t.Context(), docker.NewClient(tc.m), "sind", "dev")
		assert.ErrorContains(t, err, tc.want, name)
		if name == "listing fails" || name == "no controller" {
			assert.Nil(t, info, name)
		}
	}
}

func TestGetSlurmInfoKeepsTheFilesWhenSinfoFails(t *testing.T) {
	m := slurmInfoExec(t, "", "", map[string]string{slurm.SlurmConfPath: "ClusterName=dev\n"})
	m.OnCall = func(next func([]string, string) mock.Result) func([]string, string) mock.Result {
		return func(args []string, stdin string) mock.Result {
			if args[2] == "sinfo" {
				return mock.Result{Stderr: "slurm_load_node: Unable to contact slurm controller\n", Err: testutil.ExitCode1(t)}
			}
			return next(args, stdin)
		}
	}(m.OnCall)
	info, err := GetSlurmInfo(t.Context(), docker.NewClient(m), "sind", "dev")
	require.ErrorContains(t, err, "slurm_load_node: Unable to contact slurm controller")
	assert.Equal(t, &SlurmInfo{Files: []SlurmFile{{Path: slurm.SlurmConfPath, Content: "ClusterName=dev\n"}}}, info,
		"cat reads the files without slurmctld")
}

// hangingSinfo runs sinfo until its context ends, as against a slurmctld
// that does not answer.
type hangingSinfo struct {
	*mock.Executor
}

func (h hangingSinfo) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if len(args) > 2 && args[2] == "sinfo" {
		<-ctx.Done()
		return "", "", errors.New("signal: killed")
	}
	return h.Executor.Run(ctx, name, args...)
}

func TestGetSlurmInfoTimesOut(t *testing.T) {
	saved := slurmInfoTimeout
	slurmInfoTimeout = 20 * time.Millisecond
	t.Cleanup(func() { slurmInfoTimeout = saved })

	m := slurmInfoExec(t, "", "", map[string]string{slurm.SlurmConfPath: "ClusterName=dev\n"})
	info, err := GetSlurmInfo(t.Context(), docker.NewClient(hangingSinfo{m}), "sind", "dev")
	require.EqualError(t, err, "sinfo on the controller did not answer within 20ms")
	assert.Len(t, info.Files, 1)
}
