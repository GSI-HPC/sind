// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPStream_ListensOnLocalhost(t *testing.T) {
	stream, _, err := NewRootCommand().Find([]string{"mcp", "stream"})
	require.NoError(t, err)

	host := stream.Flags().Lookup("host")
	require.NotNil(t, host)
	assert.Equal(t, "127.0.0.1", host.DefValue)
	assert.Equal(t, "127.0.0.1", host.Value.String())
	assert.Equal(t, "8080", stream.Flags().Lookup("port").DefValue)
}

// mcpTool is the part of an exported MCP tool definition the tests look at.
type mcpTool struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties struct {
			Flags struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"flags"`
		} `json:"properties"`
	} `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

// exportMCPTools runs `sind mcp tools` in a temporary directory and returns
// the tools it exported, by name.
func exportMCPTools(t *testing.T) map[string]mcpTool {
	t.Helper()
	t.Chdir(t.TempDir())
	_, _, err := executeCommand("mcp", "tools")
	require.NoError(t, err)
	data, err := os.ReadFile("mcp-tools.json")
	require.NoError(t, err)
	var list []mcpTool
	require.NoError(t, json.Unmarshal(data, &list))
	tools := make(map[string]mcpTool, len(list))
	for _, tool := range list {
		tools[tool.Name] = tool
	}
	return tools
}

func TestMCPTools_Set(t *testing.T) {
	tools := exportMCPTools(t)
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	slices.Sort(names)
	assert.Equal(t, []string{
		"sind_create_cluster",
		"sind_create_worker",
		"sind_delete_cluster",
		"sind_delete_worker",
		"sind_doctor",
		"sind_exec",
		"sind_get_cluster",
		"sind_get_clusters",
		"sind_get_dns",
		"sind_get_mesh",
		"sind_get_networks",
		"sind_get_node",
		"sind_get_nodes",
		"sind_get_realms",
		"sind_get_ssh-config",
		"sind_get_ssh-known-hosts",
		"sind_get_ssh-public-key",
		"sind_get_volumes",
		"sind_logs",
		"sind_power_cut",
		"sind_power_cycle",
		"sind_power_freeze",
		"sind_power_on",
		"sind_power_reboot",
		"sind_power_shutdown",
		"sind_power_unfreeze",
		"sind_version",
	}, names)
}

func TestMCPExcluded_NamesExistingCommands(t *testing.T) {
	root := NewRootCommand()
	for path := range mcpExcluded {
		cmd, rest, err := root.Find(strings.Fields(path))
		require.NoError(t, err, path)
		assert.Empty(t, rest, path)
		assert.Equal(t, path, commandPath(cmd))
	}
}
