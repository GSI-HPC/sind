---
weight: 110
title: "Installation"
icon: "download"
description: "Prerequisites and installation instructions"
toc: true
---

## Prerequisites

- **Linux host** with cgroupv2 and the `nsdelegate` mount option:
  ```bash
  mount -o remount,nsdelegate /sys/fs/cgroup
  ```
- **Docker Engine 28.0+** (required for `--security-opt writable-cgroups=true`)
- **Go 1.21+** (for building from source)

## Install from source

```bash
go install github.com/GSI-HPC/sind/cmd/sind@latest
```

## Verify installation

```bash
sind --version
```

## Container image

sind requires a container image with systemd, munge, sshd, and Slurm installed. The default image (`ghcr.io/gsi-hpc/sind-node:latest`) is pulled automatically when creating your first cluster.

See [Container Images](../../container-images/building-images/) for details on building custom images.
