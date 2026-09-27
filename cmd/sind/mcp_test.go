// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/njayp/ophis"
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

func TestMCPTools_Flags(t *testing.T) {
	tools := exportMCPTools(t)
	for name, tool := range tools {
		flags := tool.InputSchema.Properties.Flags.Properties
		assert.NotContains(t, flags, "verbose", name)
		assert.NotContains(t, flags, "follow", name)
		assert.Contains(t, flags, "realm", name)
	}
	assert.Contains(t, tools["sind_create_cluster"].InputSchema.Properties.Flags.Properties, "config")
}

func TestMCPTools_GetHasNoOutputFlag(t *testing.T) {
	tools := exportMCPTools(t)
	var get int
	for name, tool := range tools {
		if strings.HasPrefix(name, "sind_get_") {
			get++
			assert.NotContains(t, tool.InputSchema.Properties.Flags.Properties, "output", name)
		}
	}
	assert.Equal(t, 12, get)
}

func TestForceJSONOutput(t *testing.T) {
	in := ophis.ToolInput{Flags: map[string]any{"output": "human", "realm": "ci-42"}, Args: []string{"dev"}}
	var got ophis.ToolInput
	next := func(_ context.Context, _ *mcp.CallToolRequest, in ophis.ToolInput) (*mcp.CallToolResult, ophis.ToolOutput, error) {
		got = in
		return nil, ophis.ToolOutput{}, nil
	}

	_, _, err := forceJSONOutput(t.Context(), &mcp.CallToolRequest{}, in, next)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"output": "json", "realm": "ci-42"}, got.Flags)
	assert.Equal(t, []string{"dev"}, got.Args)
	assert.Equal(t, "human", in.Flags["output"], "the caller's map is not modified")

	_, _, err = forceJSONOutput(t.Context(), &mcp.CallToolRequest{}, ophis.ToolInput{}, next)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"output": "json"}, got.Flags)
}
