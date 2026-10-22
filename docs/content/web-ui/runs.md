---
weight: 343
title: "Runs and Progress"
description: "Follow and stop the commands that sind ui and your terminal run"
toc: true
---

When you start an action, its run appears in the task dock, on its run page and on the page of its cluster.

## The task dock

The task dock at the bottom right shows a card with the command, a spinner, a bar with one part for each step, and the current step, such as `nodes 3/8 · worker-2 ready munge`. A card links to the run page. Use **Show progress** to open it. Use **Stop** to stop the run. A card of a run that has ended stays until you hide it with ×.

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

{{< uishot "run-going" "A run that goes on. Two workers wait for munge." >}}

{{< uishot "run-failed" "A run that failed. The page says what failed, that the rollback removed everything, and what to try." >}}

The cluster page shows new nodes as dashed tiles while they start, and a banner that links to the run.

## Runs from a terminal

The progress comes from the activity log. sind writes it by default, in `activity.jsonl` in its state directory (see [The activity log]({{< relref "/usage/progress#the-activity-log" >}})). sind ui reads that log, so a command that you start in a terminal also shows up in the UI. Its run is marked with a **terminal** label. sind ui can show it, but it cannot stop it. Press Ctrl-C in that terminal to stop it.

The **Activity** page lists the runs, from this UI and from a terminal.

{{< uishot "activity" >}}

## Stop a run

Press **Stop** on the task card or on the run page. sind ui sends SIGINT to the command, the same signal as Ctrl-C in a terminal. The command then rolls back what it has made, and the run ends as canceled. The button shows **Stopping…** until the rollback ends. Steps that have not started do not start.

**Stop** asks you to confirm first.

**Stop** sends SIGINT only, never SIGKILL. If you stop the UI while a command runs, sind ui interrupts the command and waits up to 60 seconds for the rollback. Only after that does it kill the command. A command that runs from a terminal is not stopped by the UI at all.
