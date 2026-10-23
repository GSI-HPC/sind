---
weight: 342
title: "Actions"
description: "Create, change and delete with the dialogs of sind ui"
toc: true
---

## Run an action

Action buttons open a dialog. The dialog shows:

- the fields of the action, such as the number of workers or the wait limit;
- the **Runs** line, which is the exact `sind` command;
- a **Copy** button for that command;
- whether you can undo the action.

Press the button of the dialog, such as **Create**, **Add 3 workers**, **Reboot worker-0** or **Delete cluster dev**, or press Ctrl+Enter (Cmd+Enter on macOS), to start the command. The button names what it changes. Enter in a field does not start it, unless the dialog has only that field, such as the name to type for a delete. The UI checks the fields against the cluster as it is now. It also builds the command again when you press the button. If that command differs from the one in the dialog, the UI refuses to start it.

When the command starts, the dialog closes and you stay on your page. A notice names the command and links to its run page, and the task dock shows its progress, so you can start the next action at once. **New cluster** opens the run page of its command instead, and so does **Delete** on the page of the cluster that it deletes, or on the page of one of its nodes.

Esc, **Cancel** or a click beside the dialog closes it, and the focus goes back to the button that opened it. When you have changed a field, the dialog asks first: **Keep editing** or **Discard**. When you leave the page while such a dialog is open, your browser asks first. **New cluster** keeps a draft instead.

You can copy the command and run it in a terminal. It makes the same change. This is useful to learn the CLI, to script the change, or to run it again later. In the New cluster dialog, **Copy script** gives a shell script that does all that Create does: it saves the config, creates the data directory if needed, and runs the command with `--realm`, as `SIND_REALM` in your shell would otherwise take the place of the config's realm.

{{< uishot "dialog-create-worker" "Each dialog shows the command that it runs, here `sind create worker dev`." >}}

## The actions

Each action runs one command:

| Action | Command | Undo |
|--------|---------|------|
| New cluster | `sind create cluster --config FILE` | Delete the cluster |
| Add workers | `sind create worker CLUSTER --count N` | Delete the workers |
| Delete cluster | `sind delete cluster NAME` | Cannot be undone |
| Delete workers | `sind delete worker NODES` | Cannot be undone |
| Power | `sind power VERB NODES` | Use the opposite verb |
| Drain or resume workers | `sind exec CLUSTER -- scontrol update NodeName=NODES State=DRAIN Reason=WHY` | Resume, or drain |
| Connect or disconnect a container | `sind connect container NAME` | Disconnect or connect it again |
| Delete realm | `sind --realm REALM delete realm` | Cannot be undone |

## New cluster

**New cluster** takes the name, the realm, the image, the nodes, the node defaults, the users, groups and Slurm accounts, the data storage, the wait for ready and whether to pull the image. Every cluster has one controller. A checkbox adds each of the other roles (backup controller, db, api, submitter), and a field with − and + sets the number of workers. **+ Add worker group** adds a group of workers with a size of its own; with more than one, the groups are `workers 1`, `workers 2` and so on, also in the messages of the plan. The node defaults are Small, Medium, Large or your own CPUs, memory and `/tmp` size; every node takes them. The size button of a node opens its own fields: image, CPUs, memory and `/tmp`, and under **More** unmanaged, capabilities, devices and security options. An empty field takes the node defaults, which it shows in grey. The name is a free one of the realm, such as `hpc` or `hpc-2`, selected when the dialog opens (on a touch screen, when you first tap it), so that typing replaces it, and the data directory follows the name until you change it. The realm is that of the page you open the dialog from, or `sind`; the field suggests the realms there are, and a new name makes a new realm. In Users and accounts, each user, group and Slurm account is a row with its name; an empty UID or GID takes the next free one from 1000. **More** on a user gives its primary group, groups, the accounts it coordinates and its Slurm admin level. An account has a parent and limits, each a pair of a sacctmgr option, such as `MaxJobs`, and its value. Slurm accounts need the db node. Identity picks where the nodes find the users: `local`, `nssSlurm` or `clientIds`, and with `clientIds` whether the controllers get the users too. Slurm config has a tab for each file, `slurm.conf`, `cgroup.conf`, `gres.conf`, `topology.conf`, `plugstack.conf` and `slurmdbd.conf`, with the lines that sind adds after its own; a dot marks a tab with lines. In Run options, the wait for ready is 2, 5 or 10 minutes, no limit, or a time you type, such as `15m`. **Pull the image** is on for an image of a registry whose tag can move to a newer image, such as `latest` or `26.05`, and off for a fixed tag, a digest or an image you built. It follows the image until you tick or clear it. The data storage, in the Data section, is a directory on the host or a Docker volume, mounted at `/data` or at the path you give. **CVMFS** mounts CVMFS read-only at `/cvmfs` on every node. sind ui writes it into the config, so the cluster never uses the directory you started the UI in by accident. Docker mounts only a directory that exists: sind ui creates a missing one first, unless you clear **Create the directory if it is missing**; the directory stays if the create fails. The UI saves the cluster config in `~/.local/state/sind/ui/configs/`, with mode `0600`, so you can run the same command again from a terminal.

The plan beside the form shows the nodes, the command and the config as you type. A wrong field gets its message below it and a line in the plan, which links to the field. The message says what to change, such as `use only lowercase letters, digits and -, no spaces, such as my-cluster`. The plan also lists every error that sind finds in the config, all at once, where sind itself stops at the first. An error that a row of Users and accounts causes, such as an account that a user names but no row declares, goes to the field of that row, in the words of the form: `add the account physics under Slurm accounts first, or remove it from mia`. The plan then takes the default for a wrong field, so it never goes blank. The status at the bottom shows the number of problems and the field of the first, such as `1 problem: Name`; press it to go to that field, also in a folded section. Create works only when the status says `Ready`; `Ready · 2 warnings` counts the warnings of the plan, which do not stop it. Below the command, **What sind runs** lists what sind does once the nodes are up: the `groupadd` and `useradd` commands on the nodes that get the users, and the `sacctmgr` commands of the Slurm accounts on the controller. **Preview slurm.conf**, below the config, shows the `slurm.conf` that sind writes, with your lines of Slurm config after its own, and the `sind-nodes.conf` that it includes. Both show once the config is valid. Beside the status, and in the plan, the UI adds up the containers, CPUs and memory of all nodes and shows the memory against that of the Docker host; the plan warns when the nodes may use more than 80% of it. More than all of it is a problem until you tick **Create anyway** below the meter: Docker caps the memory of each node but sets none aside, so idle nodes use less, but jobs that use it all make the host run out of memory.

The dialog keeps what you typed as a draft in your browser until you create the cluster or discard it. When you open **New cluster** again, it starts from your draft and says so; **Start over** discards the draft. A click beside the New cluster dialog does not close it.

The plan checks more than sind does before the create starts. An image reference must be one that Docker takes, such as `ghcr.io/gsi-hpc/sind-node:26.05`: lowercase, no spaces, a tag after `:` and a digest after `@` (`sha256:` and 64 hex digits). An account limit `QOS`, `DefaultQOS` or `QosLevel` may name only the QOS `normal`, as a new cluster has no other and sind creates none. sind takes your lines of Slurm config after its own, and Slurm takes the last value of a parameter, so yours wins. sind refuses only a few, such as `SlurmctldHost` with a backup controller and the database parameters of `slurmdbd.conf`; the plan warns when `slurm.conf` sets `ClusterName`, `SlurmctldHost`, `SlurmUser`, `StateSaveLocation`, `SlurmdSpoolDir` or `PlugStackConfig`, which the cluster needs as sind sets them, and of a line that is no `Key=Value` pair, outside `plugstack.conf`.

The plan lists the images of the nodes with their Slurm release, which sind ui reads from the label `sind.slurm.version` of a sind-node image. For an image without the label, or one that is not on the host, **Check image** starts it once with `slurmctld -V`, as sind does; for an image that is not on the host, it pulls the image first. sind ui keeps what a check found for that image. Monitoring needs Slurm 26.05 or later in the controller and worker images: with an older image, Create stays off, unless Create pulls a newer copy of the image first, which the plan then says. The plan also warns of an api node whose image is older than 26.05, as sind-node images have slurmrestd from that release, and of workers with a newer Slurm than the controller's.

**Edit YAML**, at the top of the plan, puts the whole config into an editor, also what the form has no field for, such as named Slurm config fragments or `defaults` capabilities. **Start from YAML…**, below the name, opens the editor empty: paste a config there, press **Open a file…**, or drop a file on it, such as a config that you keep in git (at most 64 KiB). The editor takes the place of the form, which stays locked until you choose **Discard YAML edits**; the run options still apply. Name and Realm stay beside the editor: they show the name and the realm of the YAML, and a change of either goes into it, as a new line if the YAML has none. **Discard YAML edits** puts back the name and the realm of the form. A YAML without a name makes the cluster `default`, as sind does. The plan follows the YAML, and sind ui saves it as you wrote it, with your comments. A missing host directory of the YAML is created first. **Copy YAML**, next to Edit YAML, copies the config, to keep it in a file of your own.

{{< uishot "dialog-create-cluster" "The New cluster dialog: the form on the left, the plan on the right." >}}

## Add workers

**Add workers** shows the size and the names of the new workers, such as `3 × 2 CPUs, 1 GiB: worker-6 … worker-8`. New workers copy the newest worker of the cluster that is managed like its controller, which sind ui reads from its container; the CPUs and memory fields name that worker in grey. A cluster without workers gets sind's defaults, and so does a limit that the newest worker has none of. The button names what it adds: **Add worker-6** or **Add 3 workers**.

While the dialog checks a change of its fields, its footer shows **Checking…**.

## Power

The power buttons of a page, such as **Reboot…**, open the Power dialog. It sorts the actions into three groups and selects the one of the button:

- **Slurm (scontrol)**: **Drain** and **Resume** change what Slurm does with workers and leave their containers running. Drain lets the running jobs end and starts no new ones on the nodes; it needs a reason, which `sinfo -R` shows. Resume lets the nodes take jobs again. Both run `scontrol update` in the cluster with `sind exec`, which writes no progress, so their task has no run page.
- **Containers**: power on, shut down, reboot, freeze and unfreeze change the containers, up to 8 at a time.
- **Can lose data**: power-cycle and cut power kill the containers at once. Run them only when you need to.

Before an action that stops nodes, the dialog warns when they include the controller in control, the standby controller of a pair, the db node or every running worker of a cluster, and says what that does to jobs, failover and accounting. The button names the action and the nodes, such as **Shut down 3 nodes**.

{{< uishot "dialog-power" "The Power dialog groups the actions, and shows the command of the one that you selected." >}}

## Delete

**Delete cluster**, **Delete workers** and **Delete realm** list what goes and what stays: the containers, the network, the volumes and the Slurm state of a cluster go, and the host directories it mounts stay. The button names what it deletes, such as **Delete worker-5** or **Delete cluster dev**.

To confirm, type the name of the worker when you delete one worker, else the name of the cluster or the realm. The name must match with its case; spaces around it do not count. Until it matches, a hint below the field says why the button stays off.

Delete realm deletes every cluster of the realm, its mesh and its monitoring stack, and detaches the containers that sind did not make. Its list of what stays names the host directories of its clusters and those containers. Open it from the realm's row on the Overview, or from Mesh and DNS. Its command always names the realm, so a copy of it deletes this realm in any shell.

{{< uishot "dialog-delete-cluster" "A delete lists what goes and what stays, and asks you to type the name of the cluster." >}}

## The realm lock

A dialog that changes a realm with a running command shows which command holds the realm lock. Your command waits until the other one ends. See [Troubleshooting]({{< relref "/web-ui/troubleshooting#a-command-waits-for-the-realm-lock" >}}).
