// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
)

func TestLogContainerDiagnostics(t *testing.T) {
	inspect := func(status string, exitCode int, oom bool) mock.Result {
		return mock.Result{Stdout: fmt.Sprintf(`[{"Id":"abc","Name":"sind-dev-worker-0","State":{"Status":%q,"ExitCode":%d,"OOMKilled":%t}}]`, status, exitCode, oom)}
	}
	tests := []struct {
		name    string
		results []mock.Result
		want    []string // the calls' commands
	}{
		{"inspect error", []mock.Result{{Err: fmt.Errorf("no such container")}}, []string{"inspect"}},
		{"running skipped", []mock.Result{inspect("running", 0, false)}, []string{"inspect"}},
		{"exited with logs", []mock.Result{inspect("exited", 1, false), {Stdout: "slurmctld: fatal: Unable to process configuration file\n"}}, []string{"inspect", "logs"}},
		{"oom killed", []mock.Result{inspect("exited", 137, true), {}}, []string{"inspect", "logs"}},
		{"logs error", []mock.Result{inspect("exited", 1, false), {Err: fmt.Errorf("logs failed")}}, []string{"inspect", "logs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			for _, r := range tt.results {
				m.AddResult(r.Stdout, r.Stderr, r.Err)
			}

			logContainerDiagnostics(t.Context(), docker.NewClient(&m), "sind-dev-worker-0")

			got := make([]string, len(m.Calls))
			for i, c := range m.Calls {
				got[i] = c.Args[0]
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLogClusterDiagnostics_ListError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("list failed"))
	c := docker.NewClient(&m)

	logClusterDiagnostics(t.Context(), c, "sind", "dev")

	assert.Len(t, m.Calls, 1)
}

func TestLogClusterDiagnostics_InspectsEachContainer(t *testing.T) {
	var m mock.Executor
	// ListContainers returns two containers
	m.AddResult(`{"ID":"a1","Names":"sind-dev-controller","State":"exited","Image":"img","Labels":""}
{"ID":"a2","Names":"sind-dev-worker-0","State":"exited","Image":"img","Labels":""}`, "", nil)
	// Inspect + logs for controller
	m.AddResult(`[{"Id":"a1","Name":"sind-dev-controller","State":{"Status":"exited","ExitCode":1,"OOMKilled":false},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`, "", nil)
	m.AddResult("error log line\n", "", nil)
	// Inspect + logs for worker
	m.AddResult(`[{"Id":"a2","Name":"sind-dev-worker-0","State":{"Status":"exited","ExitCode":1,"OOMKilled":false},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`, "", nil)
	m.AddResult("worker error\n", "", nil)
	c := docker.NewClient(&m)

	logClusterDiagnostics(t.Context(), c, "sind", "dev")

	assert.Len(t, m.Calls, 5) // list + 2*(inspect + logs)
}
