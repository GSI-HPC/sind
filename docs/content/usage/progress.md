---
weight: 355
title: "Progress Display"
description: "What create, delete, connect, disconnect and power show while they run, and the event log"
toc: true
---

`sind create cluster`, `create worker`, `delete cluster` (also `--all`), `delete realm`, `delete worker`, `connect container`, `disconnect container` and the `power` commands show their progress on standard error while they run. The other commands (`get`, `ssh`, `enter`, `exec`, `logs`, `doctor`, `version`, `mcp`, `completion`) never do.

By default sind shows it only on a terminal. Where standard error is not a terminal, in a pipe, a file or a CI log, these commands print nothing on success, as they did before, and their standard error holds exactly what it did: errors, `Warning:` lines and the `-v` logs.

{{< ways >}}
{{< cli >}}

Run the command on a terminal. sind draws a live tree of its steps and nodes, as [On a terminal](#on-a-terminal) describes. Ctrl-C stops the command, and sind rolls back what it made.

{{< /cli >}}
{{< ui >}}

Start the command from a dialog of `sind ui`. The task dock at the bottom right shows a card for the run, with its steps and the current step. **Show progress** opens the run page, with a timeline of the steps and a tile for each node. **Stop** stops the command, and sind rolls back what it made. [Runs and Progress]({{< relref "/web-ui/runs" >}}) describes them.

{{< uishot "run-going" >}}

sind ui reads the [activity log](#the-activity-log), so it also shows the runs of the commands that you start in a terminal.

{{< /ui >}}
{{< /ways >}}

## On a terminal

On a terminal, sind draws a live tree in the last rows: the command and how long it has run, each step under way with how many of its targets are done, and each target that runs, with how long it has run and what it is doing. The targets that are done fold into one row, such as `✓ worker-[0-7,11]`; those that failed get a row for each error. A row the terminal has no room for is cut, as `… 2 more running`, and the tree takes at most six rows, or a third of a taller terminal's rows.

When a step is done, the tree leaves one line for it above itself, such as `✓ nodes  6.3s  6 ok`. When the command is done, the tree goes and only these lines stay:

- A command that succeeds leaves nothing else. Its exit status 0 says the rest.
- A command that fails, or that you interrupt, also leaves a summary line, as it does under `--progress counter` and `plain`, then the error line:

  ```text
  sind: ✗ create cluster: failed in 38s: 5 ok, 1 failed, 2 canceled
  12:00:00.000 ERRO cluster "dev" not ready within 12s: ...
  ```

The tree draws nothing in the first second, so a command done by then leaves nothing on the terminal, as before. A step that ends at once leaves no line, and `mesh` and `preflight`, which sind usually finishes quickly, show only once they have run for a second, and leave a line only when they took that long or failed. Each docker command shows under the step or node it runs for once it has taken a second, such as `worker-1  12.0s  docker exec`.

What the command writes goes above the tree, never into it: `Warning:` lines, the logs that `-v` asks for, and the summary. While another sind command holds the [realm lock]({{< relref "/configuration/realms#advisory-locking" >}}), the tree shows the wait below the warning, with the command it waits for when the warning names one:

```text
Warning: waiting for another sind command in realm "sind" to finish: sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03)
delete cluster · 0:03.1
  realm lock  3.1s  sind create cluster dev (pid 4242 on build-07, since 2026-10-04 10:02:03)
```

After Ctrl-C or SIGTERM, the command's row says so while sind cleans up, with how many running targets will stop and how many queued ones will not start, as `create cluster · interrupting · 2 running will stop · 0:41.3`. The summary line then says `canceled`.

On a terminal, the tree, the counter and the summary line draw in colour: ok in green, failed in red, canceled in amber and running in blue. They use the terminal's own 16 colours where it shows no more, and the marks alone when `NO_COLOR` is set. Where stderr is no terminal, or `TERM` is `dumb`, they write plain text, as a log wants.

The tree draws with ASCII marks (`+`, `x`, `~`, `>`), and [plain lines](#plain-lines) join the parts of a path with `>`, unless `LC_ALL`, `LC_CTYPE` or `LANG` names a UTF-8 locale. On a terminal smaller than 40 columns or 8 rows it draws the counter's single line instead. A sind started in the background (`&`, or Ctrl-Z and `bg`) draws nothing, so that the shell's prompt stays as it is. `sind create cluster --config -` takes the tree off the terminal while it reads the configuration from standard input.

## What the commands show

`sind create cluster` shows these steps:

| Step | Targets | What it does |
|------|---------|--------------|
| `mesh` | `network`, `dns`, `volume`, `relay` | sets up the realm's [mesh]({{< relref "/architecture/networking" >}}); a part in place is skipped, and the rows of `dns` and `relay` show the last line of the pull of their image. A mesh in place leaves no line |
| `pull images` | one per image | pulls the images the daemon does not have, or every image with `--pull`; each row shows the last line `docker pull` wrote and, in the event log, how many of the image's layers are done out of those docker has told of so far (see [Image pulls](#image-pulls)). No step when nothing is pulled |
| `preflight` | | beside the pull: checks that no resource of the cluster exists yet and that its nodes fit, creates the cluster network and volumes, reads the Slurm version and checks `nsdelegate` |
| `nodes` | one per node | creates and starts each node, waits until it is `ready` and sets it up (`setup`) |
| `mesh registration` | | adds the nodes' DNS records and `known_hosts` entries |
| `slurm` | one per managed node | starts the Slurm daemons, the db node's first, and waits until each is `ready` |
| `accounts` | | with `accounts`: waits until slurmdbd knows the cluster, then creates the Slurm accounts |
| `home directories` | | with `users`: creates the home directories |
| `monitoring` | | with `monitoring`: creates or starts the realm's [monitoring stack]({{< relref "/guides/monitoring" >}}), waits until it is ready and adds the new nodes to it |
| `rollback` | | after a failure or an interrupt: removes what the command created |

`mesh registration`, `slurm`, `home directories` and `monitoring` run at the same time, and `accounts` after `slurm`. While a node waits, its row says which check it waits for and how much of the `--wait` limit it has used, `2.4s/5m  ready munge`. `sind create worker` shows `pull images` (for an `--image` the daemon does not have, or with `--pull`), `nodes`, `mesh registration`, `slurm` and `rollback` the same way, and on a monitored cluster `monitoring` before `nodes`.

A creation on a host that had neither the mesh nor the images, while the nodes boot:

```text
$ sind create cluster --config dev.yaml
✓ mesh  12s  4 ok
✓ pull images  9.4s  2 ok
✓ preflight  10s
create cluster · 0:25.3
  nodes  0/6 · 6 running
    ▸ controller  2.4s/5m  ready munge
    ▸ db  2.4s/5m  ready sshd
    ▸ worker-0  2.1s  setup
    ▸ worker-1  2.4s/5m  ready systemd
    ▸ worker-2  2.4s/5m  ready systemd
    ▸ worker-3  2.4s/5m  ready sshd
```

and once it is done:

```text
$ sind create cluster --config dev.yaml
✓ mesh  12s  4 ok
✓ pull images  9.4s  2 ok
✓ preflight  10s
✓ nodes  6.3s  6 ok
✓ mesh registration  0.4s
✓ slurm  6.0s  6 ok
✓ accounts  1.7s
```

The same creation with `--wait 12s`, where `worker-2` does not become ready in time and the two nodes still booting are stopped. A failure row writes the node's own name as `{}`, so that nodes that failed the same way share one row:

```text
$ sind create cluster --config dev.yaml --wait 12s
✓ mesh  12s  4 ok
✓ pull images  9.4s  2 ok
✓ preflight  10s
✗ nodes  12s  3 ok, 1 failed, 2 canceled
  ✗ worker-2  timeout: node {}: timed out; last probe error: probe munge: munge not ready: activating
✓ rollback  3.1s
sind: ✗ create cluster: failed in 38s: 5 ok, 1 failed, 2 canceled
12:00:34.839 ERRO cluster "dev" not ready within 12s: node worker-2: timed out; last probe error: probe munge: munge not ready: activating
```

The `power` commands show a step named by what they do, with a target for each node, named as you name it in a node argument (`worker-0`, or `worker-0.dev` in the cluster `dev`): `stopping` (`shutdown`), `killing` (`cut`), `starting` (`on`), `pausing` (`freeze`) or `unpausing` (`unfreeze`); `reboot` shows `stopping` and then `starting`, and `cycle` shows `killing` and then `starting`. A `power reboot` of 24 workers, at most 8 at a time, while it stops them:

```text
$ sind power reboot 'worker-[0-23]'
power reboot · 0:07.3
  stopping  9/24 · 8 running · 7 queued
    ▸ worker-8  4.2s  docker stop
    ▸ worker-9  4.1s  docker stop
    ▸ worker-10  3.4s  docker stop
    ▸ worker-12  1.2s  docker stop
    ▸ worker-13  1.2s  docker stop
    ▸ worker-14  1.2s  docker stop
    … 2 more running
    ✓ worker-[0-7,11]
```

and once it is done:

```text
$ sind power reboot 'worker-[0-23]'
✓ stopping  14s  24 ok
✓ starting  2.2s  24 ok
```

`sind delete cluster` shows these steps, each only when there is something for it to do:

| Step | Targets | What it does |
|------|---------|--------------|
| `mesh deregistration` | | when other clusters keep the realm's mesh: removes the nodes' DNS records and `known_hosts` entries, starting the mesh first if it is stopped. A failure is a warning, not a failure of the step |
| `nodes` | one per node | stops and removes each node's container |
| `network` | | detaches the containers that joined the cluster network, those of the mesh and outside sind, and removes it |
| `volumes` | one per volume | removes the cluster's volumes |
| `monitoring` | | on a monitored cluster: syncs the realm's [monitoring stack]({{< relref "/guides/monitoring" >}}) and removes the nodes' journals |
| `monitoring stack`, `mesh`, `monitoring volumes` | | with the realm's last cluster: remove the monitoring stack's containers, the mesh and the stack's volumes. The two of the stack show only when they take long or fail |

`sind delete worker` shows `slurm` (takes the workers out of `sind-nodes.conf` and reconfigures slurmctld, on a managed cluster) and `mesh deregistration` at the same time, then `nodes`, with a target for each worker, and on a monitored cluster `monitoring`. A node that cannot be removed fails its target, and the other nodes are removed still.

`sind connect container` shows the step `connecting`, and `sind disconnect container` the step `disconnecting`, each with a target for each container, named as you name it: the containers are attached or detached one after another, and one that is on every network of the realm already, or on none, is skipped, as `skipped: connected` or `skipped: not connected`. The first container that fails ends the step, and those not yet tried are canceled.

`sind delete cluster --all` and `sind delete realm` show the step `clusters`, with a target for each cluster; what each cluster's removal does shows as `nodes`, `network` and `volumes` steps below its target, without targets of their own. `mesh deregistration` follows, for the clusters that were deleted when another one failed to be, then `monitoring stack`, `mesh` and `monitoring volumes` for the realm.

## Choosing the display

`--progress` chooses what sind shows, and `SIND_PROGRESS` does when the flag is not given:

| Mode | Shows |
|------|-------|
| `auto` (default) | the live tree when standard error is a terminal, `TERM` is not `dumb` and standard output goes into no pipe; nothing otherwise |
| `tty` | the live tree |
| `counter` | one line, redrawn as the work goes on, with how far each step under way has got and how long the command has run: `stopping · 9/24 · 8 running · 7 queued · 0:07.3` |
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

`--progress plain` writes a line when a step starts and when it ends, when a wait with a limit starts, when a wait or a target fails, and every ten seconds for each step under way that counts its targets. Each line starts with the time since the command started and names what it tells of by its path from the command. Plain lines have no escape codes and need no terminal, so they suit a CI log or a file. `mesh` and `preflight`, which the tree shows only after a second, get no line, and neither do the docker commands or the lines docker writes. The same creation as above, trimmed to two nodes of each step:

```text
$ sind create cluster --config dev.yaml --progress plain 2>&1 | tee create.log
[0:12] create cluster › pull images: start, 2 targets
[0:21] create cluster › pull images: done in 9.4s: 2 ok
[0:22] create cluster › nodes: start, 6 targets
[0:23] create cluster › nodes › worker-0 › ready: waiting 5m0s
[0:23] create cluster › nodes › controller › ready: waiting 5m0s
[0:29] create cluster › nodes: done in 6.3s: 6 ok
[0:29] create cluster › slurm: start, 6 targets
[0:29] create cluster › mesh registration: start
[0:29] create cluster › mesh registration: done in 0.4s
[0:31] create cluster › slurm › db › ready: waiting 4m52s
[0:33] create cluster › slurm › controller › ready: waiting 4m50s
[0:35] create cluster › slurm: done in 6.0s: 6 ok
[0:35] create cluster › accounts: start
[0:35] create cluster › accounts › ready: waiting 4m48s
[0:36] create cluster › accounts: done in 1.7s
```

The waits `ready` say how much of the `--wait` limit they have left. The parts of a path are joined by `›` when `LC_ALL`, `LC_CTYPE` or `LANG` names a UTF-8 locale, and by `>` otherwise (`[0:12] create cluster > pull images: start, 2 targets`), as the tree then draws ASCII marks. On a terminal, each line also takes a mark after the time, in the colours of the tree; in a pipe or a file, as above, it is plain text.

## Image pulls

sind runs `docker pull` without a terminal, and there docker writes no byte counts, only a line for each change of a layer: `Pulling fs layer`, `Download complete`, `Pull complete`, `Already exists`. So a pull is counted in layers, not in bytes: the target of an image reports the layers that are done (`Pull complete` or `Already exists`) out of the layers docker has told of so far, which grows while the pull goes on, as work in `items`. A display can draw it as a count or a bar, and the event log holds it as `amount`, `size` and `unit` on the target (`advance` events, at most four a second), never the text of a line. A pull that finds the image there and so writes no layer is one item, done. Lines that sind does not know are ignored. Only `pull images` counts layers: the pulls that the `docker create` of the mesh's containers makes show their last line alone.

## Event log

Every command that reports progress appends its events to a log. `--progress-log FILE`, or `SIND_PROGRESS_LOG` when the flag is not given, names the file; without either, sind writes the [activity log](#the-activity-log) in its state directory. The log holds, one JSON object per line, the progress events of the command, in go-clikit's [event log format](https://github.com/GSI-HPC/go-clikit/blob/v0.4.0/doc/event-log.md), version 1: what started and ended when, under which step or target, and how it ended and why, with each docker command by its subcommand alone and its exit code. It holds neither the command line nor the environment of the command, and of what the programs sind runs write, only what an error quotes; the `realm lock` wait names the command that holds the lock, its command line and host, as the `Warning:` line does. It works with every `--progress` mode, `none` included, and writes nothing on standard error.

- sind creates the file readable and writable by you alone (mode `0600`) and appends to it, so the commands of a CI job can share one log; each command's lines carry a `run` of their own.
- An existing file must be yours, have no other name (no hard link to it) and be readable or writable by nobody else: sind refuses one that is not, and for one that others can read or write says `run chmod 600 FILE`. A symbolic link on the way to it, and a named pipe, must be yours or root's: sind follows no link put at the file's name after it looked, and refuses another user's named pipe at once instead of waiting for someone to read it. `/dev/stderr`, `/dev/stdout` and `/dev/fd/N` are written to where that stream goes; on the terminal the tree is drawn on, the lines go above the tree, as the `-v` logs do.
- A file named by `--progress-log` that sind cannot use is a usage error, before the command does anything. One named by `SIND_PROGRESS_LOG` is not: sind says why in one line and runs without a log. `--progress-log ""` writes no log, whatever the variable says.
- A log that cannot be written while the command runs, such as on a full disk, does not fail the command: sind says once that the log stops short.
- Only `create`, `delete`, `connect`, `disconnect` and `power` open the file; other commands leave it alone.

### The activity log

Without `--progress-log` and `SIND_PROGRESS_LOG`, sind appends the events to `activity.jsonl` in its state directory, `$XDG_STATE_HOME/sind/` or `~/.local/state/sind/`, so that a tool that watches the state directory sees what every sind command did, whichever way it was started. It is the same log as above, in the same format, and it changes nothing on standard output and standard error.

- sind creates the directory for you alone (mode `0700`) and the file readable and writable by you alone (mode `0600`). The same rules for an existing file hold: it must be yours, have no other name and be readable and writable by nobody else.
- Before a command appends, sind moves a log larger than 8 MiB to `activity.jsonl.1`, replacing the copy that was there, so the two files together stay under about 16 MiB. Commands that run at once rotate it once.
- A log that sind cannot use, such as one in a read-only home directory, is left out without a word, and never fails the command; the debug log (`-vv`) says why.
- `--progress-log ""`, or `SIND_PROGRESS_LOG=` with an empty value, turns it off; a file named by either replaces it.

When sind is started with a W3C trace context in `TRACEPARENT` (and `TRACESTATE`), the log continues that trace. While sind shows progress or writes a log, it removes both variables from the environment of the programs it runs, such as docker; otherwise they inherit them.

## Scripts, CI and MCP

Where standard error is not a terminal, nothing changes unless you ask: no tree and no summary. [sind-action](https://github.com/GSI-HPC/sind-action) and scripts that read sind's standard error see what they did before; the [activity log](#the-activity-log) is the only addition, and it is a file. `--progress plain` adds lines to a CI log, and `--progress-log` keeps the events as a job artifact; see [CI/CD]({{< relref "/guides/ci-cd#progress-in-ci-logs" >}}).

The [MCP server]({{< relref "/guides/mcp" >}}) draws no progress: its tools take neither flag, and it runs its tool calls with `SIND_PROGRESS=none` and without `SIND_PROGRESS_LOG`, whatever its own environment says, so a tool call that reports progress writes the [activity log](#the-activity-log) and nothing on its standard error. A call that comes with a progress token gets its progress as MCP progress notifications instead, read back from that log (see [MCP]({{< relref "/guides/mcp" >}})).
