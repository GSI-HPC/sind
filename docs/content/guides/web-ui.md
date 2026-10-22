---
weight: 361
title: "Web UI"
description: "Watch and change your clusters in a browser with sind ui"
toc: true
---

`sind ui` starts a web UI for sind on your machine. It shows the clusters, nodes and networks of a realm and the runs of sind commands. You can also start the actions that change something: create a cluster, add workers, power nodes, or delete. Each action shows the exact `sind` command first, so the UI also teaches the CLI.

The UI runs the same sind commands as your terminal. It does not replace them. Long commands show their progress live, step by step and node by node.

{{< callout type="info" >}}
The UI has no terminal and no Slurm page in this version. To open a shell on a node, copy the command from the Connect tab of a cluster and run it in your own terminal.
{{< /callout >}}

## Start it

```bash
sind ui
```

sind prints the URL and opens it in your browser:

```
sind ui: http://127.0.0.1:41873/?token=...
Press Ctrl-C to stop.
```

The UI runs until you press Ctrl-C in that terminal. Pressing Ctrl-C stops the server. Commands that the UI started are interrupted, and they roll back (see [Stop a run](#stop-a-run)).

Useful flags:

| Flag | Effect |
|------|--------|
| `--port PORT` | Listen on this port on `127.0.0.1`. By default sind takes a free one. |
| `--no-open` | Print the URL, but do not start a browser. |
| `--monitoring-interval DURATION` | How often to read the monitoring stacks while a page is open, from `5s` to `1h`. The default is `30s`. |
| `--demo` | Show made-up clusters, and play the commands instead of running them. It uses no Docker and changes nothing. |

The UI listens on `127.0.0.1` only. Only your user can open it. To use it from another machine, forward the port over SSH and use the same port on both ends (see [Use it from another machine](#use-it-from-another-machine)).

## What each page shows

The rail on the left lists the pages. The top bar has the realm switch. The task dock at the bottom right shows the runs in progress. A dotted word explains itself when you point at it, and `?` opens help for the page you are on.

| Page | What it shows |
|------|---------------|
| Overview | A card for each cluster, the things that need you, and the recent activity. An empty realm says so and offers New cluster. |
| Clusters | Every cluster of the realm. sind keeps no list of clusters. It finds them by their Docker labels. |
| Nodes | Every node of the realm. A node is named as you type it in commands, such as `worker-0.dev`. |
| Cluster | The state of one cluster, with the tabs Summary, Nodes, Logs, Network and storage, and Connect. |
| Node | The services, events and power state of one node. |
| Mesh and DNS | The mesh network, the DNS server and its records, and the containers that are connected to the realm. |
| Monitoring | The state and addresses of Grafana, Prometheus, Loki and Alloy, if the realm runs them. Use **read every** to set how often sind ui reads them. The change lasts until sind ui stops. |
| Topology | A diagram from the host, through the mesh services, to the cluster networks and the nodes. |
| Doctor | The host checks of `sind doctor`, each as ok, warning or failed, with the fix for each. |
| Activity | The runs of sind commands that change something, from this UI or from a terminal. |
| Run | One run: its summary, what failed, steps, nodes and transfers. |

The cluster page has these tabs:

- **Summary** shows a matrix of nodes and services, such as munge, sshd and slurmctld, and the network checks.
- **Nodes** lets you select nodes. The selection appears as a node set, such as `worker-[0-1,3].dev`. Power and delete act on the selection.
- **Logs** shows the container log of a node, or the journal of one of its services. It starts with the last 200 lines and keeps the last 2,000. Use **Pause** to stop the stream and **Follow** to resume it.
- **Network and storage** lists the networks, volumes and mounts of the cluster.
- **Connect** shows the commands to open a shell on a node, to run a command, and to use `ssh`. Use **Copy** to copy one.

The pages update by themselves. A cluster page refreshes its state while it is open, and the Overview and the lists update when Docker reports a change.

## Run an action

Action buttons open a dialog. The dialog shows:

- the fields of the action, such as the number of workers or the wait limit;
- the **Runs** line, which is the exact `sind` command;
- a **Copy** button for that command;
- whether you can undo the action.

Press the button of the dialog, such as **Create**, **Add**, **Power** or **Delete**, to start the command. The UI checks the fields against the cluster as it is now. It also builds the command again when you press the button. If that command differs from the one in the dialog, the UI refuses to start it.

You can copy the command and run it in a terminal. It makes the same change. This is useful to learn the CLI, to script the change, or to run it again later. In the New cluster dialog, **Copy script** gives a shell script that does all that Create does: it saves the config, creates the data directory if needed, and runs the command with `--realm`, as `SIND_REALM` in your shell would otherwise take the place of the config's realm.

The actions are:

| Action | Command | Undo |
|--------|---------|------|
| New cluster | `sind create cluster --config FILE` | Delete the cluster |
| Add workers | `sind create worker CLUSTER --count N` | Delete the workers |
| Delete cluster | `sind delete cluster NAME` | Cannot be undone |
| Delete workers | `sind delete worker NODES` | Cannot be undone |
| Power | `sind power VERB NODES` | Use the opposite verb |
| Connect or disconnect a container | `sind connect container NAME` | Disconnect or connect it again |
| Delete realm | `sind delete realm` | Cannot be undone |

Notes on specific actions:

- **New cluster** takes the name, the image, the nodes, the node defaults, the users, groups and Slurm accounts, the data storage, the wait for ready and whether to pull the image. Every cluster has one controller. A checkbox adds each of the other roles (backup controller, db, api, submitter), and a field with − and + sets the number of workers. **+ Add worker group** adds a group of workers with a size of its own. The node defaults are Small, Medium, Large or your own CPUs, memory and `/tmp` size; every node takes them. The size button of a node opens its own fields: image, CPUs, memory and `/tmp`, and under **More** unmanaged, capabilities, devices and security options. An empty field takes the node defaults, which it shows in grey. The name is a free one of the realm, such as `hpc` or `hpc-2`, and the data directory follows the name until you change it. In Users and accounts, each user, group and Slurm account is a row with its name; an empty UID or GID takes the next free one from 1000. **More** on a user gives its primary group, groups, the accounts it coordinates and its Slurm admin level. An account has a parent and limits, each a pair of a sacctmgr option, such as `MaxJobs`, and its value. Slurm accounts need the db node. In Run options, the wait for ready is 2, 5 or 10 minutes, no limit, or a time you type, such as `15m`. **Pull the image** is on for an image of a registry whose tag can move to a newer image, such as `latest` or `26.05`, and off for a fixed tag, a digest or an image you built. It follows the image until you tick or clear it. The data storage, in the Data section, is a directory on the host or a Docker volume, mounted at `/data` or at the path you give. sind ui writes it into the config, so the cluster never uses the directory you started the UI in by accident. Docker mounts only a directory that exists: sind ui creates a missing one first, unless you clear **Create the directory if it is missing**; the directory stays if the create fails. The UI saves the cluster config in `~/.local/state/sind/ui/configs/`, with mode `0600`, so you can run the same command again from a terminal.
- The plan beside the form shows the nodes, the command and the config as you type. A wrong field gets its message below it and a line in the plan, which links to the field. The plan also lists every error that sind finds in the config, all at once, where sind itself stops at the first. The plan then takes the default for a wrong field, so it never goes blank, and the status at the bottom says `Not ready` with the number of problems. Create works only when it says `Ready`. The plan also adds up the CPUs and memory of all nodes, shows the memory against that of the Docker host, and warns when the nodes may use more than 80% of it.
- **Power** offers the verbs that fit the state of the chosen nodes. Power-cycle and cut power kill the containers at once. Run them only when you need to.
- **Delete cluster**, **Delete workers** and **Delete realm** ask you to type the name of the cluster or realm to confirm.
- A dialog that changes a realm with a running command shows which command holds the realm lock. Your command waits until the other one ends. See [Troubleshooting](#a-command-waits-for-the-realm-lock).

## Progress

When you start an action, its run appears in three places:

- **The task dock** at the bottom right shows a card with the command, a spinner, a bar with one part for each step, and the current step, such as `nodes 3/8 · worker-2 ready munge`. A card links to the run page. Use **Show progress** to open it. Use **Stop** to stop the run. A card of a run that has ended stays until you hide it with ×.
- **The run page** shows the whole run:
  - **Summary:** the time that has passed, the step bar, and how many targets are in each state, such as running, ok or failed.
  - **What failed:** the errors of the run, grouped by text, with the nodes they happened on. It also says whether the rollback removed everything. For the logs of a failed service, open the Logs tab of the cluster.
  - **Steps:** a timeline. Steps that overlap ran at the same time.
  - **Nodes:** a tile for each node, with its role, the check that it waits for, and a ring that shows how much of the wait limit it has used. The ring turns amber at 50 % and red at 80 %.
  - **Transfers:** the work done of the work to do, for the steps that report it, such as image pulls.
  - **What the event log says about this run:** the facts that sind wrote for this run, for a bug report.
- **The cluster page** shows new nodes as dashed tiles while they start, and a banner that links to the run.

The progress comes from the activity log. sind writes it by default, in `activity.jsonl` in its state directory (see [The activity log]({{< relref "/usage/progress#the-activity-log" >}})). sind ui reads that log, so a command that you start in a terminal also shows up in the UI. Its run is marked with a **terminal** label. sind ui can show it, but it cannot stop it. Press Ctrl-C in that terminal to stop it.

The browser tab title shows the step that is running, such as `▸ nodes 3/8 · sind ui`. You can see the progress in another tab.

## Stop a run

Press **Stop** on the task card or on the run page. sind ui sends SIGINT to the command, the same signal as Ctrl-C in a terminal. The command then rolls back what it has made, and the run ends as canceled. The button shows **Stopping…** until the rollback ends. Steps that have not started do not start.

**Stop** asks you to confirm first.

**Stop** sends SIGINT only, never SIGKILL. If you stop the UI while a command runs, sind ui interrupts the command and waits up to 60 seconds for the rollback. Only after that does it kill the command. A command that runs from a terminal is not stopped by the UI at all.

## Security

The UI can do what your Docker access can do, so sind protects it in these ways:

- **Only your user.** The UI refuses connections from processes of other users before it reads any request. This works on Linux only.
- **A token in the URL.** Each run prints a new token. The first visit with the token sets a cookie for this browser, and the address bar then shows the page without the token. A visit with an old or wrong token is refused.
- **Your own pages only.** The UI accepts requests for `127.0.0.1` only, not for `localhost`. A request that changes something must come from the UI itself, with its CSRF token. A web page in another tab cannot start an action.
- **No secrets.** The UI has no page for the munge key, the JWT key or the SSH private key.
- **No outside resources.** The UI loads its scripts, styles and fonts from itself only. No other site can show it in a frame.

## Troubleshooting

### Port in use

```
sind ui: port 8080 is in use; pick another with --port, or leave it out
```

Another program listens on that port. Run `sind ui --port 8081`, or leave `--port` out so that sind takes a free port.

### A second sind ui opens the first

```
sind ui is already running: http://127.0.0.1:41873/?token=...
```

sind ui recorded the running UI in `$XDG_RUNTIME_DIR/sind/ui.json`. The second command opens that UI in your browser and exits. If the browser does not open, copy the URL into it. Normally only one UI runs for your user at a time.

To start a new one, press Ctrl-C in the terminal of the running UI. If the UI is gone but the record is left over, the next `sind ui` starts a new one.

If `XDG_RUNTIME_DIR` is not set, sind ui keeps no record, so each `sind ui` starts its own UI on its own port.

### Docker is not reachable

The Overview shows **Docker is not reachable**. The pages show the data that sind ui read last. Open **Doctor** for the cause and the fix. Or run `sind doctor` in a terminal, as described in [Diagnostics]({{< relref "/usage/diagnostics" >}}).

### The page says the link is old or wrong

The token is new with each run. Use the URL that the running `sind ui` printed, or the one that the second command printed. A bookmark of an older run does not work. A tab of an older run that is still open is refused too, as its session is gone. Open the new URL.

### Opened by another host name

```
sind ui answers only to 127.0.0.1:41873
```

The browser sent another host name, so the UI refused the request. Open the URL that sind printed, with `127.0.0.1` and the same port. A URL with `localhost` does not work: a browser can send it to `::1`, where sind ui does not listen.

### The browser does not open

Use `--no-open`, or copy the URL that sind prints into a browser on this machine.

### A run says "no end recorded"

The activity log has no end for this run, and its last event is more than an hour old. Its command was most likely killed, for example with `kill -9`, so it could not write its end. Check with `sind get cluster` what the command left, and delete it if needed. A run that sind ui started ends when its command ends, with the outcome of the command.

### A command waits for the realm lock

The Overview says that a command holds the lock of the realm, and it names that command. Your command goes on when the other one ends. Open **Activity** to see the runs. If the other command hangs, stop it: press **Stop** if the UI started it, or press Ctrl-C in its terminal. See [Realms]({{< relref "/configuration/realms" >}}) for the lock.

## Use it from another machine

The UI does not accept connections from other hosts. To use it from a laptop, start `sind ui --no-open` on the host and forward its port over SSH:

```bash
# on the laptop
ssh -L 41873:127.0.0.1:41873 user@host
```

Use the same port on both ends. The UI checks the host name that the browser sends, so a different local port does not work. Then open the URL that the host printed, `http://127.0.0.1:41873/?token=...`, on the laptop.
