---
weight: 370
title: "Exit Status"
icon: "exit_to_app"
description: "What sind's exit status tells a script"
toc: true
---

Scripts branch on sind's exit status, so it is part of the command line contract: a status may be added, but never renumbered.

| Status | Meaning | Typical cause |
|--------|---------|---------------|
| `0` | The command succeeded | |
| `1` | The command ran and failed | Docker is not reachable, a node did not become ready, a cluster does not exist, a required `sind doctor` check failed, an invalid `SIND_REALM` or config file |
| `2` | sind rejected the command line before acting on it | An unknown command or flag, a flag value or argument that is not valid, the wrong number of arguments |
| `130` | SIGINT or SIGTERM interrupted the command | Ctrl-C, `timeout`, `docker stop` |

## Usage errors

Status `2` means that the command line has to change before the command can succeed; running it again will not help.

```console
$ sind get cluster dev extra
12:00:00.000 ERRO accepts at most 1 arg(s), received 2
$ echo $?
2
```

This covers:

- an unknown command or help topic: `sind get bogus`, `sind help bogus`, `sind completion fishy`
- an unknown flag, a flag without its value, or a value that is not valid: `sind get clusters -o yaml`, `sind --realm Not_A_Realm get clusters`
- the wrong number of arguments, or an argument that is not valid: `sind get cluster a b`, `sind get cluster Not_A_Name`
- a node argument that does not expand, or that names several nodes where one is needed: `sind power on 'worker-['`, `sind ssh 'worker-[0-1]'`
- `sind exec` without `--` and a command

`SIND_REALM` and the config file are not part of the command line: an invalid one exits `1`.

## Interrupts

The first SIGINT or SIGTERM stops the command and runs its cleanup, such as the rollback of a failed `sind create cluster`; sind then exits `130`. A second signal ends sind at once, without waiting for the cleanup to finish.

## MCP tools

A tool call of the [MCP server]({{< relref "/guides/mcp" >}}) returns the command's exit status as `exitCode`, next to its stdout and stderr: a tool call whose arguments sind rejects reads `2`.
