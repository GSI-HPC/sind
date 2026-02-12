// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testVolumeName VolumeName = "sind-dev-config"

func TestCreateVolume(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testVolumeName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.CreateVolume(context.Background(), testVolumeName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "create", string(testVolumeName)}, m.Calls[0].Args)
}

func TestCreateVolume_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: create volume failed\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.CreateVolume(context.Background(), testVolumeName)
	assert.Error(t, err)
}

func TestRemoveVolume(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testVolumeName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveVolume(context.Background(), testVolumeName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "rm", string(testVolumeName)}, m.Calls[0].Args)
}

func TestRemoveVolume_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such volume: "+string(testVolumeName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveVolume(context.Background(), testVolumeName)
	assert.Error(t, err)
}

const volumeLsJSON = `{"Name":"sind-dev-config","Driver":"local","Mountpoint":"/var/lib/docker/volumes/sind-dev-config/_data","Scope":"local"}
{"Name":"sind-dev-munge","Driver":"local","Mountpoint":"/var/lib/docker/volumes/sind-dev-munge/_data","Scope":"local"}
{"Name":"sind-dev-data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/sind-dev-data/_data","Scope":"local"}`

func TestListVolumes(t *testing.T) {
	var m MockExecutor
	m.AddResult(volumeLsJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListVolumes(context.Background(), "name=sind-dev")
	require.NoError(t, err)
	require.Len(t, entries, 3)

	assert.Equal(t, VolumeName("sind-dev-config"), entries[0].Name)
	assert.Equal(t, "local", entries[0].Driver)
	assert.Equal(t, VolumeName("sind-dev-munge"), entries[1].Name)
	assert.Equal(t, VolumeName("sind-dev-data"), entries[2].Name)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "ls", "--format", "json", "--filter", "name=sind-dev"}, m.Calls[0].Args)
}

func TestListVolumes_Empty(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	entries, err := c.ListVolumes(context.Background(), "name=nonexistent")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListVolumes_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListVolumes(context.Background())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing volume ls output")
}

func TestListVolumes_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListVolumes(context.Background())
	assert.Error(t, err)
	assert.Nil(t, entries)
}
