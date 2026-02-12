// SPDX-License-Identifier: LGPL-3.0-or-later

package ssh

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- InjectPublicKey ---

func TestInjectPublicKey(t *testing.T) {
	var m docker.MockExecutor
	// Exec mkdir → success
	m.AddResult("", "", nil)
	// AppendFile → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := InjectPublicKey(context.Background(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...\n")
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"mkdir", "-p", "/root/.ssh",
	}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"exec", "-i", "sind-dev-controller",
		"sh", "-c", "cat >> " + authorizedKeysPath,
	}, m.Calls[1].Args)
	assert.Equal(t, "ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestInjectPublicKey_AddsNewline(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := InjectPublicKey(context.Background(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...")
	require.NoError(t, err)

	// Should append newline if missing.
	assert.Equal(t, "ssh-ed25519 AAAA...\n", m.Calls[1].Stdin)
}

func TestInjectPublicKey_MkdirError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := InjectPublicKey(context.Background(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...\n")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating .ssh directory")
}

func TestInjectPublicKey_WriteError(t *testing.T) {
	var m docker.MockExecutor
	// Exec mkdir → success
	m.AddResult("", "", nil)
	// AppendFile → error
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := InjectPublicKey(context.Background(), c,
		"sind-dev-controller", "ssh-ed25519 AAAA...\n")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing authorized_keys")
}
