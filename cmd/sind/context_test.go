// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestResolveRealm_FlagOverridesAll(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")
	cmd.Flags().Set("realm", "from-flag")

	result := resolveRealm(cmd, "from-config")
	assert.Equal(t, "from-flag", result)
}

func TestResolveRealm_ConfigOverridesEnv(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "from-env")
	result := resolveRealm(cmd, "from-config")
	assert.Equal(t, "from-config", result)
}

func TestResolveRealm_EnvOverridesDefault(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "from-env")
	result := resolveRealm(cmd, "")
	assert.Equal(t, "from-env", result)
}

func TestResolveRealm_DefaultFallback(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	result := resolveRealm(cmd, "")
	assert.Equal(t, mesh.DefaultRealm, result)
}

func TestRealmFromFlag_NoFlagUsesDefault(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	result := realmFromFlag(cmd)
	assert.Equal(t, mesh.DefaultRealm, result)
}

func TestRealmFromFlag_WithEnv(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("realm", "", "")

	t.Setenv("SIND_REALM", "envtest")
	result := realmFromFlag(cmd)
	assert.Equal(t, "envtest", result)
}
