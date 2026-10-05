---
weight: 80
title: "10 · Let your assistant drive"
description: "MCP, and an AI assistant that builds and debugs a cluster"
---

{{< video "course-10-assistant" >}}

You work in your editor with Claude, VS Code Copilot or Cursor beside you, and you would like it to create clusters, check their status and manage their nodes for you. This episode connects sind's built-in MCP server to your assistant, shows the tools it offers, and lets the assistant build and debug a cluster, with exit codes, HTTP mode and the security limits along the way.

## In this episode

- Registering sind with Claude Desktop, VS Code or Cursor, or by hand: [MCP Integration]({{< relref "/guides/mcp#quick-setup" >}})
- Every command as a tool, marked read-only or destructive, and what stays out: [Available tools]({{< relref "/guides/mcp#available-tools" >}})
- Exit codes in tool calls: [Exit Status]({{< relref "/usage/exit-status#mcp-tools" >}})
- MCP over HTTP with a bearer token: [HTTP mode]({{< relref "/guides/mcp#http-mode" >}})
- What the tools can do on your Docker host: [Security]({{< relref "/guides/mcp#security" >}})

## Commands

Every command and output in the video, in order, as on the reference pages.

Register sind with your editor ([MCP Integration]({{< relref "/guides/mcp#quick-setup" >}})):

```bash
# Claude Desktop
sind mcp claude enable

# VS Code
sind mcp vscode enable

# Cursor
sind mcp cursor enable
```

Or add sind to your editor's MCP config by hand ([Manual configuration]({{< relref "/guides/mcp#manual-configuration" >}})):

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

Export the tool definitions to `mcp-tools.json` ([Available tools]({{< relref "/guides/mcp#available-tools" >}})):

```bash
sind mcp tools
```

The tool table in the video is a subset of the one on the [MCP Integration]({{< relref "/guides/mcp#available-tools" >}}) page. The chat scenes show only your request and the names of the tools the assistant calls, such as `sind_create_cluster`, `sind_get_nodes`, `sind_get_cluster`, `sind_logs`, `sind_doctor` and `sind_power_reboot`; what a call returns depends on your cluster.

`sind exec` exits with the status of the command it ran ([Exit Status › Commands that run a program]({{< relref "/usage/exit-status#commands-that-run-a-program" >}})), and the `sind_exec` tool returns that status as its `exitCode`; a call whose arguments sind rejects reads `2` ([Exit Status › MCP tools]({{< relref "/usage/exit-status#mcp-tools" >}})):

```console
$ sind exec -- sh -c 'exit 42'
$ echo $?
42
```

Serve MCP over HTTP ([HTTP mode]({{< relref "/guides/mcp#http-mode" >}})):

```bash
sind mcp stream --port 8080
```

```text
MCP server listening on http://127.0.0.1:8080/
Bearer token in /home/alice/.local/state/sind/mcp-token (new at every start; set SIND_MCP_TOKEN to keep one)
Client config: {"type": "http", "url": "http://127.0.0.1:8080/", "headers": {"Authorization": "Bearer <token>"}}
```

## Refresher: What MCP is

An assistant connects to servers that describe their tools, decides when to call one, can ask you to approve the call, and gets the result back, over stdio or HTTP: [the full refresher]({{< relref "refreshers#what-mcp-is" >}}).

## Go deeper

- [MCP Integration]({{< relref "/guides/mcp" >}}): setup flags, the full tool list, HTTP mode and security, with its own clip
- [Exit Status]({{< relref "/usage/exit-status" >}}): every status a script or an assistant can branch on
- [Diagnostics]({{< relref "/usage/diagnostics" >}}): what `sind doctor` and the `get` commands report
- [Cluster Configuration]({{< relref "/configuration/cluster-config#trust" >}}): what a cluster config can grant, before you approve one
- [Introduction]({{< relref "/introduction" >}}): sind's features, AI-ready via MCP among them

This was the last course episode. Pick a guide from the [Video Course]({{< relref "/course" >}}) page that fits your work, and keep building.
