---
weight: 79
title: "09 · Bring your own provisioning"
description: "Unmanaged clusters for Ansible or Chef, custom node images"
---

{{< video "course-09-own-provisioning" >}}

Your site's Slurm comes from Ansible roles or Chef cookbooks, and you want to test them end to end on fresh nodes, perhaps on your site's own node image. This episode creates an unmanaged cluster, where sind brings the nodes and leaves Slurm to you, provisions Slurm on it by hand and over SSH, and lists what a custom node image needs.

## In this episode

- [Unmanaged Cluster]({{< relref "/guides/unmanaged-cluster" >}}): `managed: false`, and what sind still sets up without Slurm
- [Provision Slurm]({{< relref "/guides/unmanaged-cluster#provision-slurm" >}}): a minimal Slurm configuration by hand, and the nodes as hosts in an Ansible inventory
- [Custom image requirements]({{< relref "/container-images/building-images#custom-image-requirements" >}}): systemd, sshd, munge and the stop signal in your own node image
- [System containers]({{< relref "/introduction#system-containers" >}}): why nodes with systemd as PID 1 take the same configuration management as bare metal

## Commands

Every command, config and file shown in the video, in order, as on the reference pages.

An unmanaged cluster: `managed: false` on the controller ([Unmanaged Cluster]({{< relref "/guides/unmanaged-cluster#create-an-unmanaged-cluster" >}})):

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

Provision Slurm by hand: write the configuration on the controller, then start the daemons ([Provision Slurm]({{< relref "/guides/unmanaged-cluster#provision-slurm" >}})):

```bash
sind ssh controller.dev -- 'cat > /etc/slurm/slurm.conf' <<'EOF'
ClusterName=dev
SlurmctldHost=controller
SlurmUser=slurm
StateSaveLocation=/var/spool/slurmctld
SlurmdSpoolDir=/var/spool/slurmd
ProctrackType=proctrack/cgroup
TaskPlugin=task/cgroup
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

The same nodes as hosts in an Ansible inventory, with the SSH configuration that sind exports included ([Provision Slurm]({{< relref "/guides/unmanaged-cluster#provision-slurm" >}})):

```ini
[slurm_controller]
controller.dev.sind.sind

[slurm_submitter]
submitter.dev.sind.sind

[slurm_workers]
worker-0.dev.sind.sind
worker-1.dev.sind.sind
```

The munge key for tooling that needs it, and Slurm's own view of the cluster:

```bash
sind get auth-key dev
sind exec dev -- sinfo
sind logs controller.dev slurmctld
```

The container settings of your own node image ([Container settings]({{< relref "/container-images/building-images#container-settings" >}})):

```dockerfile
VOLUME ["/etc/slurm", "/etc/munge", "/data"]
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
```

## Refresher: systemd as PID 1

An application container runs one program as its first process, while a sind node runs systemd there, which starts, supervises and cleanly stops its services as on a real machine: [the full refresher]({{< relref "refreshers#systemd-as-pid-1" >}}).

## Go deeper

- [Run slurmctld by hand]({{< relref "/guides/unmanaged-cluster#run-slurmctld-by-hand" >}}): debug a configuration with `slurmctld -Dvvvvvv` in the foreground
- [Controller pair]({{< relref "/guides/unmanaged-cluster#controller-pair" >}}) and [Workers]({{< relref "/guides/unmanaged-cluster#workers" >}}): a backup controller and added workers in an unmanaged cluster
- [User SSH client integration]({{< relref "/usage/node-access#user-ssh-client-integration" >}}): the SSH configuration to include for Ansible or Chef
- [Building Images]({{< relref "/container-images/building-images" >}}): official images and every custom image requirement, with its guide clip
- [Design Overview]({{< relref "/architecture/overview#container-per-node" >}}): systemd as PID 1 in each node, and the container options it needs
- Guide clip: [Unmanaged Cluster]({{< relref "/guides/unmanaged-cluster" >}})
