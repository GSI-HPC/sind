// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readMCPConfig returns the servers of an editor configuration and the
// file's other top-level members.
func readMCPConfig(t *testing.T, path, key string) (servers map[string]map[string]any, rest map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &rest))
	raw, err := json.Marshal(rest[key])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &servers))
	delete(rest, key)
	return servers, rest
}

// backups returns the backups of the configuration file at path.
func backups(t *testing.T, path string) []string {
	t.Helper()
	matches, err := filepath.Glob(path + ".*.bak")
	require.NoError(t, err)
	return matches
}

func TestMCPEnable_KeepsTheRestOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	orig := `{"globalShortcut":"Ctrl+Space","preferences":{"x":1},` +
		`"mcpServers":{"other":{"command":"x","cwd":"/w","disabled":true,"autoApprove":["t"]}}}`
	require.NoError(t, os.WriteFile(path, []byte(orig), 0o644))

	stdout, stderr, err := executeCommand("mcp", "claude", "enable", "--config-path", path)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)

	exe, err := os.Executable()
	require.NoError(t, err)
	servers, rest := readMCPConfig(t, path, "mcpServers")
	assert.Equal(t, map[string]any{"globalShortcut": "Ctrl+Space", "preferences": map[string]any{"x": float64(1)}}, rest)
	assert.Equal(t, map[string]map[string]any{
		"other": {"command": "x", "cwd": "/w", "disabled": true, "autoApprove": []any{"t"}},
		"sind":  {"command": exe, "args": []any{"mcp", "start"}},
	}, servers)

	require.Len(t, backups(t, path), 1)
	backup, err := os.ReadFile(backups(t, path)[0])
	require.NoError(t, err)
	assert.Equal(t, orig, string(backup))

	// Enabled again, the file does not change and gets no new backup.
	_, _, err = executeCommand("mcp", "claude", "enable", "--config-path", path)
	require.NoError(t, err)
	assert.Len(t, backups(t, path), 1)
}

func TestMCPEnable_VSCodeWorkspaceWithComments(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir(".vscode", 0o755))
	path := filepath.Join(".vscode", "mcp.json")
	require.NoError(t, os.WriteFile(path, []byte("{\n  // servers of this workspace\n  \"servers\": {},\n}\n"), 0o644))

	_, _, err := executeCommand("mcp", "vscode", "enable", "--workspace", "--server-name", "slurm")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "// servers of this workspace")
	assert.Contains(t, string(data), "\"slurm\": {\n      \"type\": \"stdio\",")

	stdout, _, err := executeCommand("mcp", "vscode", "list", "--workspace")
	require.NoError(t, err)
	exe, err := os.Executable()
	require.NoError(t, err)
	assert.Equal(t, "NAME    COMMAND\nslurm   "+exe+" mcp start\n", stdout)

	_, _, err = executeCommand("mcp", "vscode", "disable", "--workspace", "--server-name", "slurm")
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{\n  // servers of this workspace\n  \"servers\": {},\n}\n", string(data))
}

func TestMCPEnable_Environment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	_, _, err := executeCommand("--realm", "ci", "mcp", "cursor", "enable", "--config-path", path,
		"--env", "PATH=/opt/bin", "--env", "SIND_REALM=ci", "--log-level", "debug")
	require.NoError(t, err)

	servers, _ := readMCPConfig(t, path, "mcpServers")
	assert.Equal(t, "stdio", servers["sind"]["type"])
	assert.Equal(t, []any{"mcp", "start", "--log-level", "debug"}, servers["sind"]["args"])
	assert.Equal(t, map[string]any{"PATH": "/opt/bin", "SIND_REALM": "ci"}, servers["sind"]["env"])
	assert.Empty(t, backups(t, path), "a new file needs no backup")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	_, _, err = executeCommand("--realm", "dev", "mcp", "cursor", "enable", "--config-path", path)
	require.NoError(t, err)
	servers, _ = readMCPConfig(t, path, "mcpServers")
	assert.Equal(t, map[string]any{"SIND_REALM": "dev"}, servers["sind"]["env"])
	assert.Equal(t, []any{"mcp", "start"}, servers["sind"]["args"])
}

func TestMCPEnable_UsageErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	tests := []struct {
		args    []string
		wantErr string
	}{
		{
			[]string{"--realm", "ci", "mcp", "cursor", "enable", "--config-path", path, "--env", "SIND_REALM=dev"},
			"--realm ci and --env SIND_REALM=dev name different realms",
		},
		{
			[]string{"mcp", "cursor", "enable", "--config-path", path, "--workspace"},
			"--config-path and --workspace are mutually exclusive",
		},
		{[]string{"mcp", "claude", "enable", "--workspace"}, "unknown flag: --workspace"},
		{[]string{"mcp", "claude", "disable", "extra"}, "accepts no arguments, received 1 (usage: sind mcp claude disable)"},
	}
	for _, tt := range tests {
		t.Run(tt.wantErr, func(t *testing.T) {
			_, _, err := executeCommand(tt.args...)
			require.ErrorContains(t, err, tt.wantErr)
			assert.True(t, cobratree.IsUsage(err))
		})
	}
	_, err := os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing is written")
}

func TestMCPEditor_DefaultPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))

	for _, tt := range []struct{ editor, path, key string }{
		{"claude", filepath.Join(home, "config", "Claude", "claude_desktop_config.json"), "mcpServers"},
		{"vscode", filepath.Join(home, "config", "Code", "User", "mcp.json"), "servers"},
		{"cursor", filepath.Join(home, ".cursor", "mcp.json"), "mcpServers"},
	} {
		t.Run(tt.editor, func(t *testing.T) {
			_, _, err := executeCommand("mcp", tt.editor, "enable")
			require.NoError(t, err)
			servers, _ := readMCPConfig(t, tt.path, tt.key)
			assert.Contains(t, servers, "sind")
		})
	}
}

func TestMCPEditor_NoUserConfigDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	_, _, err := executeCommand("mcp", "claude", "list")
	require.ErrorContains(t, err, "locating the Claude Desktop configuration: ")
	assert.False(t, cobratree.IsUsage(err))
}

func TestMCPDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"mcpServers":{"other":{},"sind":{}}}`), 0o644))

	_, _, err := executeCommand("mcp", "claude", "disable", "--config-path", path)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcpServers":{"other":{}}}`, string(data))
	assert.Len(t, backups(t, path), 1)

	_, _, err = executeCommand("mcp", "claude", "disable", "--config-path", path)
	require.EqualError(t, err, `MCP server "sind" is not registered in `+path)
	assert.False(t, cobratree.IsUsage(err))
	assert.Len(t, backups(t, path), 1)
}

func TestMCPList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"mcpServers":{
		"sind":{"command":"/usr/bin/sind","args":["mcp","start"],"env":{"TOKEN":"secret"}},
		"web":{"type":"http","url":"https://example.com/mcp"},
		"evil\u001b]0;x\u0007":{"command":"x"}}}`), 0o644))

	stdout, _, err := executeCommand("mcp", "claude", "list", "--config-path", path)
	require.NoError(t, err)
	assert.Equal(t, "NAME               COMMAND\n"+
		"sind               /usr/bin/sind mcp start\n"+
		"web                https://example.com/mcp\n"+
		`evil\x1b]0;x\x07   x`+"\n", stdout)

	stdout, _, err = executeCommand("mcp", "claude", "list", "--config-path", filepath.Join(t.TempDir(), "none.json"))
	require.NoError(t, err)
	assert.Equal(t, "NAME   COMMAND\n", stdout)
}

func TestMCPEditor_LoadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"mcpServers": [`), 0o644))
	for _, sub := range []string{"enable", "disable", "list"} {
		_, _, err := executeCommand("mcp", "claude", sub, "--config-path", path)
		require.ErrorContains(t, err, "parsing "+path+": ", sub)
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"mcpServers": [`, string(data))
}
