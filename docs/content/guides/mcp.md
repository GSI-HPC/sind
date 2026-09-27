---
weight: 352
title: "MCP Integration"
description: "Using sind with AI assistants via the Model Context Protocol"
---

sind includes a built-in [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server that exposes its CLI commands as tools for AI assistants. This lets tools like Claude, VS Code Copilot, or Cursor create clusters, check status, manage workers, and control node power states through natural language.

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

Every sind command is exposed as an MCP tool, except:

- command groups such as `sind get`, which only print help
- `sind enter` and `sind ssh`, which need an interactive terminal
- `sind get ssh-private-key` and `sind get munge-key`, which print secrets

The tools take the same arguments and flags as the commands, except `-v` and `sind logs --follow`, which a tool call cannot use.

To see the full list:

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
| `sind_exec` | Run a command on submitter or controller |

## HTTP mode

For multi-client setups, sind can serve MCP over HTTP:

```bash
sind mcp stream --port 8080
```

The stream listens on `127.0.0.1` unless `--host` names another address. It has no authentication, and its tools create and delete containers, so only pass `--host 0.0.0.0` (all interfaces) on a network you trust, or put an authenticating proxy in front of it.

## Security

The MCP server is not a sandbox. Its tools run sind with your Docker access: `sind_exec` runs any command inside a cluster, and `sind_create_cluster` can bind-mount a host directory with `--data`. Leaving out `sind get ssh-private-key` and `sind get munge-key` keeps those secrets out of routine tool output, but an agent that may call `sind_exec` can still read them from the nodes. Give an agent the sind tools only where you would let it run sind yourself.
