// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"strings"
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
	// The containers' diagnostics are read in parallel.
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch args[0] {
		case "ps":
			return mock.Result{Stdout: `{"ID":"a1","Names":"sind-dev-controller","State":"exited","Image":"img","Labels":""}
{"ID":"a2","Names":"sind-dev-worker-0","State":"exited","Image":"img","Labels":""}`}
		case "inspect":
			return mock.Result{Stdout: `[{"Id":"x","Name":"` + args[1] + `","State":{"Status":"exited","ExitCode":1,"OOMKilled":false},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`}
		case "logs":
			return mock.Result{Stdout: "error log line\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}}
	c := docker.NewClient(newBarrier(m, 2, "inspect"))

	logClusterDiagnostics(t.Context(), c, "sind", "dev")

	var calls []string
	for _, call := range m.Calls {
		calls = append(calls, strings.Join(call.Args[:2], " "))
	}
	assert.ElementsMatch(t, []string{"ps -a", "inspect sind-dev-controller", "inspect sind-dev-worker-0",
		"logs --tail", "logs --tail"}, calls, "list + 2*(inspect + logs)")
}
