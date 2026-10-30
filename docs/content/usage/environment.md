---
weight: 380
title: "Environment Variables"
description: "The SIND_* variables that stand in for flags, and the other variables sind reads"
toc: true
webui: none
webuiNote: "sind ui runs its commands without the `SIND_*` variables that stand in for a flag or an argument, so that a run does what its command says. The variables on this page are for the CLI."
---

A `SIND_*` variable stands in for a flag, or for an argument, that the command line does not give. Set it once, in a shell profile or for a CI job, and every command that follows uses it:

```bash
export SIND_REALM=ci-42
sind create cluster
sind get clusters        # the clusters of realm ci-42
```

## Precedence

sind takes a value from the first of these that gives it, the same way for every variable:

| Source | Example |
|--------|---------|
| The command line | `sind --realm ci-42 get clusters` |
| The `SIND_*` variable | `export SIND_REALM=ci-42` |
| The config file (`sind create cluster` only) | `realm: ci-42` in the YAML config |
| The default | `sind` |

A variable set to the empty string counts as not set, except `SIND_PROGRESS_LOG`, whose empty value writes no event log. A flag given with an empty value counts as given: `--progress-log ""` writes no event log, whatever `SIND_PROGRESS_LOG` says.

The help of each flag names the variable that stands in for it, before the default:

```console
$ sind --help
...
      --realm string          realm namespace for resource isolation (default: SIND_REALM, else the realm of a config file, else sind)
```

## SIND_* variables

| Variable | Stands in for | Default | See |
|----------|---------------|---------|-----|
| `SIND_REALM` | `--realm` | the `realm` of the config file, else `sind` | [Realms]({{< relref "/configuration/realms" >}}) |
| `SIND_CLUSTER` | the cluster argument, `NAME` or `CLUSTER`, and the cluster of a node argument that names none | `default`; for `sind create cluster` the `name` of the config file, else `default` | [The cluster](#the-cluster) |
| `SIND_PROGRESS` | `--progress` | `auto` | [Progress Display]({{< relref "/usage/progress" >}}) |
| `SIND_PROGRESS_LOG` | `--progress-log` | the [activity log]({{< relref "/usage/progress#the-activity-log" >}}) | [Progress Display]({{< relref "/usage/progress#event-log" >}}) |
| `SIND_LOG_TIMESTAMPS` | no flag: `false` leaves the time stamp off log lines | time stamps | [MCP]({{< relref "/guides/mcp" >}}) |
| `SIND_MCP_TOKEN` | no flag: the bearer token of `sind mcp stream`, which a command line would show to the other users of the host | a new random token at every start | [MCP]({{< relref "/guides/mcp" >}}) |

## The cluster

`SIND_CLUSTER` names the cluster of every command that takes a cluster or nodes and names none, so a shell that works on one cluster does not repeat its name:

```bash
export SIND_CLUSTER=dev
sind create cluster           # creates dev
sind ssh worker-0             # worker-0.dev
sind exec -- sinfo            # runs sinfo in dev
sind power off worker-1.prod  # worker-1 of prod: the node names its cluster
sind get nodes                # the nodes of every cluster, as without SIND_CLUSTER
```

As `SIND_REALM` wins over the `realm` of a config file, `SIND_CLUSTER` wins over its `name`: `sind create cluster --config lab.yaml` creates `dev`, not the cluster `lab.yaml` names. Give the name as `NAME` to create that one: `sind create cluster lab --config lab.yaml`.

## Values sind cannot use

A variable is not part of the command line, so a value that sind cannot use is no usage error, which exits `2` (see [Exit Status]({{< relref "/usage/exit-status" >}})):

- A variable that changes where or how a command acts fails the command with exit status `1`, and the error names the variable:

  ```console
  $ SIND_REALM=ci_42 sind get clusters
  12:00:00.000 ERRO SIND_REALM: invalid realm name "ci_42": '_' is not a letter, a digit or a hyphen
  $ echo $?
  1
  ```

  sind checks the variable only when it is in effect: `sind --realm ci get clusters` does not read `SIND_REALM`, `sind version` reads no realm, and `sind ssh worker-0.dev` reads no `SIND_CLUSTER`.

- A variable that changes only what sind shows on standard error fails no command. With a value of `SIND_PROGRESS` or `SIND_PROGRESS_LOG` that sind cannot use, it shows no progress, or writes no event log, and says why in one line.

## sind ui and the MCP server

`sind ui` runs the commands of its dialogs without the variables that stand in for a flag or an argument, whatever its own environment holds, so that a run does what the command it shows says. The commands it shows leave out the realm `sind` and the cluster `default`, as you would type them, so in a shell with `SIND_REALM` or `SIND_CLUSTER`, a copy of one acts in the realm or on the cluster that these name. Some always name them: **Copy script** of New cluster names the realm and the cluster, Delete realm the realm, Delete cluster and Delete clusters each cluster, and Delete workers the cluster of the workers, `default` too.

The [MCP server]({{< relref "/guides/mcp" >}}) runs its tool calls in its own environment, with these changes: `--realm` of `sind mcp start` or `sind mcp stream`, when given, sets `SIND_REALM`; `SIND_PROGRESS` is `none`, `SIND_PROGRESS_LOG` is not set, and `SIND_LOG_TIMESTAMPS` is `false`. An editor starts the server with the environment of its entry, not that of your shell: `sind mcp <editor> enable` writes the realm into the entry.

## Other variables

sind also reads these variables, which other programs define:

| Variable | What it changes |
|----------|-----------------|
| `DOCKER_HOST`, `DOCKER_CONTEXT` | The daemon that the docker CLI, which sind runs, reaches. sind supports only a daemon on this machine (see [Docker host]({{< relref "/usage/diagnostics#docker-host" >}})). |
| `XDG_STATE_HOME` | The state directory, `$XDG_STATE_HOME/sind/`, else `~/.local/state/sind/`: SSH files, realm locks, the activity log |
| `XDG_RUNTIME_DIR` | Where `sind ui` records the running UI (see [Troubleshooting]({{< relref "/web-ui/troubleshooting" >}})) |
| `NO_COLOR`, `TERM`, `COLORTERM` | The colours of the progress display and of log lines: `NO_COLOR` turns them off, and `TERM=dumb` writes plain text (see [Progress Display]({{< relref "/usage/progress" >}})) |
| `LC_ALL`, `LC_CTYPE`, `LANG` | A UTF-8 locale draws the progress tree with Unicode marks, else ASCII |
| `TRACEPARENT`, `TRACESTATE` | The W3C trace that the event log continues |
| `SSH_CONNECTION`, `DISPLAY`, `WAYLAND_DISPLAY` | Whether `sind ui --open` can open a browser |
