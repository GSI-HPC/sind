---
weight: 100
title: "Container exits with code 255"
webui: none
webuiNote: "The [Doctor page](../../web-ui/pages/#doctor) of the web UI shows the inotify limit and the commands that raise it. The steps on this page run in a shell on the Docker host."
---

## Symptom

Cluster creation fails with containers exiting immediately. Depending on whether the container had already exited when sind first checked it, or died while sind was waiting for it, the error reads:

```
node worker-5: probe container: container sind-dev-worker-5 is exited (exit code 255)
```

or:

```
node worker-5: container sind-dev-worker-5 died: exitCode=255
```

This typically happens when the host runs 10 or more nodes, in one cluster or in several. Some containers start successfully while others crash within the first second.

## Cause

Exit code 255 means systemd (PID 1 inside the container) failed during early initialization. The most common cause is the Linux `inotify` instance limit being too low.

containerd takes one inotify instance per container to watch its memory events, and systemd and journald in each node take more. All of them count against the host's root user: containerd runs as root, and the nodes run without a user namespace. When the per-user limit is exhausted, new containers cannot initialize systemd and exit immediately with code 255.

The default on many distributions (Fedora, Ubuntu) is 128 instances, which supports roughly 10-13 concurrent systemd containers depending on the workload.

## Diagnosis

`sind doctor` warns when the limit is below 1024 and prints the commands that raise it (see [Doctor]({{< relref "/usage/diagnostics#doctor" >}})). By hand, check the current limit and whether containerd is hitting it:

```bash
# Current limit
cat /proc/sys/fs/inotify/max_user_instances
# 128

# Check containerd logs for inotify errors
journalctl -u containerd -u docker --since "5 minutes ago" | grep inotify
# error from *cgroupsv2.Manager.EventChan: failed to create inotify fd: too many open files
```

## Fix

Increase the inotify instance limit. A value of 1024 supports approximately 100 concurrent containers:

```bash
# Temporary (resets on reboot)
sudo sysctl fs.inotify.max_user_instances=1024

# Persistent
echo "fs.inotify.max_user_instances = 1024" | sudo tee /etc/sysctl.d/99-sind.conf
sudo sysctl --system
```

No restart of Docker or running containers is required. The new limit takes effect immediately for new containers.

## GitHub Actions

GitHub's Ubuntu 26.04 runner images, which `ubuntu-latest` and `ubuntu-26.04-arm` run, keep the kernel's default of 128 instances, while its Ubuntu 24.04 image allows 1280. [sind-action](https://github.com/GSI-HPC/sind-action) v2.3.0 and later raise the limit to 8192 before `sind doctor` when `sudo` needs no password, as on GitHub's runners, and only warn otherwise. Without the action, or with an older release of it, run `sudo sysctl -w fs.inotify.max_user_instances=8192` before you create clusters. See [CI/CD]({{< relref "/guides/ci-cd" >}}) for other CI systems.
