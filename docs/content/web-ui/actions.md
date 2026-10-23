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

Press the button of the dialog, such as **Create**, **Add**, **Run** or **Delete**, or press Ctrl+Enter (Cmd+Enter on macOS), to start the command. Enter in a field does not start it, unless the dialog has only that field, such as the name to type for a delete. The UI checks the fields against the cluster as it is now. It also builds the command again when you press the button. If that command differs from the one in the dialog, the UI refuses to start it.

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
| Connect or disconnect a container | `sind connect container NAME` | Disconnect or connect it again |
| Delete realm | `sind delete realm` | Cannot be undone |

## New cluster

**New cluster** takes the name, the realm, the image, the nodes, the node defaults, the users, groups and Slurm accounts, the data storage, the wait for ready and whether to pull the image. Every cluster has one controller. A checkbox adds each of the other roles (backup controller, db, api, submitter), and a field with − and + sets the number of workers. **+ Add worker group** adds a group of workers with a size of its own. The node defaults are Small, Medium, Large or your own CPUs, memory and `/tmp` size; every node takes them. The size button of a node opens its own fields: image, CPUs, memory and `/tmp`, and under **More** unmanaged, capabilities, devices and security options. An empty field takes the node defaults, which it shows in grey. The name is a free one of the realm, such as `hpc` or `hpc-2`, and the data directory follows the name until you change it. The realm is that of the page you open the dialog from, or `sind`; the field suggests the realms there are, and a new name makes a new realm. In Users and accounts, each user, group and Slurm account is a row with its name; an empty UID or GID takes the next free one from 1000. **More** on a user gives its primary group, groups, the accounts it coordinates and its Slurm admin level. An account has a parent and limits, each a pair of a sacctmgr option, such as `MaxJobs`, and its value. Slurm accounts need the db node. Identity picks where the nodes find the users: `local`, `nssSlurm` or `clientIds`, and with `clientIds` whether the controllers get the users too. Slurm config has a tab for each file, `slurm.conf`, `cgroup.conf`, `gres.conf`, `topology.conf`, `plugstack.conf` and `slurmdbd.conf`, with the lines that sind adds after its own; a dot marks a tab with lines. In Run options, the wait for ready is 2, 5 or 10 minutes, no limit, or a time you type, such as `15m`. **Pull the image** is on for an image of a registry whose tag can move to a newer image, such as `latest` or `26.05`, and off for a fixed tag, a digest or an image you built. It follows the image until you tick or clear it. The data storage, in the Data section, is a directory on the host or a Docker volume, mounted at `/data` or at the path you give. **CVMFS** mounts CVMFS read-only at `/cvmfs` on every node. sind ui writes it into the config, so the cluster never uses the directory you started the UI in by accident. Docker mounts only a directory that exists: sind ui creates a missing one first, unless you clear **Create the directory if it is missing**; the directory stays if the create fails. The UI saves the cluster config in `~/.local/state/sind/ui/configs/`, with mode `0600`, so you can run the same command again from a terminal.

The plan beside the form shows the nodes, the command and the config as you type. A wrong field gets its message below it and a line in the plan, which links to the field. The message says what to change, such as `use only lowercase letters, digits and -, no spaces, such as my-cluster`. The plan also lists every error that sind finds in the config, all at once, where sind itself stops at the first. An error that a row of Users and accounts causes, such as an account that a user names but no row declares, goes to the field of that row, in the words of the form: `add the account physics under Slurm accounts first, or remove it from mia`. The plan then takes the default for a wrong field, so it never goes blank. The status at the bottom shows the number of problems and the field of the first, such as `1 problem: Name`; press it to go to that field, also in a folded section. Create works only when the status says `Ready`. Beside the status, and in the plan, the UI adds up the containers, CPUs and memory of all nodes and shows the memory against that of the Docker host; the plan warns when the nodes may use more than 80% of it.

The dialog keeps what you typed as a draft in your browser until you create the cluster or discard it. When you open **New cluster** again, it starts from your draft and says so; **Start over** discards the draft. A click beside the New cluster dialog does not close it.

The plan lists the images of the nodes with their Slurm release, which sind ui reads from the label `sind.slurm.version` of a sind-node image. For an image without the label, or one that is not on the host, **Check image** starts it once with `slurmctld -V`, as sind does; for an image that is not on the host, it pulls the image first. sind ui keeps what a check found for that image. Monitoring needs Slurm 26.05 or later in the controller and worker images: with an older image, Create stays off, unless Create pulls a newer copy of the image first, which the plan then says. The plan also warns of an api node whose image is older than 26.05, as sind-node images have slurmrestd from that release, and of workers with a newer Slurm than the controller's.

**Edit YAML**, beside the config in the plan, puts the whole config into an editor, also what the form has no field for, such as Slurm config fragments or `defaults` capabilities. The editor takes the place of the form, which stays locked until you choose **Discard YAML edits**; the run options still apply. The plan follows the YAML, and sind ui saves it as you wrote it, with your comments. A missing host directory of the YAML is created first.

{{< uishot "dialog-create-cluster" "The New cluster dialog: the form on the left, the plan on the right." >}}

## Power

The power buttons of a page, such as **Reboot…**, open the Power dialog. It lists every verb and selects the one of the button. Power-cycle and cut power kill the containers at once. Run them only when you need to.

{{< uishot "dialog-power" "The Power dialog lists every verb, and the command of the one that you selected." >}}

## Delete

**Delete cluster**, **Delete workers** and **Delete realm** ask you to type the name of the cluster or realm to confirm. Delete realm deletes every cluster of the realm, its mesh and its monitoring stack.

{{< uishot "dialog-delete-cluster" "A delete asks you to type the name of the cluster." >}}

## The realm lock

A dialog that changes a realm with a running command shows which command holds the realm lock. Your command waits until the other one ends. See [Troubleshooting]({{< relref "/web-ui/troubleshooting#a-command-waits-for-the-realm-lock" >}}).
