---
weight: 352
title: "MCP Integration"
description: "Using sind with AI assistants via the Model Context Protocol"
webui: none
webuiNote: "The MCP server runs the commands of the CLI. The steps on this page are for the CLI."
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

Claude Code has its own command, which registers sind's stdio server under the name `sind` (`claude mcp remove sind` removes it):

```bash
claude mcp add sind -- sind mcp start
```

To unregister:

```bash
sind mcp claude disable
sind mcp vscode disable
sind mcp cursor disable
```

`enable` adds an entry named `sind` that runs `sind mcp start` and changes nothing else in the editor's configuration: its other settings, the other servers, comments and formatting stay. The entry runs sind by the absolute path of the binary you ran `enable` with, so run `enable` again after you move sind or install it somewhere else. Run again, `enable` keeps the entry's environment, its realm among it, and settings you added such as `disabled` or `autoApprove`; `--env`, `--realm` and `SIND_REALM` change the variables they name, and the command's arguments are those of the flags you give, `--log-level` among them. `disable` removes the entry, and fails if there is none. Before either changes the file, it copies it to `<file>.<YYYYMMDD-HHMMSS>.bak` next to it; a backup is never overwritten. A file that names a server twice is left alone with an error that names it, since your editor reads the last of the two; remove one by hand.

`sind mcp <editor> list` shows the MCP servers registered with that editor, each with its command or URL; when there are none, it says so on stderr with the file it read, `No MCP servers found in "<path>".` `enable` takes `--server-name` (default `sind`), `--log-level`, `--config-path` and `--env KEY=VALUE`, and for VS Code and Cursor `--workspace` to register sind in the workspace settings (`.vscode/mcp.json`, `.cursor/mcp.json`) instead; see `sind mcp <editor> enable --help`. `enable` checks what it writes: `--env SIND_REALM` must be a valid realm name, `--env SIND_CLUSTER` a valid cluster name, and `--log-level` one of `debug`, `info`, `warn` and `error`, and `--server-name` and `--config-path` must not be empty. The editor starts sind with its own environment, not your shell's, so `sind --realm ci mcp claude enable` writes `SIND_REALM=ci` into the entry to serve the realm `ci`, as `sind --realm ci mcp start` does for a server you start yourself. An exported `SIND_REALM` is written the same way, unless `--env SIND_REALM=...` names the realm. `--env SIND_CLUSTER=dev` makes `dev` the cluster of the tool calls that name none; an exported `SIND_CLUSTER` is not written.

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

The tools take the same arguments and flags as the commands, except `-v`, `sind logs --follow`, `--progress` and `--progress-log`, which a tool call cannot use. Flags go in the call's `flags` object; a call that puts a flag in `args` is refused, except for the command after `--` in `sind_exec`. A flag left out of a call is not given, as on the command line: `sind_logs` without `tail` returns the whole log, with `"tail": 0` none. A tool call has no stdin, so `sind_create_cluster` takes `config` as the path of a file the server can read, not `-`.

Tool calls draw no [progress]({{< relref "/usage/progress" >}}) and name no event log: the server runs them with `SIND_PROGRESS=none` and without `SIND_PROGRESS_LOG`, whatever its own environment says, so a tool's stderr holds what the command writes without progress. A call that comes with a progress token gets the progress of its command as MCP progress notifications: the number of targets that have ended, of how many the command expects, and a line such as `start the nodes: 3/16 done, 1 failed`. A command that counts no targets sends none. A call that creates, deletes, connects, disconnects or powers still appends to the [activity log]({{< relref "/usage/progress#the-activity-log" >}}), like the same command on a shell. Its error lines carry no time stamp, as in `ERRO cluster "dev" not found in realm "sind"`.

Each tool tells the client what it does, so that the client can decide when to ask before calling it: the `get` tools, `sind_logs`, `sind_doctor` and `sind_version` are marked read-only; `sind_power_on`, `sind_power_unfreeze` and `sind_connect_container` only restore or add; `sind_create_*`, `sind_delete_*`, `sind_disconnect_container`, `sind_exec` and the other `sind_power_*` tools are marked destructive. The create tools are destructive because their flags decide what runs on your Docker host (see [Security](#security)).

The `sind_get_*` tools and `sind_doctor` always return JSON: the server runs them with `-o json`, so they take no `-o` flag.

Each tool call returns the command's stdout, stderr and exit status (`exitCode`). A call whose arguments or flags sind rejects exits `2`, a flag in `args` among them, and `sind_exec` returns the exit status of the command it ran. A call whose command exits non-zero is also marked as an error (`isError: true`); see [Exit Status]({{< relref "/usage/exit-status" >}}). A flag the tool does not have fails the validation of the call's input before sind runs, with an error result and no `exitCode`.

A client that cancels a call of a read-only tool or of `sind_exec` gets its answer at once: the server stops the sind process and the docker command it runs. The command `sind_exec` started in the node runs on until it ends, since Docker cannot stop it, so bound a long one with `timeout`, as in `"args": ["--", "timeout", "60", "srun", "hostname"]`. The tools that change state run to completion, or roll back a failed create, and answer when they end.

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
| `sind_get_nodes` | List the nodes of a cluster, or of every cluster of the realm |
| `sind_get_realms` | List all realms |
| `sind_get_mesh` | Show mesh infrastructure info |
| `sind_get_monitoring` | Show the realm's monitoring stack |
| `sind_power_shutdown` | Gracefully shut down nodes (docker stop) |
| `sind_power_reboot` | Gracefully reboot nodes (shutdown, then on) |
| `sind_exec` | Run a command on the submitter or controller |

## HTTP mode

For multi-client setups, sind can serve MCP over HTTP:

```bash
sind mcp stream --port 8080
```

The stream listens on `127.0.0.1` unless `--host` names another address; an empty `--host` is an error. With `--host 0.0.0.0` the first line it prints ends in `(all interfaces)`, and the URL in it stays `http://localhost:PORT/`, where a client on this machine connects. Every request needs the header `Authorization: Bearer <token>`; without it the stream answers `401 Unauthorized`. Loopback keeps other hosts out, but not the other users of your machine, and every tool runs sind with your Docker access, so the token is what keeps them from creating containers as you.

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

Set `SIND_MCP_TOKEN` (printable ASCII, no spaces, at least 16 characters) to keep one token across restarts, for example from a password manager: `SIND_MCP_TOKEN=$(openssl rand -hex 32) sind mcp stream`. The token travels in clear text over HTTP, so pass `--host 0.0.0.0` (all interfaces) only on a network you trust, or put a TLS proxy in front of the stream. `sind mcp start` (stdio) needs no token: only the program that started it can talk to it.

## Security

The MCP server is not a sandbox. Its tools run sind with your Docker access: `sind_exec` runs any command inside a cluster, and `sind_create_cluster` bind-mounts a host directory read-write at `/data`: the one `--data` names or, without it, the MCP server's working directory. Pass `data: "volume"` to keep host files out. `sind_create_cluster` also reads any config file the server can read, and a config sets images, capabilities, devices and host paths (see what a [cluster config can grant]({{< relref "/configuration/cluster-config#trust" >}})). `sind_create_worker` takes `image`, `cap-add`, `cap-drop`, `device` and `security-opt`; an empty array, e.g. `"device": []`, gives the new workers none, while leaving the flag out gives them the newest worker's. These flags matter: a single call can start an image of the caller's choosing as root with every capability, a host disk and no seccomp or AppArmor profile, which amounts to root on the Docker host. Approve a create call only after reading its flags. Leaving out `sind get ssh-private-key` and `sind get auth-key` keeps those secrets out of routine tool output, but an agent that may call `sind_exec` can still read them from the nodes. An agent that creates a cluster with `data: "/"`, or with a config whose `storage.dataStorage.hostPath` is `/`, and then calls `sind_exec` has root on the host. Give an agent the sind tools only where you would let it run sind yourself.
