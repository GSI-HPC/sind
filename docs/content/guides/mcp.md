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

`sind mcp <editor> list` shows the MCP servers registered with that editor. `enable` takes `--server-name`, `--log-level`, `--config-path` and `--env KEY=VALUE` (e.g. `--env SIND_REALM=ci` to serve another realm; `sind --realm ci mcp start` does the same for a server you start yourself), and for VS Code and Cursor `--workspace` to register sind in the workspace settings instead; see `sind mcp <editor> enable --help`.

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
- `sind help`, `sind completion` and the `sind mcp` commands
- `sind enter` and `sind ssh`, which need an interactive terminal
- `sind get ssh-private-key` and `sind get auth-key`, which print secrets

The tools take the same arguments and flags as the commands, except `-v` and `sind logs --follow`, which a tool call cannot use. Flags go in the call's `flags` object; a call that puts a flag in `args` is refused, except for the command after `--` in `sind_exec`.

Each tool tells the client what it does, so that the client can decide when to ask before calling it: the `get` tools, `sind_logs`, `sind_doctor` and `sind_version` are marked read-only; `sind_power_on` and `sind_power_unfreeze` only restore; `sind_create_*`, `sind_delete_*`, `sind_exec` and the other `sind_power_*` tools are marked destructive. The create tools are destructive because their flags decide what runs on your Docker host (see [Security](#security)).

The `sind_get_*` tools and `sind_doctor` always return JSON: the server runs them with `-o json`, so they take no `-o` flag.

Each tool call returns the command's stdout, stderr and exit status (`exitCode`). A call whose arguments or flags sind rejects exits `2`, and `sind_exec` returns the exit status of the command it ran; see [Exit Status]({{< relref "/usage/exit-status" >}}).

To see the full list:

```bash
sind mcp tools
```

This exports the tool definitions to `mcp-tools.json`. The tools follow the naming convention `sind_<command>_<subcommand>`, for example:

| Tool | Description |
|---|---|
| `sind_create_cluster` | Create a Slurm cluster |
| `sind_create_worker` | Add worker nodes to a cluster |
| `sind_delete_cluster` | Delete a cluster |
| `sind_get_cluster` | Show cluster health status |
| `sind_get_nodes` | List nodes |
| `sind_get_realms` | List all realms |
| `sind_get_mesh` | Show mesh infrastructure info |
| `sind_power_shutdown` | Graceful shutdown |
| `sind_power_reboot` | Graceful reboot |
| `sind_exec` | Run a command on submitter or controller |

## HTTP mode

For multi-client setups, sind can serve MCP over HTTP:

```bash
sind mcp stream --port 8080
```

The stream listens on `127.0.0.1` unless `--host` names another address. Every request needs the header `Authorization: Bearer <token>`; without it the stream answers `401 Unauthorized`. Loopback keeps other hosts out, but not the other users of your machine, and every tool runs sind with your Docker access, so the token is what keeps them from creating containers as you.

The token comes from `SIND_MCP_TOKEN`. When that is not set, the stream generates a new random token at every start and writes it, readable only by you, to `mcp-token` in sind's state directory (`$XDG_STATE_HOME/sind`, by default `~/.local/state/sind`). It prints where the token is, never the token itself, and a client configuration to stderr:

```
MCP server listening on http://127.0.0.1:8080/
Bearer token in /home/alice/.local/state/sind/mcp-token (new at every start; set SIND_MCP_TOKEN to keep one)
Client config: {"type": "http", "url": "http://127.0.0.1:8080/", "headers": {"Authorization": "Bearer <token>"}}
```

For example, with Claude Code:

```bash
claude mcp add --transport http sind http://127.0.0.1:8080/ \
  --header "Authorization: Bearer $(cat ~/.local/state/sind/mcp-token)"
```

Set `SIND_MCP_TOKEN` (printable ASCII, no spaces) to keep one token across restarts, for example from a password manager: `SIND_MCP_TOKEN=$(openssl rand -hex 32) sind mcp stream`. The token travels in clear text over HTTP, so pass `--host 0.0.0.0` (all interfaces) only on a network you trust, or put a TLS proxy in front of the stream. `sind mcp start` (stdio) needs no token: only the program that started it can talk to it.

## Security

The MCP server is not a sandbox. Its tools run sind with your Docker access: `sind_exec` runs any command inside a cluster, and `sind_create_cluster` bind-mounts a host directory read-write at `/data`: the one `--data` names or, without it, the MCP server's working directory. Pass `data: "volume"` to keep host files out. `sind_create_cluster` also reads any config file the server can read, and a config sets images, capabilities, devices and host paths (see what a [cluster config can grant]({{< relref "/configuration/cluster-config#trust" >}})). `sind_create_worker` takes `image`, `cap-add`, `device` and `security-opt`: a single call can start an image of the caller's choosing as root with every capability, a host disk and no seccomp or AppArmor profile, which amounts to root on the Docker host. Approve a create call only after reading its flags. Leaving out `sind get ssh-private-key` and `sind get auth-key` keeps those secrets out of routine tool output, but an agent that may call `sind_exec` can still read them from the nodes. An agent that creates a cluster with `data: "/"`, or with a config whose `storage.dataStorage.hostPath` is `/`, and then calls `sind_exec` has root on the host. Give an agent the sind tools only where you would let it run sind yourself.
