// SPDX-License-Identifier: LGPL-3.0-or-later

package fakedocker

import (
	"errors"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemon_NetworkLifecycle(t *testing.T) {
	d := New(t)
	c := d.Client()

	id, err := c.CreateConfigOnlyNetwork(t.Context(), "sind-lock", docker.Labels{"a": "1", "b": "2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sind-lock"}, d.Networks())
	assert.Equal(t, &Network{ID: string(id), Labels: docker.Labels{"a": "1", "b": "2"}}, d.Network("sind-lock"))
	assert.True(t, d.HasID(string(id)))

	_, err = c.CreateConfigOnlyNetwork(t.Context(), "sind-lock", nil)
	require.ErrorContains(t, err, "already exists")

	n, err := c.InspectNetworkMeta(t.Context(), "sind-lock")
	require.NoError(t, err)
	assert.Equal(t, id, n.ID)
	assert.Equal(t, docker.Labels{"a": "1", "b": "2"}, n.Labels)
	assert.True(t, n.ConfigOnly)
	assert.True(t, n.Created.Equal(Created))

	require.NoError(t, c.RemoveNetworkByID(t.Context(), id))
	assert.Empty(t, d.Networks())
	assert.False(t, d.HasID(string(id)))

	_, err = c.InspectNetworkMeta(t.Context(), "sind-lock")
	assert.True(t, docker.IsNotFound(err), "%v", err)
	assert.True(t, docker.IsNotFound(c.RemoveNetworkByID(t.Context(), id)))
}

func TestDaemon_AddRemove(t *testing.T) {
	d := New(t)
	first := d.Add("a", nil)
	second := d.Add("b", docker.Labels{"x": "y"})
	assert.NotEqual(t, first, second)
	assert.Equal(t, []string{"a", "b"}, d.Networks())

	d.Remove("a")
	assert.Nil(t, d.Network("a"))
	assert.Equal(t, []string{"b"}, d.Networks())
}

func TestDaemon_Hook(t *testing.T) {
	d := New(t)
	d.SetHook(func(args []string) *mock.Result {
		if args[1] == "rm" {
			return &mock.Result{Err: errors.New("daemon gone")}
		}
		return nil
	})
	id := d.Add("a", nil)

	assert.EqualError(t, d.Client().RemoveNetworkByID(t.Context(), docker.NetworkID(id)), "daemon gone")
	assert.Equal(t, []string{"a"}, d.Networks())
}

func TestDaemon_UnexpectedCall(t *testing.T) {
	d := New(t)
	for _, args := range [][]string{{"ps"}, {"network", "ls", "-q"}} {
		r := d.OnCall(args, "")
		assert.ErrorContains(t, r.Err, "fake daemon: unexpected call", "%v", args)
	}
}
