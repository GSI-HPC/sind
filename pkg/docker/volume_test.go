// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testVolumeName VolumeName = "sind-dev-config"

func TestVolumeLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itVolumeName("vol")
	n := string(name)

	if !rec.IsIntegration() {
		rec.AddResult(n+"\n", "", nil)                                                                                                      // create
		rec.AddResult("[{}]\n", "", nil)                                                                                                    // exists → true
		rec.AddResult(`{"Name":"`+n+`","Driver":"local","Mountpoint":"/var/lib/docker/volumes/`+n+`/_data","Scope":"local"}`+"\n", "", nil) // list
		rec.AddResult("", "", nil)                                                                                                          // list no matches
		rec.AddResult(n+"\n", "", nil)                                                                                                      // remove (ok)
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})                                                           // exists → false
		rec.AddResult("", "Error: No such volume\n", fmt.Errorf("exit status 1"))                                                           // remove again (error)
	}
	t.Cleanup(func() { _ = c.RemoveVolume(context.Background(), name) })

	// Create.
	err := c.CreateVolume(ctx, name, nil)
	require.NoError(t, err)

	// Exists → true.
	exists, err := c.VolumeExists(ctx, name)
	require.NoError(t, err)
	assert.True(t, exists)

	// List with match.
	entries, err := c.ListVolumes(ctx, "name="+n)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, name, entries[0].Name)

	// List with no matches.
	entries, err = c.ListVolumes(ctx, "name=sind-nonexistent-xyz")
	require.NoError(t, err)
	assert.Empty(t, entries)

	// Remove.
	err = c.RemoveVolume(ctx, name)
	require.NoError(t, err)

	// Exists → false.
	exists, err = c.VolumeExists(ctx, name)
	require.NoError(t, err)
	assert.False(t, exists)

	// Remove again → error.
	err = c.RemoveVolume(ctx, name)
	assert.Error(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestVolumeExists_True(t *testing.T) {
	var m MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	exists, err := c.VolumeExists(t.Context(), testVolumeName)
	require.NoError(t, err)
	assert.True(t, exists)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "inspect", string(testVolumeName)}, m.Calls[0].Args)
}

func TestVolumeExists_False(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such volume: "+string(testVolumeName)+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.VolumeExists(t.Context(), testVolumeName)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestVolumeExists_OtherError(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	exists, err := c.VolumeExists(t.Context(), testVolumeName)
	assert.Error(t, err)
	assert.False(t, exists)
}

func TestCreateVolume(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testVolumeName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.CreateVolume(t.Context(), testVolumeName, nil)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "create", string(testVolumeName)}, m.Calls[0].Args)
}

func TestCreateVolume_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: create volume failed\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.CreateVolume(t.Context(), testVolumeName, nil)
	assert.Error(t, err)
}

func TestRemoveVolume(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testVolumeName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveVolume(t.Context(), testVolumeName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "rm", string(testVolumeName)}, m.Calls[0].Args)
}

func TestRemoveVolume_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such volume: "+string(testVolumeName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveVolume(t.Context(), testVolumeName)
	assert.Error(t, err)
}

const volumeLsJSON = `{"Name":"sind-dev-config","Driver":"local","Mountpoint":"/var/lib/docker/volumes/sind-dev-config/_data","Scope":"local"}
{"Name":"sind-dev-munge","Driver":"local","Mountpoint":"/var/lib/docker/volumes/sind-dev-munge/_data","Scope":"local"}
{"Name":"sind-dev-data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/sind-dev-data/_data","Scope":"local"}`

func TestListVolumes(t *testing.T) {
	var m MockExecutor
	m.AddResult(volumeLsJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListVolumes(t.Context(), "name=sind-dev")
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

	entries, err := c.ListVolumes(t.Context(), "name=nonexistent")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListVolumes_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListVolumes(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing volume ls output")
}

func TestListVolumes_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListVolumes(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
}
