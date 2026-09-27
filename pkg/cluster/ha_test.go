// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heartbeatOutput renders what `date +%s; od -A n -t u1 -v heartbeat` prints
// for a heartbeat written at written by the controller at index.
func heartbeatOutput(now, written, index uint64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d\n", now)
	for _, v := range []uint64{written, index} {
		for shift := 56; shift >= 0; shift -= 8 {
			fmt.Fprintf(&b, " %d", (v>>uint(shift))&0xff)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func isHeartbeatRead(args []string) bool {
	return len(args) > 4 && args[0] == "exec" && args[2] == "sh" && strings.Contains(args[4], "heartbeat")
}

func TestReadHeartbeat(t *testing.T) {
	var m mock.Executor
	m.AddResult(heartbeatOutput(1_700_000_010, 1_700_000_000, 1), "", nil)
	c := docker.NewClient(&m)

	hb, ok := readHeartbeat(t.Context(), c, "sind-dev-controller")

	require.True(t, ok)
	assert.Equal(t, heartbeat{index: 1, age: 10 * time.Second}, hb)
	assert.Equal(t, []string{"exec", "sind-dev-controller", "sh", "-c",
		"date +%s; od -A n -t u1 -v /var/spool/slurmctld/heartbeat"}, m.Calls[0].Args)
}

func TestReadHeartbeat_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		err    error
	}{
		{name: "exec fails", err: fmt.Errorf("exit status 1")},
		{name: "missing bytes", stdout: "1700000000\n 0 0 0\n"},
		{name: "bad clock", stdout: strings.Replace(heartbeatOutput(1, 1, 0), "1\n", "x\n", 1)},
		{name: "bad byte", stdout: strings.Replace(heartbeatOutput(1, 1, 0), " 0", " 300", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(tt.stdout, "", tt.err)
			c := docker.NewClient(&m)

			_, ok := readHeartbeat(t.Context(), c, "sind-dev-controller")
			assert.False(t, ok)
		})
	}
}

func TestHeartbeat_InControl(t *testing.T) {
	hb := heartbeat{index: 0, age: 5 * time.Second}
	assert.True(t, hb.inControl(0, true))
	assert.False(t, hb.inControl(1, true), "other index")
	assert.False(t, hb.inControl(0, false), "slurmctld down")
	assert.False(t, heartbeat{index: 0, age: 2 * time.Minute}.inControl(0, true), "stale")
}

// pairStatusOnCall serves GetStatus for a cluster with a controller pair and
// one worker. states maps short names to container states (default
// running); ping is the scontrol ping output; hb the heartbeat read result.
func pairStatusOnCall(t *testing.T, states map[string]string, ping string, hb mock.Result) func([]string, string) mock.Result {
	t.Helper()
	state := func(short string) string {
		if s, ok := states[short]; ok {
			return s
		}
		return "running"
	}
	return func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "ps":
			var entries []testutil.PsEntry
			for _, short := range []string{"controller", "controller-backup", "worker-0"} {
				role := "controller"
				if short == "worker-0" {
					role = "worker"
				}
				entries = append(entries, testutil.PsEntry{
					ID: short, Names: "sind-dev-" + short, State: state(short), Image: "img",
					Labels: "sind.cluster=dev,sind.role=" + role,
				})
			}
			return mock.Result{Stdout: testutil.NDJSON(entries...)}
		case args[0] == "inspect":
			return mock.Result{Stdout: statusInspectJSONBatch(args, func(name string) (string, string) {
				return state(strings.TrimPrefix(name, "sind-dev-")), "172.18.0.9"
			})}
		case isHeartbeatRead(args):
			return hb
		case args[0] == "exec" && args[2] == "systemctl":
			return fusedIsActiveResponse(t, args)
		case args[0] == "exec" && args[2] == "scontrol":
			return mock.Result{Stdout: ping}
		case args[0] == "network" || args[0] == "volume":
			return mock.Result{Stdout: "[{}]\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

func controllerHA(t *testing.T, status *Status) (primary, backup *HAStatus) {
	t.Helper()
	require.Len(t, status.Nodes, 3)
	assert.Nil(t, status.Nodes[2].Health.HA, "worker has no HA status")
	return status.Nodes[0].Health.HA, status.Nodes[1].Health.HA
}

func TestGetStatus_ControllerPair(t *testing.T) {
	bothUp := "Slurmctld(primary) at controller is UP\nSlurmctld(backup) at controller-backup is UP\n"
	fresh := func(index uint64) mock.Result {
		return mock.Result{Stdout: heartbeatOutput(1000, 995, index)}
	}

	t.Run("primary in control", func(t *testing.T) {
		var m mock.Executor
		m.OnCall = pairStatusOnCall(t, nil, bothUp, fresh(0))
		status, err := GetStatus(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

		require.NoError(t, err)
		primary, backup := controllerHA(t, status)
		assert.Equal(t, &HAStatus{Position: PositionPrimary, InControl: true}, primary)
		assert.Equal(t, &HAStatus{Position: PositionBackup, InControl: false}, backup)
	})

	t.Run("backup in control after failover", func(t *testing.T) {
		var m mock.Executor
		m.OnCall = pairStatusOnCall(t, map[string]string{"controller": "exited"},
			"Slurmctld(primary) at controller is DOWN\nSlurmctld(backup) at controller-backup is UP\n", fresh(1))
		status, err := GetStatus(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

		require.NoError(t, err)
		primary, backup := controllerHA(t, status)
		assert.Equal(t, &HAStatus{Position: PositionPrimary, InControl: false}, primary)
		assert.Equal(t, &HAStatus{Position: PositionBackup, InControl: true}, backup)
		assert.False(t, status.Nodes[0].Health.Services[probe.ServiceSlurmctld])
	})

	t.Run("no controller running", func(t *testing.T) {
		var m mock.Executor
		m.OnCall = pairStatusOnCall(t, map[string]string{"controller": "exited", "controller-backup": "exited"},
			"", fresh(0))
		status, err := GetStatus(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

		require.NoError(t, err)
		primary, backup := controllerHA(t, status)
		assert.False(t, primary.InControl)
		assert.False(t, backup.InControl)
		for _, c := range m.Calls {
			assert.False(t, isHeartbeatRead(c.Args), "no heartbeat read without a running controller")
		}
	})
}

func TestGetStatus_UnmanagedPairHasNoHA(t *testing.T) {
	var m mock.Executor
	base := pairStatusOnCall(t, nil, "", mock.Result{Stdout: heartbeatOutput(1000, 995, 0)})
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			var entries []testutil.PsEntry
			for _, short := range []string{"controller", "controller-backup", "worker-0"} {
				role := "controller"
				if short == "worker-0" {
					role = "worker"
				}
				entries = append(entries, testutil.PsEntry{
					ID: short, Names: "sind-dev-" + short, State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.managed=false,sind.role=" + role,
				})
			}
			return mock.Result{Stdout: testutil.NDJSON(entries...)}
		}
		return base(args, stdin)
	}
	status, err := GetStatus(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, status.Nodes, 3)
	for _, n := range status.Nodes {
		assert.Nil(t, n.Health.HA, n.Name)
	}
	for _, c := range m.Calls {
		assert.False(t, isHeartbeatRead(c.Args), "no heartbeat read on an unmanaged cluster")
	}
}

func TestGetStatus_SingleControllerHasNoHA(t *testing.T) {
	var m mock.Executor
	m.OnCall = fullStatusOnCall(t)
	status, err := GetStatus(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	for _, n := range status.Nodes {
		assert.Nil(t, n.Health.HA)
	}
}

// nodeHAOnCall serves GetNodeHealth for one controller of a possible pair.
// states maps short names to container states; a missing entry means the
// container does not exist.
func nodeHAOnCall(t *testing.T, states map[string]string, inspectErr error, hb mock.Result) func([]string, string) mock.Result {
	t.Helper()
	notFound := testutil.ExitCode1(t)
	return func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect":
			short := strings.TrimPrefix(args[1], "sind-dev-")
			if inspectErr != nil && short == ControllerBackupShortName {
				return mock.Result{Err: inspectErr}
			}
			s, ok := states[short]
			if !ok {
				return mock.Result{Stderr: "Error: No such object: " + args[1] + "\n", Err: notFound}
			}
			return mock.Result{Stdout: statusInspectJSON(args[1], s, "172.18.0.9")}
		case isHeartbeatRead(args):
			return hb
		case args[0] == "exec" && args[2] == "systemctl":
			return fusedIsActiveResponse(t, args)
		case args[0] == "exec" && args[2] == "scontrol":
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\nSlurmctld(backup) at controller-backup is UP\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

func TestGetNodeHealth_UnmanagedControllerHasNoHA(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "inspect" && args[1] == "sind-dev-controller-backup":
			return mock.Result{Stdout: "[" + statusInspectEntryLabels(args[1], "running", "172.18.0.9",
				docker.Labels{LabelManaged: "false"}) + "]"}
		case args[0] == "exec" && args[2] == "systemctl":
			return fusedIsActiveResponse(t, args)
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}

	health, err := GetNodeHealth(t.Context(), docker.NewClient(&m), "sind-dev-controller-backup",
		config.RoleController, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Nil(t, health.HA)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true}, health.Services)
	assert.Len(t, m.Calls, 2, "no partner inspect, scontrol ping or heartbeat read")
}

func TestGetNodeHealth_ControllerHA(t *testing.T) {
	fresh := mock.Result{Stdout: heartbeatOutput(1000, 995, 1)}
	tests := []struct {
		name       string
		node       string
		states     map[string]string
		inspectErr error
		want       *HAStatus
		wantErr    string
		readFrom   string
	}{
		{
			name:   "single controller",
			node:   "controller",
			states: map[string]string{"controller": "running"},
		},
		{
			name:     "backup in control",
			node:     "controller-backup",
			states:   map[string]string{"controller": "running", "controller-backup": "running"},
			want:     &HAStatus{Position: PositionBackup, InControl: true},
			readFrom: "sind-dev-controller-backup",
		},
		{
			name:     "primary while backup in control",
			node:     "controller",
			states:   map[string]string{"controller": "running", "controller-backup": "running"},
			want:     &HAStatus{Position: PositionPrimary},
			readFrom: "sind-dev-controller",
		},
		{
			name:     "stopped primary reads heartbeat from backup",
			node:     "controller",
			states:   map[string]string{"controller": "exited", "controller-backup": "running"},
			want:     &HAStatus{Position: PositionPrimary},
			readFrom: "sind-dev-controller-backup",
		},
		{
			name:   "stopped backup without primary",
			node:   "controller-backup",
			states: map[string]string{"controller-backup": "exited"},
			want:   &HAStatus{Position: PositionBackup},
		},
		{
			name:       "partner inspect fails",
			node:       "controller",
			states:     map[string]string{"controller": "running"},
			inspectErr: fmt.Errorf("daemon unreachable"),
			wantErr:    "inspecting controller-backup",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.OnCall = nodeHAOnCall(t, tt.states, tt.inspectErr, fresh)
			c := docker.NewClient(&m)

			health, err := GetNodeHealth(t.Context(), c, "sind-dev-"+tt.node, config.RoleController, mesh.DefaultRealm, "dev")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, health.HA)

			var readFrom string
			for _, call := range m.Calls {
				if isHeartbeatRead(call.Args) {
					readFrom = call.Args[1]
				}
			}
			assert.Equal(t, tt.readFrom, readFrom)
		})
	}
}
