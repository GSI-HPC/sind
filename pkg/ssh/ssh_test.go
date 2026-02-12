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

// --- CollectHostKey ---

func TestCollectHostKey(t *testing.T) {
	var m docker.MockExecutor
	// ssh-keyscan output includes comments and the key line
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.6\nlocalhost ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n", "", nil)
	c := docker.NewClient(&m)

	key, err := CollectHostKey(context.Background(), c, "sind-dev-controller")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest", key)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"exec", "sind-dev-controller",
		"ssh-keyscan", "-t", "ed25519", "localhost",
	}, m.Calls[0].Args)
}

func TestCollectHostKey_NoKey(t *testing.T) {
	var m docker.MockExecutor
	// ssh-keyscan returns only comments (e.g. sshd not serving ed25519)
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.6\n", "", nil)
	c := docker.NewClient(&m)

	_, err := CollectHostKey(context.Background(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ed25519 host key found")
}

func TestCollectHostKey_MalformedLine(t *testing.T) {
	var m docker.MockExecutor
	// Non-comment line with no space (malformed, skipped)
	m.AddResult("malformed\n", "", nil)
	c := docker.NewClient(&m)

	_, err := CollectHostKey(context.Background(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ed25519 host key found")
}

func TestCollectHostKey_EmptyOutput(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, err := CollectHostKey(context.Background(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ed25519 host key found")
}

func TestCollectHostKey_ExecError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	_, err := CollectHostKey(context.Background(), c, "sind-dev-controller")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scanning host key")
}
