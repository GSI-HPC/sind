---
weight: 340
title: "Web UI"
description: "Watch and change your clusters in a browser with sind ui"
aliases:
  - /guides/web-ui/
---

`sind ui` starts a web UI for sind on your machine. It shows the clusters, nodes and networks of a realm and the runs of sind commands. You can also start the actions that change something: create a cluster, add workers, power nodes, or delete. Each action shows the exact `sind` command first, so the UI also teaches the CLI.

The UI runs the same sind commands as your terminal. It does not replace them. Long commands show their progress live, step by step and node by node.

{{< callout type="info" >}}
The UI has no terminal and no Slurm page in this version. To open a shell on a node, copy the command from the Connect tab of a cluster and run it in your own terminal.
{{< /callout >}}

The pages of the docs that tell you how to do a task show the steps for the CLI or for the web UI. Select **Web UI** at the top of such a page, or on one of its tabs, to see the steps for the UI on all pages. Your browser keeps the choice. The help pane of `sind ui` opens the docs with the steps for the UI.

## Start it

```bash
sind ui
```

sind prints the URL and opens it in your browser:

```
sind ui: http://127.0.0.1:41873/?token=...
Press Ctrl-C to stop.
```

The UI runs until you press Ctrl-C in that terminal. Pressing Ctrl-C stops the server. Commands that the UI started are interrupted, and they roll back (see [Stop a run]({{< relref "/web-ui/runs#stop-a-run" >}})).

Useful flags:

| Flag | Effect |
|------|--------|
| `--port PORT` | Listen on this port on `127.0.0.1`. By default sind takes a free one. |
| `--no-open` | Print the URL, but do not start a browser. |
| `--monitoring-interval DURATION` | How often to read the monitoring stacks while a page is open, from `5s` to `1h`. The default is `30s`. |
| `--demo` | Show made-up clusters, and play the commands instead of running them. It uses no Docker and changes nothing. |

The UI listens on `127.0.0.1` only. Only your user can open it. To use it from another machine, forward the port over SSH and use the same port on both ends (see [Use it from another machine]({{< relref "/web-ui/security#use-it-from-another-machine" >}})).

{{< uishot "overview" "The Overview of two realms. The task dock at the bottom right shows a run that goes on." >}}

## The parts of the window

- **The rail** on the left lists the pages. A number shows how many items a page has, or how many need you.
- **The top bar** has the jump box, which finds a cluster, a node, a page or a help entry, and the realm switch. **live** shows that the page gets updates. `?` opens the help for the page you are on, and ◐ turns the dark theme on or off.
- **The task dock** at the bottom right shows the runs that go on (see [Runs and Progress]({{< relref "/web-ui/runs" >}})).
- A dotted word explains itself when you point at it.
- **Skip to content** is the first stop of the Tab key. It skips the top bar and the rail.

{{< uishot "help" "The help pane tells what the page shows, explains its words and links to these docs." >}}

To try the UI without Docker, run `sind ui --demo`. It shows made-up clusters, and its dialogs play the commands instead of running them. The screenshots in these docs come from it.
