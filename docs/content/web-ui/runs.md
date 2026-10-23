---
weight: 343
title: "Runs and Progress"
description: "Follow and stop the commands that sind ui and your terminal run"
toc: true
---

When you start an action, its run appears in the task dock, on its run page and on the page of its cluster.

## The task dock

The task dock at the bottom right shows one card at a time: the command that works now, with a spinner, a bar with one part for each step, and the current step, such as `nodes 3/8 · worker-2 ready munge`. The title of a card is the command with its main flags, such as `create worker dev --count 3`, and the realm beside it; point at it to see the whole command. A card links to the run page. Use **Show progress** to open it, or **Details** for a run that has ended. A screen reader hears once when a command starts, ends or fails; the clock and the current step are not read out. Use **Stop…** to stop the run, on a phone too.

When there is more than one command, a bar above the card sums them up, such as `2 running · 1 failed · 3 done`. **Show all** shows every card, and **Show one** goes back to one. A card of a command that ended ok hides after a few seconds. A card of a command that failed or was stopped stays until you hide it with ×. It keeps to one line, such as the first line of the error; **Show what failed** opens the run page with the whole error and what the command wrote. **Clear done** hides the cards of all commands that have ended. Hiding a card keeps the run: Activity lists it, and its run page still shows its command and **Try again**. The page of a run leaves out the card of that run, and the bar still counts it.

{{< uishot "task-dock" >}}

The browser tab title shows the step that is running, such as `▸ nodes 3/8 · sind ui`. You can see the progress in another tab.

## The run page

The run page shows the whole run:

- **Summary:** the time that has passed, the step bar, and how many targets are in each state, such as running, ok or failed.
- **What failed:** the errors of the run, grouped by text, with the nodes they happened on. It also says whether the rollback removed everything. For the logs of a failed service, open the Logs tab of the cluster.
- **Steps:** a timeline. Steps that overlap ran at the same time.
- **Nodes:** a tile for each node, with its role, the check that it waits for, and a ring that shows how much of the wait limit it has used. The ring turns amber at 50 % and red at 80 %.
- **Transfers:** the work done of the work to do, for the steps that report it, such as image pulls.
- **What the event log says about this run:** the facts that sind wrote for this run, for a bug report.

A run that sind ui started also shows its command, with **Copy**, and the config it ran with, under **Config used**. sind ui keeps these for the last 25 commands that ended, until it stops. A button leads back to what the command worked on: **Open dev** for the cluster, **Open Clusters** once the cluster is gone, or **Open Mesh and DNS**.

When the run failed or was stopped, **Try again** runs the same command again. If the command would now differ, as when a cluster changed in between, sind ui opens the dialog instead, so that you can check it. **Edit and try again…** opens the dialog with the fields of the run filled in. A delete, which asks you to type a name, has **Try again…**, which opens its dialog.

{{< uishot "run-going" "A run that goes on. Two workers wait for munge." >}}

{{< uishot "run-failed" "A run that failed. The page says what failed, that the rollback removed everything, and what to try." >}}

The cluster page shows new nodes as dashed tiles while they start, and one banner for the runs that work on the cluster. It names the command, or says how many commands run and how many of them wait for the realm lock. **Show progress** opens the run.

## Runs from a terminal

The progress comes from the activity log. sind writes it by default, in `activity.jsonl` in its state directory (see [The activity log]({{< relref "/usage/progress#the-activity-log" >}})). sind ui reads that log, so a command that you start in a terminal also shows up in the UI. Its run is marked with a **terminal** label. sind ui can show it, but it cannot stop it. Press Ctrl-C in that terminal to stop it.

A run that another program started, such as a CI job or an MCP client that hands sind a trace, is marked with an **other program** label. A run that sind ui started stays marked as **from sind ui**, also after you restart sind ui.

The **Activity** page lists the runs, from this UI and from a terminal. A run of this UI shows its command with the main flags, such as `create worker dev --count 3 --cpus 4`, and its realm. To find a run, type a part of its command, a cluster, a flag or its run ID in the filter field and press Enter. The tabs filter by status, and the chips by realm and by cluster; press a chip again to turn it off. **Clear filter** shows all runs again.

When several commands run on a cluster, the banner of the cluster page has **Show them**, which opens Activity with the runs of that cluster that go on.

{{< uishot "activity" >}}

## Stop a run

Press **Stop…** on the task card or on the run page. The page asks you to confirm, and says what stays when the command stops:

- A create removes the nodes or workers it has made so far (rollback). The saved config stays.
- A delete keeps what it has removed so far. The rest stays, and you can delete it again.
- A power command leaves the nodes it has handled as they are now.
- A command that still waits for the lock of its realm has changed nothing yet.

Press **Stop it** to stop the command, or **Keep running** (or Escape) to go back. sind ui sends SIGINT to the command, the same signal as Ctrl-C in a terminal. The command stops as the list above says, and the run ends as canceled. The button shows **Stopping…** until the command has stopped, which for a create includes its rollback. Steps that have not started do not start.

{{< uishot "run-canceled" "A run that was stopped. The page says what stopped and that the rollback removed everything." >}}

**Stop** sends SIGINT only, never SIGKILL. If you stop the UI while a command runs, sind ui interrupts the command and waits up to 60 seconds for the rollback. Only after that does it kill the command. A command that runs from a terminal is not stopped by the UI at all.
