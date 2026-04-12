---
weight: 100
title: "Container exits with code 255"
---

## Symptom

Cluster creation fails with containers exiting immediately:

```
waiting for worker-5: container sind-dev-worker-5 is exited (exit code 255)
```

This typically happens when creating clusters with many nodes (8+). Some containers start successfully while others crash within the first second.

## Cause

Exit code 255 means systemd (PID 1 inside the container) failed during early initialization. The most common cause is the Linux `inotify` instance limit being too low.

Each Docker container consumes several `inotify` file descriptors through containerd's cgroup event monitoring. systemd inside the container also requires inotify for service management. When the per-user limit is exhausted, new containers cannot initialize systemd and exit immediately with code 255.

The default on many distributions (Fedora, Ubuntu) is 128 instances, which supports roughly 10-13 concurrent systemd containers depending on the workload.

## Diagnosis

Check the current limit and whether containerd is hitting it:

```bash
# Current limit
cat /proc/sys/fs/inotify/max_user_instances
# 128

# Check containerd logs for inotify errors
journalctl -u docker --since "5 minutes ago" | grep inotify
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
