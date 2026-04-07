// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
)

func TestLogContainerDiagnostics_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("no such container"))
	c := docker.NewClient(&m)

	logContainerDiagnostics(t.Context(), c, "sind-dev-worker-0")

	assert.Len(t, m.Calls, 1)
}

func TestLogContainerDiagnostics_RunningSkipped(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Id":"abc","Name":"sind-dev-worker-0","State":{"Status":"running","ExitCode":0,"OOMKilled":false},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`, "", nil)
	c := docker.NewClient(&m)

	logContainerDiagnostics(t.Context(), c, "sind-dev-worker-0")

	assert.Len(t, m.Calls, 1) // only inspect, no logs
}

func TestLogContainerDiagnostics_ExitedWithLogs(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Id":"abc","Name":"sind-dev-worker-0","State":{"Status":"exited","ExitCode":1,"OOMKilled":false},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`, "", nil)
	m.AddResult("slurmctld: fatal: Unable to process configuration file\n", "", nil) // logs
	c := docker.NewClient(&m)

	logContainerDiagnostics(t.Context(), c, "sind-dev-worker-0")

	assert.Len(t, m.Calls, 2)
	assert.Equal(t, "logs", m.Calls[1].Args[0])
}

func TestLogContainerDiagnostics_OOMKilled(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Id":"abc","Name":"sind-dev-worker-0","State":{"Status":"exited","ExitCode":137,"OOMKilled":true},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`, "", nil)
	m.AddResult("", "", nil) // logs (empty)
	c := docker.NewClient(&m)

	logContainerDiagnostics(t.Context(), c, "sind-dev-worker-0")

	assert.Len(t, m.Calls, 2)
}

func TestLogContainerDiagnostics_LogsError(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Id":"abc","Name":"sind-dev-worker-0","State":{"Status":"exited","ExitCode":1,"OOMKilled":false},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`, "", nil)
	m.AddResult("", "", fmt.Errorf("logs failed"))
	c := docker.NewClient(&m)

	logContainerDiagnostics(t.Context(), c, "sind-dev-worker-0")

	assert.Len(t, m.Calls, 2)
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
