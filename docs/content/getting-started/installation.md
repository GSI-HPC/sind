---
weight: 110
title: "Installation"
icon: "download"
description: "Prerequisites and installation instructions"
toc: true
---

## Install

{{< tabs "install" >}}
{{< tab "Pre-built binary (Linux)" >}}

Releases include binaries for linux/amd64 and linux/arm64. The commands below install the amd64 one; on 64-bit ARM, download `sind-linux-arm64` instead.

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

Each release publishes a `checksums.txt` and a build provenance attestation for its binaries. Check where a download came from with the [GitHub CLI](https://cli.github.com/):

```bash
gh attestation verify ./sind --repo GSI-HPC/sind
```

{{< /tab >}}
{{< tab "mise" >}}

Install the release binary for your platform (linux/amd64 or linux/arm64) with [mise](https://mise.jdx.dev/):

```bash
mise use -g github:GSI-HPC/sind
```

Drop `-g` to pin sind in the current project's `mise.toml` instead. mise checks the download against its published digest and verifies the release's build provenance attestation.

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

This verifies Docker Engine version, Docker daemon mode and cgroup configuration, and prints fix instructions if anything is missing.

sind needs a rootful Docker daemon. Rootless Docker and a daemon with `userns-remap` are not supported: Docker refuses the `--security-opt writable-cgroups=true` that every sind node runs with. `sind doctor` reports both, and `sind create cluster` refuses them before it pulls an image. "No sudo" means that sind itself needs no root privileges and starts no privileged containers; it uses the Docker daemon your user can reach.

## Container image

sind requires a container image with systemd, munge, sshd, and Slurm installed. The default image is:

```
ghcr.io/gsi-hpc/sind-node:latest
```

It is published for linux/amd64 and linux/arm64. `latest` carries the newest supported Slurm release line. To stay on a specific release line, set `defaults.image` in the cluster configuration to its tag, e.g. `ghcr.io/gsi-hpc/sind-node:25.11`. See [Official images](../../container-images/building-images/#official-images) for the available tags.

Docker pulls the image automatically when creating your first cluster. Subsequent creates reuse the cached image — use `--pull` to force a fresh pull:

```bash
sind create cluster --pull
```

Besides the node image, each realm's mesh runs `coredns/coredns:latest` (DNS) from Docker Hub, and its SSH relay runs `ghcr.io/gsi-hpc/sind-node:latest` whatever `defaults.image` says. Hosts that cannot reach Docker Hub or ghcr.io need these images preloaded.

See [Container Images](../../container-images/building-images/) for details on building custom images.
