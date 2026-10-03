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
| `1` | The command ran and failed | Docker is not reachable, a node did not become ready within `--wait`, a cluster does not exist, a required `sind doctor` check failed, an invalid `SIND_REALM` or config file |
| `2` | sind rejected the command line before acting on it | An unknown command or flag, a flag value or argument that is not valid, the wrong number of arguments |
| `130` | SIGINT or SIGTERM interrupted the command | Ctrl-C, `timeout`, `docker stop` |

`sind ssh`, `sind exec`, `sind enter` and `sind logs` exit with the status of the program they run instead, see [below](#commands-that-run-a-program).

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
- `sind exec` without `--` and a command, and `sind ssh` with a remote command that does not follow `--`: `sind ssh worker-0 hostname`

`SIND_REALM` and the config file are not part of the command line: an invalid one exits `1`.

## Commands that run a program

`sind ssh`, `sind exec`, `sind enter` and `sind logs` hand the terminal to a program, `docker exec` running `ssh`, the command or a shell, or `docker logs`, and exit with its status. They add no error line of their own, since the program has written its own diagnostics.

```console
$ sind exec -- sh -c 'exit 42'
$ echo $?
42
```

- The status is the command's, as `docker exec` reports it, and for `sind ssh` the remote command's, as `ssh` reports it. `ssh` exits `255` when it cannot connect. Docker's own errors, such as a container that is not running, give docker's status and message.
- A failure before the program starts is sind's: `sind exec` in a cluster without a submitter or controller exits `1`, and a usage error exits `2`.
- A `docker` process killed by signal N exits 128 + N, as a shell reports it. Killed by SIGINT or SIGTERM, it exits `130`, like an interrupted sind: these signals reach `docker` when they are sent to sind's process group, as by Ctrl-C.

## Interrupts

The first SIGINT or SIGTERM stops the command and runs its cleanup, such as the rollback of a failed `sind create cluster`; sind then exits `130`. A `--wait` limit of `sind create cluster` or `sind create worker` that runs out is not an interrupt: the command rolls back the same way and exits `1`. SIGTERM exits `130` too, not `143`, so that a script checks one status for "interrupted". A second signal ends sind at once, without waiting for the cleanup to finish.

## MCP tools

A tool call of the [MCP server]({{< relref "/guides/mcp" >}}) returns the command's exit status as `exitCode`, next to its stdout and stderr: a tool call whose arguments sind rejects reads `2`, and `sind_exec` returns the status of the command it ran.
