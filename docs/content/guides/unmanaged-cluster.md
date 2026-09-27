---
weight: 355
title: "Unmanaged Cluster"
icon: "construction"
description: "Leave Slurm to your own tooling: test Chef or Ansible provisioning, or run slurmctld by hand"
toc: true
---

An unmanaged cluster gives you sind's nodes (systemd containers with networking, DNS, SSH access and a shared munge key) and leaves Slurm to you. Use it to test Chef or Ansible code that provisions Slurm end to end, or to debug a configuration by running `slurmctld -Dvvvvvv` by hand.

## Create an unmanaged cluster

Set `managed: false` on the controller:

```yaml
kind: Cluster
name: dev
nodes:
  - role: controller
    managed: false
  - role: submitter
  - role: worker
    count: 2
```

```bash
sind create cluster --config unmanaged.yaml
```

The flag applies to the whole cluster:

- Every node is unmanaged. A worker with `managed: true` is rejected.
- `slurm` sections (`main`, `cgroup`, `gres`, `topology`, `plugstack`) are rejected, because sind writes no Slurm configuration.
- `backupController: true` still adds `controller-backup` and the shared state volume (see [Controller pair](#controller-pair)).

## What sind sets up

|  | Managed cluster | Unmanaged cluster |
|--|-----------------|-------------------|
| Containers, networks, DNS, SSH access | ✓ | ✓ |
| munge key, munge running | ✓ | ✓ |
| `/etc/slurm` | generated configuration | empty |
| slurmctld and slurmd | running | not started |
| Slurm version (`sind.slurm.version` label) | discovered from the image | empty |

`/etc/slurm` is one volume shared by all nodes, like an NFS share: writable on the controllers, read-only on the submitter and the workers. `/etc/munge` holds sind's munge key and is read-only on every node. `sind create cluster` returns once each node runs systemd, sshd and munge.

The stock `sind-node` image has Slurm installed but not enabled. sind neither runs nor queries Slurm on an unmanaged cluster, so a custom image may also leave Slurm out for your provisioning to install (see [Building Images]({{< relref "/container-images/building-images" >}})).

## Provision Slurm

Write the configuration to `/etc/slurm` on a controller, then start the daemons. By hand, a minimal setup for the cluster above looks like this:

```bash
sind ssh controller.dev -- 'cat > /etc/slurm/slurm.conf' <<'EOF'
ClusterName=dev
SlurmctldHost=controller
SlurmUser=slurm
StateSaveLocation=/var/spool/slurmctld
SlurmdSpoolDir=/var/spool/slurmd
ProctrackType=proctrack/cgroup
TaskPlugin=task/cgroup,task/affinity
ReturnToService=2
NodeName=worker-[0-1] CPUs=1 State=UNKNOWN
PartitionName=all Nodes=ALL Default=YES MaxTime=INFINITE State=UP
EOF
sind ssh controller.dev -- 'echo CgroupPlugin=autodetect > /etc/slurm/cgroup.conf'
sind ssh controller.dev -- systemctl start slurmctld
sind ssh worker-0.dev -- systemctl start slurmd
sind ssh worker-1.dev -- systemctl start slurmd
sind exec dev -- srun -N2 hostname
```

Chef, Ansible and similar tools reach the nodes over SSH as root. Include sind's exported SSH configuration (see [User SSH client integration]({{< relref "/usage/node-access#user-ssh-client-integration" >}})) and use the node names as hosts, for example in an Ansible inventory:

```ini
[slurm_controller]
controller.dev.sind.sind

[slurm_submitter]
submitter.dev.sind.sind

[slurm_workers]
worker-0.dev.sind.sind
worker-1.dev.sind.sind
```

Keep in mind:

- Write Slurm files on a controller. On the submitter and the workers `/etc/slurm` is read-only, and every node reads the same files.
- munge is sind's. `/etc/munge` is read-only, so recipes that manage the munge key must leave it as it is. `sind get munge-key dev` prints the key base64-encoded if your tooling needs it.
- `sind get cluster dev` reports munge and sshd only. Check Slurm itself with `sind exec dev -- sinfo` and `sind logs controller.dev slurmctld`.

## Run slurmctld by hand

To debug a configuration, run `slurmctld` in the foreground with full verbosity instead of starting the service:

```bash
sind ssh -t controller.dev -- slurmctld -Dvvvvvv
```

Stop it with Ctrl-C, edit the files under `/etc/slurm` on a controller, and run it again. `slurmd -Dvvvvvv` works the same way on a worker.

## Controller pair

With `backupController: true`, sind creates `controller-backup` next to `controller` and mounts the shared state volume `<realm>-<cluster>-state` at `/var/spool/slurmctld` on both. Use it as `StateSaveLocation`, and list the controllers in this order so that your pair matches sind's names:

```
SlurmctldHost=controller
SlurmctldHost=controller-backup
StateSaveLocation=/var/spool/slurmctld
```

sind shows no `HA` column for an unmanaged pair, because it cannot tell which controller is in control. Ask Slurm instead with `sind exec dev -- scontrol ping`. Without a submitter, `sind enter` and `sind exec` target `controller` if it runs, otherwise `controller-backup`. [Controller Failover]({{< relref "/guides/controller-failover" >}}) shows how to trigger failovers; the power commands work the same way on an unmanaged pair.

## Workers

`sind create worker dev` adds unmanaged workers, with or without `--unmanaged`: sind does not register them with Slurm or start slurmd. Add them to your configuration and start slurmd yourself. `sind delete worker` removes the containers and never edits `/etc/slurm`.
