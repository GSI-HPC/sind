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

// slurmInfoExec answers the commands of GetSlurmInfo on sind-dev-controller:
// sinfo with nodes and partitions, and cat of the files in files.
func slurmInfoExec(t *testing.T, nodes, partitions string, files map[string]string) *mock.Executor {
	t.Helper()
	return &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		require.Equal(t, []string{"exec", "sind-dev-controller"}, args[:2])
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

func TestGetSlurmInfoErrors(t *testing.T) {
	ok := "worker-0|idle|0/2/0/2|1024|all*|none\n"
	for name, m := range map[string]*mock.Executor{
		"no controller": {OnCall: func([]string, string) mock.Result {
			return mock.Result{Stderr: "Error response from daemon: container is not running\n", Err: testutil.ExitCode1(t)}
		}},
		"bad nodes":      slurmInfoExec(t, "garbage\n", "", nil),
		"bad partitions": slurmInfoExec(t, ok, "garbage\n", nil),
		"no partitions": {OnCall: func(args []string, _ string) mock.Result {
			if strings.Contains(strings.Join(args, " "), "--Node") {
				return mock.Result{Stdout: ok}
			}
			return mock.Result{Stderr: "slurm_load_partitions: Unable to contact slurm controller\n", Err: testutil.ExitCode1(t)}
		}},
	} {
		_, err := GetSlurmInfo(t.Context(), docker.NewClient(m), "sind", "dev")
		assert.Error(t, err, name)
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
	require.Error(t, err)
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
