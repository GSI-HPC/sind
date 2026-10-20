// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stackFirst answers a call with f, else with base.
func stackFirst(f *stackFake, base func([]string, string) mock.Result) func([]string, string) mock.Result {
	return func(args []string, stdin string) mock.Result {
		if r, ok := f.handle(args, stdin); ok {
			return r
		}
		return base(args, stdin)
	}
}

// failNth fails the n-th call (from 1) whose joined arguments start with
// prefix, and answers every other call with base.
func failNth(t *testing.T, prefix string, n int, base func([]string, string) mock.Result) func([]string, string) mock.Result {
	seen := 0
	return func(args []string, stdin string) mock.Result {
		if strings.HasPrefix(strings.Join(args, " "), prefix) {
			seen++
			if seen == n {
				return mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
			}
		}
		return base(args, stdin)
	}
}

// --- WorkerAdd and WorkerRemove ---

// monitoredController answers the controller's docker inspect with a
// controller that mounts the journal volume.
func monitoredController(t *testing.T, base func([]string, string) mock.Result) func([]string, string) mock.Result {
	return func(args []string, stdin string) mock.Result {
		if len(args) > 1 && args[0] == "inspect" && args[1] == "sind-dev-controller" {
			var out []map[string]any
			require.NoError(t, json.Unmarshal([]byte(nodeInspectJSON(t, "sind-dev-controller", nodeInspect{
				Image: testControllerImageID, Labels: testControllerLabels(),
				Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"},
			})), &out))
			out[0]["Mounts"] = []map[string]string{{"Type": "volume", "Name": "sind-journal", "Destination": "/var/log/journal"}}
			b, _ := json.Marshal(out)
			return mock.Result{Stdout: string(b)}
		}
		return base(args, stdin)
	}
}

// addMonitoredWorker runs WorkerAdd of one worker on a monitored cluster
// with the realm's stack in f, the calls answered by onCall wrapped
// around the cluster's.
func addMonitoredWorker(t *testing.T, f *stackFake, wrap func(func([]string, string) mock.Result) func([]string, string) mock.Result) (*mock.Executor, error) {
	t.Helper()
	pipes := &mock.Pipes{}
	t.Cleanup(pipes.CloseAll)
	base := monitoredController(t, stackFirst(f, workerAddOnCall(t)))
	if wrap != nil {
		base = wrap(base)
	}
	m := &mock.Executor{OnCall: base, OnStart: pipes.OnStart}
	client := docker.NewClient(m)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := WorkerAdd(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), WorkerAddOptions{
		ClusterName: "dev", Count: 1, CPUs: 2, Memory: "2g", TmpSize: "1g",
	}, time.Millisecond)
	return m, err
}

func TestWorkerAdd_Monitored(t *testing.T) {
	f := newStackFake(t).withStack()
	m, err := addMonitoredWorker(t, f, nil)
	require.NoError(t, err)

	args, ok := createArgs(m.Calls, "sind-dev-worker-1")
	require.True(t, ok)
	assert.Contains(t, args, "type=volume,source=sind-journal,target=/var/log/journal,volume-subpath=dev/worker-1")
	assert.Contains(t, args, "sind.monitoring=true")
	assert.Equal(t, 1, countCalls(m.Calls, "exec", "sind-alloy", "sh"))
	assert.Equal(t, 2, countCalls(m.Calls, "cp", "-", "sind-alloy:/etc/alloy"))
	assert.True(t, f.called("run --rm --network none --entrypoint /bin/sh sha256:f00d"))
}

func TestWorkerAdd_MonitoredUnsupportedImage(t *testing.T) {
	f := newStackFake(t).withStack()
	f.version = "25.11.2"
	m, err := addMonitoredWorker(t, f, nil)
	require.ErrorIs(t, err, ErrImageUnsupported)
	_, created := createArgs(m.Calls, "sind-dev-worker-1")
	assert.False(t, created)

	f.fail["run"] = mock.Result{Err: testutil.ExitCode1(t)}
	_, err = addMonitoredWorker(t, f, nil)
	require.ErrorContains(t, err, "probing image")
}

func TestWorkerAdd_MonitoredStackErrors(t *testing.T) {
	for _, prefix := range []string{"network inspect sind-mesh", "inspect sind-grafana sind-prometheus sind-loki", "exec sind-alloy sh"} {
		t.Run(prefix, func(t *testing.T) {
			f := newStackFake(t).withStack()
			f.fail[prefix] = mock.Result{Err: testutil.ExitCode1(t)}
			m, err := addMonitoredWorker(t, f, nil)
			require.Error(t, err)
			_, created := createArgs(m.Calls, "sind-dev-worker-1")
			assert.False(t, created)
		})
	}
}

func TestWorkerAdd_MonitoredSyncFails(t *testing.T) {
	// The second sync, which adds the workers, fails: they go again, and
	// the stack forgets them.
	f := newStackFake(t).withStack()
	m, err := addMonitoredWorker(t, f, func(base func([]string, string) mock.Result) func([]string, string) mock.Result {
		return failNth(t, "info", 2, base)
	})
	require.ErrorContains(t, err, "adding the workers to the monitoring stack")
	assert.Positive(t, countCalls(m.Calls, "rm", "-f", "-v", "sind-dev-worker-1"))
	var removes int
	for _, c := range m.Calls {
		if c.Args[0] == "exec" && c.Args[1] == "sind-alloy" && c.Args[len(c.Args)-2] == "remove" && c.Args[len(c.Args)-1] == "/var/log/sind/dev/worker-1" {
			removes++
		}
	}
	assert.Equal(t, 1, removes)
}

func TestWorkerRemove_Monitored(t *testing.T) {
	nodesConf := "NodeName=worker-0 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"NodeName=worker-1 CPUs=2 RealMemory=2048 State=UNKNOWN\n" +
		"PartitionName=all Nodes=worker-0,worker-1 Default=YES MaxTime=INFINITE State=UP\n"
	f := newStackFake(t).withStack()
	f.nodes = []docker.ContainerListEntry{monitoredNode("dev", "worker-0", "worker"), monitoredNode("dev", "worker-1", "worker")}
	m := &mock.Executor{OnCall: stackFirst(f, workerRemoveOnCall(t, nodesConf))}
	client := docker.NewClient(m)

	require.NoError(t, WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"}))
	last := m.Calls[len(m.Calls)-1].Args
	assert.Equal(t, []string{"exec", "sind-alloy", "sh"}, last[:3])
	assert.Equal(t, "/var/log/sind/dev/worker-1", last[len(last)-1])

	// The workers stay known to the stack while their containers do.
	m = &mock.Executor{OnCall: failNth(t, "rm -f -v sind-dev-worker-1", 1, stackFirst(f, workerRemoveOnCall(t, nodesConf)))}
	client = docker.NewClient(m)
	require.Error(t, WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"}))
	assert.Zero(t, countCalls(m.Calls, "exec", "sind-alloy"))

	f.fail["ps"] = mock.Result{Err: testutil.ExitCode1(t)}
	require.ErrorContains(t, WorkerRemove(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-1"}), "listing monitored nodes")
}

// --- PowerOn ---

func TestPower_On_Monitoring(t *testing.T) {
	var written string
	f := newStackFake(t).withStack().stopped()
	m := &mock.Executor{OnCall: powerOnCall(t, func(args []string, stdin string) (mock.Result, bool) {
		if r, ok := f.handle(args, stdin); ok {
			return r, true
		}
		if args[0] == "inspect" && args[1] == "sind-dev-worker-0" {
			return mock.Result{Stdout: startedNodesJSON("10.0.9.2")}, true
		}
		return meshCall(t, nil, &written)(args, stdin)
	})}
	client := docker.NewClient(m)

	require.NoError(t, PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"}))
	assert.Equal(t, docker.StateRunning, f.containers["sind-grafana"].state)
	assert.Contains(t, written, "grafana.sind.sind")

	f.stopped()
	f.fail["start sind-loki"] = mock.Result{Err: testutil.ExitCode1(t)}
	err := PowerOn(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev", []string{"worker-0"})
	require.ErrorContains(t, err, "starting the monitoring stack")
}

// --- Delete ---

func TestCleanupRealm(t *testing.T) {
	f := newStackFake(t).withStack()
	realm := &realmFake{t: t, clusters: map[string][]string{}}
	m := &mock.Executor{OnCall: stackFirst(f, realm.onCall)}
	client := docker.NewClient(m)

	require.NoError(t, cleanupRealm(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm)))
	assert.Empty(t, f.containers)
	assert.Empty(t, f.volumes)
	assert.Less(t, indexOf(removed(m), "sind-grafana"), indexOf(removed(m), "sind-mesh"))
	assert.Less(t, indexOf(removed(m), "sind-mesh"), indexOf(removed(m), "sind-journal"))

	for _, prefix := range []string{"ps -a --no-trunc --format json --filter label=com.docker.compose.project", "volume ls --format json --filter label=com.docker.compose.project"} {
		f := newStackFake(t).withStack()
		f.fail[prefix] = mock.Result{Err: testutil.ExitCode1(t)}
		m := &mock.Executor{OnCall: stackFirst(f, realm.onCall)}
		client := docker.NewClient(m)
		require.Error(t, cleanupRealm(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm)), prefix)
	}

	// A failed mesh cleanup leaves the volumes.
	f = newStackFake(t).withStack()
	realm = &realmFake{t: t, clusters: map[string][]string{}, fail: func(args []string) bool { return args[0] == "network" && args[1] == "rm" }}
	m = &mock.Executor{OnCall: stackFirst(f, realm.onCall)}
	client = docker.NewClient(m)
	require.Error(t, cleanupRealm(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm)))
	assert.NotEmpty(t, f.volumes)
}

func TestDelete_Monitored(t *testing.T) {
	f := newStackFake(t).withStack()
	f.nodes = []docker.ContainerListEntry{monitoredNode("dev", "controller", "controller")}
	base := deleteOnCall(t, testutil.ExitCode1(t), "dev", deleteOnCallOpts{
		containers:    []testutil.PsEntry{{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"}},
		networkExists: true,
		volumes:       []string{"config", "munge", "data"},
		otherClusters: true,
	})
	m := &mock.Executor{OnCall: stackFirst(f, base)}
	client := docker.NewClient(m)

	require.NoError(t, Delete(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev"))
	assert.Len(t, f.containers, 4, "another cluster keeps the stack")
	last := m.Calls[len(m.Calls)-1].Args
	assert.Equal(t, []string{"exec", "sind-alloy", "sh"}, last[:3])
	assert.Equal(t, "/var/log/sind/dev", last[len(last)-1])

	f.fail["ps -a --no-trunc --format json --filter label=sind.realm=sind --filter label=sind.role --filter volume="] = mock.Result{Err: testutil.ExitCode1(t)}
	require.ErrorContains(t, Delete(t.Context(), client, mesh.NewManager(client, mesh.DefaultRealm), "dev"), "listing monitored nodes")
}

// --- Status ---

// meshHealthOnCall answers GetNetworkHealth's inspects of a running mesh
// and cluster network.
func meshHealthOnCall(t *testing.T) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "network" && args[1] == "inspect":
			return mock.Result{Stdout: netInspect(args[2], "172.19.0.0/16", "172.19.0.1")}
		case args[0] == "inspect":
			return mock.Result{Stdout: meshContainerInspect(args[1], "running")}
		}
		t.Errorf("unexpected call %v", args)
		return mock.Result{Err: testutil.ExitCode1(t)}
	}
}

func TestGetNetworkHealth_MonitoringStack(t *testing.T) {
	f := newStackFake(t).withStack()
	f.containers["sind-alloy"].state = docker.StateExited
	client := docker.NewClient(&mock.Executor{OnCall: stackFirst(f, meshHealthOnCall(t))})

	health, err := GetNetworkHealth(t.Context(), client, "sind", "dev")
	require.NoError(t, err)
	ready := func(b *bool) bool { require.NotNil(t, b); return *b }
	assert.True(t, ready(health.Grafana))
	assert.True(t, ready(health.Prometheus))
	assert.True(t, ready(health.Loki))
	assert.False(t, ready(health.Alloy))
	assert.Equal(t, []*bool{health.Grafana, health.Prometheus, health.Loki, health.Alloy}, health.StackHealth())

	b, err := json.Marshal(health)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"grafana_ok":true`)
	assert.Contains(t, string(b), `"alloy_ok":false`)

	// Without Grafana to check them, running services count as healthy.
	f.containers["sind-grafana"].state = docker.StateExited
	health, err = GetNetworkHealth(t.Context(), client, "sind", "dev")
	require.NoError(t, err)
	assert.False(t, ready(health.Grafana))
	assert.True(t, ready(health.Prometheus))
	assert.True(t, ready(health.Loki))
	assert.False(t, ready(health.Alloy))

	f.fail["ps"] = mock.Result{Err: testutil.ExitCode1(t)}
	_, err = GetNetworkHealth(t.Context(), client, "sind", "dev")
	require.ErrorContains(t, err, "listing the monitoring containers")
}

func TestGetNetworkHealth_NoStackOmitsFields(t *testing.T) {
	f := newStackFake(t)
	client := docker.NewClient(&mock.Executor{OnCall: stackFirst(f, meshHealthOnCall(t))})
	health, err := GetNetworkHealth(t.Context(), client, "sind", "dev")
	require.NoError(t, err)
	b, err := json.Marshal(health)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "grafana_ok")
}

// monitoredInfo returns the docker inspect result of a node of cluster dev
// that mounts its journal directory, with the subpath in its host
// configuration when subpath is not empty.
func monitoredInfo(shortName, subpath string) *docker.ContainerInfo {
	info := &docker.ContainerInfo{
		Name:   ContainerName("sind", "dev", shortName),
		Mounts: []docker.Mount{{Type: docker.MountVolume, Name: "sind-journal", Destination: "/var/log/journal"}},
	}
	if subpath != "" {
		info.HostConfig.Mounts = []docker.HostMount{
			{Type: docker.MountVolume, Source: "sind-dev-config", Target: "/etc/slurm"},
			{Type: docker.MountVolume, Source: "sind-journal", Target: "/var/log/journal", VolumeOptions: docker.VolumeOptions{Subpath: subpath}},
		}
	}
	return info
}

func TestGetMountPoints_Journal(t *testing.T) {
	f := newStackFake(t).withStack()
	base := func(args []string, _ string) mock.Result {
		if args[0] == "volume" && args[1] == "ls" {
			return volumeLs("sind-dev-config", "sind-dev-munge", "sind-dev-data")
		}
		t.Errorf("unexpected call %v", args)
		return mock.Result{Err: testutil.ExitCode1(t)}
	}
	client := docker.NewClient(&mock.Executor{OnCall: stackFirst(f, base)})

	mounts, err := GetMountPoints(t.Context(), client, "sind", "dev", []*docker.ContainerInfo{{Name: "sind-dev-db"}, monitoredInfo("controller", "dev/controller")})
	require.NoError(t, err)
	assert.Equal(t, MountPoint{Path: "/var/log/journal", Source: "sind-journal", Subpath: "dev", Type: config.StorageVolume, OK: true}, mounts[len(mounts)-1])

	// Without the subpath, the cluster's directory is assumed.
	delete(f.volumes, "sind-journal")
	mounts, err = GetMountPoints(t.Context(), client, "sind", "dev", []*docker.ContainerInfo{monitoredInfo("controller", "")})
	require.NoError(t, err)
	assert.Equal(t, MountPoint{Path: "/var/log/journal", Source: "sind-journal", Subpath: "dev", Type: config.StorageVolume}, mounts[len(mounts)-1])

	f.fail["volume inspect sind-journal"] = mock.Result{Stderr: "permission denied\n", Err: testutil.ExitCode1(t)}
	_, err = GetMountPoints(t.Context(), client, "sind", "dev", []*docker.ContainerInfo{monitoredInfo("controller", "")})
	require.ErrorContains(t, err, "checking volumes")
}

func TestGetStatus_Monitoring(t *testing.T) {
	f := newStackFake(t).withStack()
	base := fullStatusOnCall(t)
	m := &mock.Executor{OnCall: stackFirst(f, func(args []string, stdin string) mock.Result {
		r := base(args, stdin)
		if args[0] != "inspect" {
			return r
		}
		var infos []map[string]any
		require.NoError(t, json.Unmarshal([]byte(r.Stdout), &infos))
		for _, info := range infos {
			info["Mounts"] = []map[string]string{{"Type": "volume", "Name": "sind-journal", "Destination": "/var/log/journal"}}
		}
		b, _ := json.Marshal(infos)
		return mock.Result{Stdout: string(b)}
	})}
	status, err := GetStatus(t.Context(), docker.NewClient(m), "sind", "dev")
	require.NoError(t, err)
	assert.True(t, status.Monitoring)
	assert.Equal(t, "sind-journal", status.Mounts[len(status.Mounts)-1].Source)
	require.NotNil(t, status.Network.Grafana)
}
