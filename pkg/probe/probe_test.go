// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContainer docker.ContainerName = "sind-dev-controller"

func inspectJSON(status string) string {
	return fmt.Sprintf(`[{
  "Id": "abc123",
  "Name": "/%s",
  "State": {"Status": %q},
  "Config": {"Labels": {}},
  "NetworkSettings": {"Networks": {}}
}]`, testContainer, status)
}

func TestCheckContainerRunning(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	err := CheckContainerRunning(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", string(testContainer)}, m.Calls[0].Args)
}

func TestCheckContainerRunning_NotRunning(t *testing.T) {
	for _, status := range []string{"exited", "created", "paused", "dead"} {
		t.Run(status, func(t *testing.T) {
			var m docker.MockExecutor
			m.AddResult(inspectJSON(status), "", nil)
			c := docker.NewClient(&m)

			err := CheckContainerRunning(context.Background(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), status)
			assert.Contains(t, err.Error(), "expected running")
		})
	}
}

func TestCheckContainerRunning_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := CheckContainerRunning(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting container")
}
