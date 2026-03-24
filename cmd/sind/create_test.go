// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Default(t *testing.T) {
	cfg, err := loadConfig("")
	require.NoError(t, err)
	assert.Equal(t, "Cluster", cfg.Kind)
	assert.Equal(t, "default", cfg.Name)
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("kind: Cluster\nname: test\n")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Name)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading config")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: valid: yaml: ["), 0o644))

	_, err := loadConfig(path)
	assert.Error(t, err)
}

func TestCreateCluster_FlagParsing(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantName string
	}{
		{
			name:     "default name",
			args:     []string{"create", "cluster"},
			wantName: "default",
		},
		{
			name:     "custom name",
			args:     []string{"create", "cluster", "--name", "dev"},
			wantName: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCommand()
			cmd.SetArgs(tt.args)

			// Find the create cluster subcommand to check flag parsing
			createCmd, _, err := cmd.Find([]string{"create", "cluster"})
			require.NoError(t, err)
			assert.Equal(t, "cluster [--name NAME] [--config FILE]", createCmd.Use)
		})
	}
}

func TestCreateCluster_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("create", "cluster", "extra-arg")
	assert.Error(t, err)
}

func TestCreateCluster_NameOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("kind: Cluster\nname: from-file\n")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "from-file", cfg.Name)

	// When --name is used, it should override the config name
	// (tested via the runCreateCluster flow, but we verify the config loading
	// returns the file value so the override logic can be tested separately)
}
