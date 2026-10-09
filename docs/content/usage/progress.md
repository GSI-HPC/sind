---
weight: 355
title: "Progress Display"
icon: "timelapse"
description: "What create, delete and power show while they run, and the event log"
toc: true
---

`sind create cluster`, `create worker`, `delete cluster` (also `--all`), `delete worker` and the `power` commands show their progress on standard error while they run. The other commands (`get`, `ssh`, `enter`, `exec`, `logs`, `doctor`, `version`, `mcp`, `completion`) never do.

By default sind shows it only on a terminal. Where standard error is not a terminal, in a pipe, a file or a CI log, these commands print nothing on success, as they did before, and their standard error holds exactly what it did: errors, `Warning:` lines and the `-v` logs.

## On a terminal

On a terminal, sind draws a live tree in the last rows: the command and how long it has run, each step under way with how many of its targets are done, and each target that runs, with how long it has run and what it is doing. The targets that are done fold into one row, such as `✓ worker-[0-7,11]`; those that failed get a row for each error. A row the terminal has no room for is cut, as `… 2 more running`, and the tree takes at most six rows, or a third of a taller terminal's rows.

When a step is done, the tree leaves one line for it above itself, such as `✓ nodes  6.3s  6 ok`. When the command is done, the tree goes and only these lines stay:

- A command that succeeds leaves nothing else. Its exit status 0 says the rest.
- A command that fails, or that you interrupt, also leaves a summary line, as it does under `--progress counter` and `plain`, then the error line:

  ```text
  sind: ✗ create cluster: failed in 38s: 5 ok, 1 failed, 2 canceled
  12:00:00.000 ERRO cluster dev not ready within 12s: ...
  ```

The tree draws nothing in the first second, so a command done by then leaves nothing on the terminal, as before. A step that ends at once leaves no line, and some steps, which sind usually finishes quickly, show only once they have run for a second, and leave a line only when they took that long or failed.

What the command writes goes above the tree, never into it: `Warning:` lines, the logs that `-v` asks for, and the summary. While another sind command holds the [realm lock]({{< relref "/configuration/realms#advisory-locking" >}}), the tree shows the wait below the warning, with the command it waits for when the warning names one:

```text
Warning: waiting for another sind command in realm "sind" to finish: sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03)
delete cluster · 0:03.1
  realm lock  3.1s  sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03)
```

After Ctrl-C or SIGTERM, the command's row says so while sind cleans up, with how many running targets will stop and how many queued ones will not start, as `create cluster · interrupting · 2 running will stop · 0:41.3`. The summary line then says `canceled`.

On a terminal, the tree, the counter and the summary line draw in colour: ok in green, failed in red, canceled in amber and running in blue. They use the terminal's own 16 colours where it shows no more, and the marks alone when `NO_COLOR` is set. Where stderr is no terminal, or `TERM` is `dumb`, they write plain text, as a log wants.

The tree draws with ASCII marks (`+`, `x`, `~`, `>`) unless `LC_ALL`, `LC_CTYPE` or `LANG` names a UTF-8 locale. On a terminal smaller than 40 columns or 8 rows it draws the counter's single line instead. A sind started in the background (`&`, or Ctrl-Z and `bg`) draws nothing, so that the shell's prompt stays as it is. `sind create cluster --config -` takes the tree off the terminal while it reads the configuration from standard input.

## Choosing the display

`--progress` chooses what sind shows, and `SIND_PROGRESS` does when the flag is not given:

| Mode | Shows |
|------|-------|
| `auto` (default) | the live tree when standard error is a terminal, `TERM` is not `dumb` and standard output goes into no pipe; nothing otherwise |
| `tty` | the live tree |
| `counter` | one line, redrawn as the work goes on, with how far each step under way has got and how long the command has run |
| `plain` | plain lines, one for each thing worth one, for a log as much as a terminal (see [Plain lines](#plain-lines)) |
| `none` | nothing |

`auto` draws nothing while standard output goes into a pipe, as in `sind create cluster | tee out.log`: whatever reads it writes to the same terminal.

`--progress` is a global flag: it may come before or after the subcommand, and every command checks it, also one that shows no progress. A mode it does not know, or `tty` or `counter` where standard error is not a terminal or `TERM` is `dumb`, is a usage error, exit status 2 (see [Exit Status]({{< relref "/usage/exit-status#usage-errors" >}})):

```console
$ sind --progress tty create cluster 2>create.log
$ cat create.log
12:00:00.000 ERRO --progress asks for a live tree, but standard error is not a terminal; use none, or auto to draw one only where it can be
```

`SIND_PROGRESS` fails no command, since a shell profile or a CI job sets it for every command that follows: `tty` or `counter` where neither can be drawn shows nothing, and a value sind does not take shows nothing and says so in one line:

```text
sind: SIND_PROGRESS is "plian"; it takes one of auto, tty, counter, plain, none; no progress is shown
```

## Plain lines

`--progress plain` writes a line when a step starts and when it ends, when a wait with a limit starts, when a target fails, and every ten seconds for each step under way that counts its targets. Each line starts with the time since the command started and names what it tells of by its path from the command. Plain lines have no escape codes and need no terminal, so they suit a CI log or a file. The steps the tree shows only after a second get no line.

## Event log

`--progress-log FILE`, or `SIND_PROGRESS_LOG` when the flag is not given, appends the progress events of the command to FILE, one JSON object per line, in go-clikit's [event log format](https://github.com/GSI-HPC/go-clikit/blob/v0.3.0/doc/event-log.md), version 1: what started and ended when, under which step or target, and how it ended and why. It holds neither the command line nor the environment of the command, and of what the programs sind runs write, only what an error quotes; the `realm lock` wait names the command that holds the lock, its command line and host, as the `Warning:` line does. It works with every `--progress` mode, `none` included, and writes nothing on standard error.

- sind creates the file readable and writable by you alone (mode `0600`) and appends to it, so the commands of a CI job can share one log; each command's lines carry a `run` of their own.
- An existing file must be yours, have no other name (no hard link to it) and be readable or writable by nobody else: sind refuses one that is not, and for one that others can read or write says `run chmod 600 FILE`. A symbolic link on the way to it, and a named pipe, must be yours or root's: sind follows no link put at the file's name after it looked, and refuses another user's named pipe at once instead of waiting for someone to read it. `/dev/stderr`, `/dev/stdout` and `/dev/fd/N` are written to where that stream goes; on the terminal the tree is drawn on, the lines go above the tree, as the `-v` logs do.
- A file named by `--progress-log` that sind cannot use is a usage error, before the command does anything. One named by `SIND_PROGRESS_LOG` is not: sind says why in one line and runs without a log. `--progress-log ""` writes no log, whatever the variable says.
- A log that cannot be written while the command runs, such as on a full disk, does not fail the command: sind says once that the log stops short.
- Only `create`, `delete` and `power` open the file; other commands leave it alone.

When sind is started with a W3C trace context in `TRACEPARENT` (and `TRACESTATE`), the log continues that trace. While sind shows progress or writes a log, it removes both variables from the environment of the programs it runs, such as docker; otherwise they inherit them.

## Scripts, CI and MCP

Where standard error is not a terminal, nothing changes unless you ask: no tree, no summary, no event log. [sind-action](https://github.com/GSI-HPC/sind-action) and scripts that read sind's standard error see what they did before. `--progress plain` adds lines to a CI log, and `--progress-log` keeps the events as a job artifact; see [CI/CD]({{< relref "/guides/ci-cd#progress-in-ci-logs" >}}).

The [MCP server]({{< relref "/guides/mcp" >}}) never shows progress: its tools take neither flag, and it runs its tool calls with `SIND_PROGRESS=none` and without `SIND_PROGRESS_LOG`, whatever its own environment says.
