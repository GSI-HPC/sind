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

System-wide installation into `/usr/local/bin`:

```bash
curl -Lo ./sind https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64
# install sets mode 755 and copies to the target directory
sudo install ./sind /usr/local/bin/sind
```

Per-user installation into `~/.local/bin`:

```bash
curl -Lo ./sind https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64
install -D ./sind ~/.local/bin/sind
```

> Most distributions include `~/.local/bin` in `$PATH` by default, but only
> if the directory exists at login time. You may need to log out and back in
> after creating it.

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

sind requires a container image with systemd, munge, sshd, and Slurm installed. The default image is:

```
ghcr.io/gsi-hpc/sind-node:latest
```

Docker pulls the image automatically when creating your first cluster. Subsequent creates reuse the cached image — use `--pull` to force a fresh pull:

```bash
sind create cluster --pull
```

See [Container Images](../../container-images/building-images/) for details on building custom images.
