---
weight: 352
title: "MCP Integration"
description: "Using sind with AI assistants via the Model Context Protocol"
---

sind includes a built-in [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server that exposes all CLI commands as tools for AI assistants. This lets tools like Claude, VS Code Copilot, or Cursor create clusters, check status, manage workers, and control node power states through natural language.

## Quick setup

Register sind with your editor:

```bash
# Claude Desktop
sind mcp claude enable

# VS Code
sind mcp vscode enable

# Cursor
sind mcp cursor enable
```

To unregister:

```bash
sind mcp claude disable
sind mcp vscode disable
sind mcp cursor disable
```

## Manual configuration

If you prefer to configure the MCP server manually, add the following to your editor's MCP config:

```json
{
  "mcpServers": {
    "sind": {
      "command": "sind",
      "args": ["mcp", "start"]
    }
  }
}
```

## Available tools

All sind commands are automatically exposed as MCP tools. To see the full list:

```bash
sind mcp tools
```

This exports the tool definitions to `mcp-tools.json`. The tools follow the naming convention `sind_<command>_<subcommand>`, for example:

| Tool | Description |
|---|---|
| `sind_create_cluster` | Create a new cluster |
| `sind_create_worker` | Add workers to a running cluster |
| `sind_delete_cluster` | Delete a cluster |
| `sind_get_cluster` | Show cluster health status |
| `sind_get_nodes` | List cluster nodes |
| `sind_get_realms` | List active realms |
| `sind_get_mesh` | Show mesh infrastructure info |
| `sind_power_shutdown` | Shut down a node |
| `sind_power_reboot` | Reboot a node |
| `sind_ssh` | SSH into a node |

## HTTP mode

For remote or multi-client setups, sind can serve MCP over HTTP:

```bash
sind mcp stream --host localhost --port 8080
```
