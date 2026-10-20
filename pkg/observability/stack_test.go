// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager returns a Manager of realm sind on a fake docker, which
// waits briefly.
func newTestManager(t *testing.T) (*Manager, *fakeDocker) {
	f, c := newFakeDocker(t)
	m := NewManager(c, "sind")
	m.Interval = time.Millisecond
	m.ReadyTimeout = 200 * time.Millisecond
	return m, f
}

func TestEnsureStack_Creates(t *testing.T) {
	m, f := newTestManager(t)

	changed, err := m.EnsureStack(t.Context(), "172.30.255.254", "// alloy\n")
	require.NoError(t, err)
	assert.True(t, changed)

	for _, s := range Services {
		c := f.containers[string(ContainerName("sind", s))]
		require.NotNil(t, c, s)
		assert.Equal(t, docker.StateRunning, c.state)
		assert.Equal(t, m.Fingerprint(), c.labels[LabelVersion])
		assert.Equal(t, "sind-mesh", c.labels[docker.ComposeProjectLabel])
		assert.Equal(t, string(s), c.labels[docker.ComposeServiceLabel])
		assert.NotContains(t, c.labels, labelRealm, "a container with sind.realm would count as a cluster's")
		mem, _ := testutil.ArgValue(c.args, "--memory")
		assert.Equal(t, specs[s].memoryLimit, mem)

		labels := f.volumes[string(VolumeName("sind", s))]
		assert.Equal(t, map[string]string{labelRealm: "sind", docker.ComposeProjectLabel: "sind-mesh", docker.ComposeVolumeLabel: string(s)}, labels)
	}
	arg := func(s Service, flag string) string {
		v, _ := testutil.ArgValue(f.containers[string(ContainerName("sind", s))].args, flag)
		return v
	}
	assert.Equal(t, "172.30.255.253", arg(ServiceGrafana, "--ip"))
	assert.Equal(t, "172.30.255.252", arg(ServicePrometheus, "--ip"))
	assert.Equal(t, "172.30.255.251", arg(ServiceLoki, "--ip"))
	assert.Empty(t, arg(ServiceAlloy, "--ip"))
	assert.Equal(t, "type=volume,source=sind-grafana,target=/var/lib/grafana", arg(ServiceGrafana, "--mount"))
	assert.Equal(t, "type=volume,source=sind-prometheus,target=/prometheus", arg(ServicePrometheus, "--mount"))
	assert.Equal(t, "type=volume,source=sind-loki,target=/loki", arg(ServiceLoki, "--mount"))

	alloy := f.containers["sind-alloy"]
	assert.Equal(t, "// alloy\n", alloy.files[AlloyConfigPath])
	assert.Equal(t, []string{
		"type=volume,source=sind-alloy,target=/var/lib/alloy/data",
		"type=bind,source=/sys/fs/cgroup,target=/sys/fs/cgroup,readonly",
		"type=volume,source=sind-journal,target=/var/log/sind",
	}, testutil.ArgValues(alloy.args, "--mount"))
	cgroupns, _ := testutil.ArgValue(alloy.args, "--cgroupns")
	assert.Equal(t, "host", cgroupns)
	assert.Equal(t, alloyArgs, alloy.args[len(alloy.args)-len(alloyArgs):])
	assert.Equal(t, prometheusConfig, f.containers["sind-prometheus"].files[prometheusConfigPath])
	assert.Equal(t, lokiConfig, f.containers["sind-loki"].files[lokiConfigPath])
	grafana := f.containers["sind-grafana"]
	assert.Contains(t, grafana.files, DashboardsDir+"/slurm-cluster.json")
	assert.Contains(t, grafana.files, "/etc/grafana/provisioning/datasources/sind.yaml")
	assert.Equal(t, grafanaEnv, testutil.ArgValues(grafana.args, "--env"))
	assert.NotContains(t, grafana.args, "--pull")

	assert.Equal(t, []string{
		"http://sind-grafana:3000/api/health",
		"http://sind-prometheus:9090/-/ready",
		"http://sind-loki:3100/ready",
		"http://sind-alloy:12345/-/ready",
	}, f.curls())
}

func TestEnsureStack_WithoutPinnedAddress(t *testing.T) {
	m, f := newTestManager(t)
	m.Pull = true

	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	for _, c := range f.containers {
		_, pinned := testutil.ArgValue(c.args, "--ip")
		assert.False(t, pinned)
		pull, _ := testutil.ArgValue(c.args, "--pull")
		assert.Equal(t, "always", pull)
	}
}

func TestEnsureStack_KeepsRunningStack(t *testing.T) {
	m, _ := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)

	changed, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestEnsureStack_StartsStoppedStack(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	f.containers["sind-loki"].state = docker.StateExited
	f.containers["sind-loki"].files = map[string]string{}

	changed, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, docker.StateRunning, f.containers["sind-loki"].state)
	assert.Empty(t, f.containers["sind-loki"].files, "a container is configured only when created")
}

func TestEnsureStack_RecreatesOtherFingerprint(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	f.containers["sind-grafana"].labels[LabelVersion] = "older"
	f.containers["sind-grafana"].files = map[string]string{}

	changed, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, m.Fingerprint(), f.containers["sind-grafana"].labels[LabelVersion])
	assert.Contains(t, f.containers["sind-grafana"].files, homeDashboard)
	assert.Contains(t, f.volumes, "sind-grafana", "the volume is kept")
}

func TestEnsureStack_Errors(t *testing.T) {
	exit := func(t *testing.T) mock.Result { return mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)} }
	tests := []struct {
		name    string
		prepare func(*fakeDocker)
		fail    string
		wantErr string
	}{
		{name: "volume", fail: "volume create", wantErr: "creating prometheus volume: "},
		{name: "inspect", fail: "inspect sind-loki", wantErr: "checking loki container: "},
		{
			name:    "remove",
			prepare: func(f *fakeDocker) { f.containers["sind-alloy"].labels[LabelVersion] = "older" },
			fail:    "rm -f -v sind-alloy", wantErr: "removing alloy container: ",
		},
		{name: "create", fail: "create --name sind-loki", wantErr: "creating loki container: "},
		{name: "copy", fail: "cp - sind-grafana", wantErr: "writing grafana configuration: "},
		{
			name:    "start",
			prepare: func(f *fakeDocker) { f.containers["sind-alloy"].state = docker.StateExited },
			fail:    "start sind-alloy", wantErr: "starting alloy container: ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, f := newTestManager(t)
			if tt.prepare != nil {
				_, err := m.EnsureStack(t.Context(), "", "")
				require.NoError(t, err)
				tt.prepare(f)
			}
			f.fail[tt.fail] = exit(t)
			_, err := m.EnsureStack(t.Context(), "", "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestEnsureStack_NotReady(t *testing.T) {
	m, f := newTestManager(t)
	f.down["http://sind-loki:3100/ready"] = true

	_, err := m.EnsureStack(t.Context(), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "waiting for the monitoring stack: ")
	assert.Contains(t, err.Error(), "loki not ready")
}

func TestWaitReady_DefaultTimeout(t *testing.T) {
	m, _ := newTestManager(t)
	m.ReadyTimeout = 0
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := m.WaitReady(ctx)
	require.Error(t, err)
}

func TestServiceReady_Stopped(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	f.containers["sind-prometheus"].state = docker.StateExited

	err = m.ServiceReady(t.Context(), ServicePrometheus)
	var te *probe.TerminalError
	require.ErrorAs(t, err, &te)
}

func TestSync(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "first\n")
	require.NoError(t, err)

	require.NoError(t, m.Sync(t.Context(), "second\n"))
	assert.Equal(t, "second\n", f.containers["sind-alloy"].files[AlloyConfigPath])
	last := f.execs[len(f.execs)-1]
	assert.Equal(t, []string{"sind-grafana", "curl", "-fsS", "--max-time", "5", "-X", "POST", "-o", "/dev/null", "http://sind-alloy:12345/-/reload"}, last)

	f.down["http://sind-alloy:12345/-/reload"] = true
	err = m.Sync(t.Context(), "third\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reloading Alloy configuration: ")

	f.fail["cp - sind-alloy"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	err = m.Sync(t.Context(), "fourth\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing Alloy configuration: ")
}

func TestJournals(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)

	require.NoError(t, m.ResetJournals(t.Context(), "dev", "worker-0", "worker-1"))
	assert.Equal(t, []string{"sind-alloy", "sh", "-c", journalScript, "sh", "reset", "/var/log/sind/dev/worker-0", "/var/log/sind/dev/worker-1"}, f.execs[len(f.execs)-1])

	require.NoError(t, m.RemoveJournals(t.Context(), "dev", "worker-1"))
	assert.Equal(t, []string{"sind-alloy", "sh", "-c", journalScript, "sh", "remove", "/var/log/sind/dev/worker-1"}, f.execs[len(f.execs)-1])

	require.NoError(t, m.RemoveJournals(t.Context(), "dev"))
	assert.Equal(t, []string{"sind-alloy", "sh", "-c", journalScript, "sh", "remove", "/var/log/sind/dev"}, f.execs[len(f.execs)-1])

	f.containers["sind-alloy"].state = docker.StateExited
	err = m.ResetJournals(t.Context(), "dev", "worker-0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resetting journal directories: ")
	err = m.RemoveJournals(t.Context(), "dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing journal directories: ")
}

func TestRemove(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	delete(f.containers, "sind-loki")
	f.volumes["sind-journal"] = map[string]string{docker.ComposeProjectLabel: "sind-mesh"}
	f.containers["sind-dns"] = &fakeContainer{labels: map[string]string{docker.ComposeProjectLabel: "sind-mesh"}}
	f.volumes["sind-ssh-config"] = map[string]string{docker.ComposeProjectLabel: "sind-mesh"}

	require.NoError(t, m.Remove(t.Context()))
	assert.Equal(t, []string{"sind-dns"}, slices.Sorted(maps.Keys(f.containers)), "the mesh's containers stay")
	assert.Len(t, f.volumes, 6, "Remove keeps the volumes")

	delete(f.volumes, "sind-loki")
	require.NoError(t, m.RemoveVolumes(t.Context()))
	assert.Equal(t, []string{"sind-ssh-config"}, slices.Sorted(maps.Keys(f.volumes)), "the mesh's volumes stay")
}

func TestRemove_Gone(t *testing.T) {
	// A container or volume removed between the listing and its removal
	// counts as removed.
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	notFound := mock.Result{Stderr: testutil.NoSuchContainer("sind-alloy"), Err: testutil.ExitCode1(t)}
	f.fail["rm -f -v sind-alloy"] = notFound
	require.NoError(t, m.Remove(t.Context()))
	f.fail["volume rm sind-alloy"] = mock.Result{Stderr: testutil.NoSuchVolume("sind-alloy"), Err: testutil.ExitCode1(t)}
	require.NoError(t, m.RemoveVolumes(t.Context()))
}

func TestRemove_Errors(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	f.fail["rm -f -v sind-alloy"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	err = m.Remove(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing alloy container: ")

	f.fail["volume rm sind-grafana"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	err = m.RemoveVolumes(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume sind-grafana: ")

	f.fail["ps"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	err = m.Remove(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing the monitoring containers: ")

	f.fail["volume ls"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	err = m.RemoveVolumes(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing the monitoring volumes: ")
}

func TestStatus(t *testing.T) {
	m, f := newTestManager(t)

	statuses, found, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, found)
	require.Len(t, statuses, 4)
	assert.Equal(t, ServiceStatus{Service: ServiceGrafana, Container: "sind-grafana", Image: GrafanaImage, DNSName: "grafana.sind.sind", Port: "3000"}, statuses[0])

	_, err = m.EnsureStack(t.Context(), "172.30.255.254", "")
	require.NoError(t, err)
	f.down["http://sind-loki:3100/ready"] = true
	f.containers["sind-alloy"].state = docker.StateExited

	statuses, found, err = m.Status(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, ServiceStatus{
		Service: ServiceGrafana, Container: "sind-grafana", State: docker.StateRunning, Image: GrafanaImage,
		IP: "172.30.255.253", DNSName: "grafana.sind.sind", Port: "3000", Ready: true, Checked: true,
	}, statuses[0])
	assert.True(t, statuses[1].Ready)
	assert.False(t, statuses[2].Ready)
	assert.True(t, statuses[2].Checked)
	assert.Equal(t, ServiceStatus{Service: ServiceAlloy, Container: "sind-alloy", State: docker.StateExited, Image: AlloyImage, Port: "12345"}, statuses[3])

	// Without Grafana, nothing can be probed.
	f.containers["sind-grafana"].state = docker.StateExited
	statuses, _, err = m.Status(t.Context())
	require.NoError(t, err)
	for _, st := range statuses {
		assert.False(t, st.Ready, st.Service)
		assert.False(t, st.Checked, st.Service)
	}

	f.fail["inspect sind-grafana"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	_, _, err = m.Status(t.Context())
	require.ErrorContains(t, err, "inspecting the monitoring stack: ")

	f.fail["ps"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	_, _, err = m.Status(t.Context())
	require.ErrorContains(t, err, "listing the monitoring containers: ")
}

func TestFingerprint(t *testing.T) {
	a := NewManager(nil, "sind").Fingerprint()
	assert.Len(t, a, 16)
	assert.Equal(t, a, NewManager(nil, "sind").Fingerprint())
	assert.NotEqual(t, a, NewManager(nil, "other").Fingerprint(), "the datasources name the realm's containers")
	pulled := NewManager(nil, "sind")
	pulled.Pull = true
	assert.Equal(t, a, pulled.Fingerprint(), "a pull alone recreates nothing")
}

func TestURL(t *testing.T) {
	assert.Equal(t, "http://dev-loki:3100/ready", NewManager(nil, "dev").URL(ServiceLoki, "/ready"))
}

func TestStart(t *testing.T) {
	m, f := newTestManager(t)

	found, err := m.Start(t.Context())
	require.NoError(t, err)
	assert.False(t, found)

	_, err = m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)
	for _, c := range f.containers {
		c.state = docker.StateExited
	}
	found, err = m.Start(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
	for name, c := range f.containers {
		assert.Equal(t, docker.StateRunning, c.state, name)
	}

	// A running stack is only waited for.
	found, err = m.Start(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
}

func TestStart_Errors(t *testing.T) {
	m, f := newTestManager(t)
	_, err := m.EnsureStack(t.Context(), "", "")
	require.NoError(t, err)

	delete(f.containers, "sind-loki")
	found, err := m.Start(t.Context())
	require.Error(t, err)
	assert.True(t, found)
	assert.Equal(t, "the monitoring stack lacks sind-loki: creating a monitored cluster creates it again", err.Error())

	f.fail["inspect sind-prometheus"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	_, err = m.Start(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking prometheus container: ")
	delete(f.fail, "inspect sind-prometheus")

	f.containers["sind-alloy"].state = docker.StateExited
	f.fail["start sind-alloy"] = mock.Result{Stderr: "boom\n", Err: testutil.ExitCode1(t)}
	_, err = m.Start(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starting alloy container: ")
}
