// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPower_SubcommandsExist(t *testing.T) {
	subcmds := []string{"shutdown", "cut", "on", "reboot", "cycle", "freeze", "unfreeze"}

	for _, sub := range subcmds {
		t.Run(sub, func(t *testing.T) {
			cmd := NewRootCommand()
			c, _, err := cmd.Find([]string{"power", sub})
			require.NoError(t, err)
			assert.Contains(t, c.Use, "NODES")
		})
	}
}

func TestPower_RequiresArgs(t *testing.T) {
	subcmds := []string{"shutdown", "cut", "on", "reboot", "cycle", "freeze", "unfreeze"}

	for _, sub := range subcmds {
		t.Run(sub, func(t *testing.T) {
			_, _, err := executeCommand("power", sub)
			assert.Error(t, err)
		})
	}
}

// --- Integration ---

func TestPowerLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx := t.Context()
	realm := "it-pwr-" + testID
	cluster := "cli-pwr-" + testID

	ctrName := docker.ContainerName(realm + "-" + cluster + "-worker-0")

	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
	})

	_, err := c.RunContainer(ctx,
		"--name", string(ctrName),
		"--label", "sind.realm="+realm,
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=worker",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)

	node := "worker-0." + cluster

	// shutdown (stop)
	_, _, err = executeWithRealm(realm, "power", "shutdown", node)
	require.NoError(t, err)
	info, err := c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, "exited", info.Status)

	// on (start)
	_, _, err = executeWithRealm(realm, "power", "on", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// freeze (pause)
	_, _, err = executeWithRealm(realm, "power", "freeze", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, "paused", info.Status)

	// unfreeze (unpause)
	_, _, err = executeWithRealm(realm, "power", "unfreeze", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// reboot (stop + start)
	_, _, err = executeWithRealm(realm, "power", "reboot", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// cycle (kill + start)
	_, _, err = executeWithRealm(realm, "power", "cycle", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// cut (kill)
	_, _, err = executeWithRealm(realm, "power", "cut", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, ctrName)
	require.NoError(t, err)
	assert.NotEqual(t, "running", info.Status)
}
