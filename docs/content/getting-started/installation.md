---
weight: 110
title: "Installation"
icon: "download"
description: "Prerequisites and installation instructions"
toc: true
---

## Install

{{< tabs "install" >}}
{{< tab "Pre-built binary (linux/amd64)" >}}
```bash
curl -Lo ./sind https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64
chmod +x ./sind
sudo mv ./sind /usr/local/bin/sind
```
{{< /tab >}}
{{< tab "From source" >}}
Build and install with Go:

```bash
go install github.com/GSI-HPC/sind/cmd/sind@latest
```
{{< /tab >}}
{{< /tabs >}}

## Verify

Check that sind is installed and your system meets all prerequisites:

```bash
sind doctor
```

This verifies Docker Engine version and cgroup configuration, and prints fix instructions if anything is missing.

## Container image

sind requires a container image with systemd, munge, sshd, and Slurm installed. The default image (`ghcr.io/gsi-hpc/sind-node:latest`) is pulled automatically when creating your first cluster.

See [Container Images](../../container-images/building-images/) for details on building custom images.
