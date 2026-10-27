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

sind prints the URL. Open it in your browser, or add `--open` to have sind open it:

```
sind ui: http://127.0.0.1:41873/?token=...
Press Ctrl-C to stop.
```

The UI runs until you press Ctrl-C in that terminal. Pressing Ctrl-C stops the server. Commands that the UI started are interrupted, and they roll back (see [Stop a run]({{< relref "/web-ui/runs#stop-a-run" >}})).

Useful flags:

| Flag | Effect |
|------|--------|
| `--port PORT` | Listen on this port on `127.0.0.1`. By default sind takes a free one. |
| `--open` | Also open the URL in your browser. |
| `--monitoring-interval DURATION` | How often to read the monitoring stacks while a page is open, from `5s` to `1h`. The default is `30s`. |
| `--demo` | Show made-up clusters, and play the commands instead of running them. It uses no Docker and changes nothing. |

The UI listens on `127.0.0.1` only. Only your user can open it. To use it from another machine, forward the port over SSH and use the same port on both ends (see [Use it from another machine]({{< relref "/web-ui/security#use-it-from-another-machine" >}})).

{{< uishot "overview" "The Overview of two realms. The task dock at the bottom right shows a run that goes on." >}}

To try the UI without Docker, run `sind ui --demo`. It shows made-up clusters, and its dialogs play the commands instead of running them. The screenshots in these docs come from it.

## The parts of the window

- **The rail** on the left lists the pages. A badge says what needs you, with its unit: `1 down` for nodes, `1 warning` or `1 failed` for the host checks, `1 failed` for runs. A green dot means that all is well. **Clusters** shows how many clusters there are. On a phone, and in a window that is not high, the rail is a bar at the bottom of the screen: Overview, Clusters, Nodes, Doctor and **More**, which opens the other pages.
- **The top bar** has the jump box, which finds a cluster, a node, a page or a help entry, and the realm switch. **live** shows that the page gets updates. `?` opens the help for the page you are on, and ◐ turns the dark theme on or off.
- **The realm switch** shows the realm of the page. Pick a realm on the Overview, Clusters, Nodes, Mesh and DNS, Monitoring, Topology or Activity, and the page shows only that realm. The page says **Showing realm ci**, and its address has `?realm=ci`, so a link that you share shows the same. On a cluster or node page, a pick opens the list of clusters or nodes of that realm. With the keyboard, the arrow keys go through the realms, and Enter shows the one you reached. On a Mac, the arrow keys open the list, and the realm you pick there shows at once. The UI remembers the realm you picked last: Clusters and the other lists open with it, and **Show all** shows every realm again.
- **Connection lost** shows at the top when the page stops getting updates, for example because `sind ui` stopped. The page then dims, and its buttons wait until it is live again. See [Connection lost]({{< relref "/web-ui/troubleshooting#connection-lost" >}}).
- **The task dock** at the bottom right shows the runs that go on (see [Runs and Progress]({{< relref "/web-ui/runs" >}})).
- A dotted word explains itself when you point at it, tap it or give it the focus. Services such as slurmctld and slurmd, node roles such as submitter, and words of sind such as realm are dotted.
- **Skip to content** is the first stop of the Tab key. It skips the top bar and the rail.

## Find things and get help

The jump box finds what you type in the names of clusters, nodes, pages and runs, and in the help: type `sbatch`, `clean up` or `stuck` to find the how-to for it. Clusters and pages come before nodes. When the list holds only some of the results, its last line says how many there are. Start with `?` to search the help only. Without a query, the box lists up to five clusters, the actions that you use most (**New cluster**, **Add workers** and **Power**) and the pages; actions that cannot be undone show only when you type their name.

An action that needs a cluster, such as **Add workers** or **Power**, asks for it next: the box lists the clusters, the one of the page you are on first, and opens the dialog of the cluster you pick. Backspace in the empty box goes back. An action of a realm, such as **Delete realm**, asks for the realm when there is more than one.

The help pane shows the help of the page: what it shows, the words on it and how to do the common things. The **?** button of a dialog opens the help of that dialog, above it. A pane that you left open stays beside a dialog that opens later, or behind it on a narrow screen. The pane takes the focus, and Esc closes it and takes you back to where you were. On a phone the pane fills the screen, and Tab and a screen reader stay in it until you close it. **All how-tos** and **All words** list every how-to and every word, such as how to see the output of a job, what to do when a job does not start, and how to clean up when you are done.

The address `/help` opens the same texts as a page of their own, with a search that works without script, so that you can bookmark or share an entry.

{{< uishot "help" "The help pane tells what the page shows, explains its words and links to these docs." >}}

## Keys

| Key | What it does |
|-----|--------------|
| `/` or `Ctrl+K` | Open the jump box. On a Mac, `⌘ K`. On Clusters and Nodes, `/` goes to the filter of the list instead. |
| `?` | Help for the item under the pointer or with the focus, else for the open dialog or the page |
| `g` then `o`, `c`, `n`, `m`, `p`, `t`, `d` or `a` | Go to Overview, Clusters, Nodes, Mesh and DNS, Monitoring, Topology, Doctor or Activity |
| `↑` `↓`, `Enter` | Move through the results of the jump box, and open one |
| `Ctrl+Enter` | Run the command of the dialog. On a Mac, `⌘ Enter`. |
| `Esc` | Close the hint card, the jump box, the help pane or the dialog. A dialog with changes asks first. |

Keys do nothing while you type in a field, except `Esc`, `Ctrl+K` and `Ctrl+Enter`. To turn off the shortcuts of one key (`/`, `?` and the `g` keys), as speech input can press them by accident, open **Keys** in the help pane, or type `keys` in the jump box, and clear **Single-key shortcuts**. `Ctrl+K` and `Esc` still work, and the buttons and hint cards stop showing `/` and `?`. Your browser keeps the setting.
