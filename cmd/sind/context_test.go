// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRealm_FlagOverridesAll(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")
	require.NoError(t, cmd.Flags().Set("realm", "from-flag"))

	result, err := resolveRealm(cmd, "from-config")
	require.NoError(t, err)
	assert.Equal(t, "from-flag", result)
}

func TestResolveRealm_ConfigOverridesEnv(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "from-env")
	result, err := resolveRealm(cmd, "from-config")
	require.NoError(t, err)
	assert.Equal(t, "from-config", result)
}

func TestResolveRealm_EnvOverridesDefault(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "from-env")
	result, err := resolveRealm(cmd, "")
	require.NoError(t, err)
	assert.Equal(t, "from-env", result)
}

func TestResolveRealm_DefaultFallback(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	result, err := resolveRealm(cmd, "")
	require.NoError(t, err)
	assert.Equal(t, mesh.DefaultRealm, result)
}

func TestRealmFromFlag_NoFlagUsesDefault(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	result, err := realmFromFlag(cmd)
	require.NoError(t, err)
	assert.Equal(t, mesh.DefaultRealm, result)
}

func TestRealmFromFlag_WithEnv(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "envtest")
	result, err := realmFromFlag(cmd)
	require.NoError(t, err)
	assert.Equal(t, "envtest", result)
}

func TestResolveRealm_InvalidFlag(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")
	require.NoError(t, cmd.Flags().Set("realm", "../x"))

	_, err := resolveRealm(cmd, "")
	require.EqualError(t, err, `--realm: invalid realm name "../x": '.' is not a letter, a digit or a hyphen`)
}

func TestResolveRealm_InvalidEnv(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "ci_42")
	_, err := resolveRealm(cmd, "")
	require.EqualError(t, err, `SIND_REALM: invalid realm name "ci_42": '_' is not a letter, a digit or a hyphen`)
}

// TestResolveRealm_InvalidEnvNotUsed checks that SIND_REALM is only checked
// when it is the realm in effect.
func TestResolveRealm_InvalidEnvNotUsed(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "ci_42")
	result, err := resolveRealm(cmd, "from-config")
	require.NoError(t, err)
	assert.Equal(t, "from-config", result)
}
