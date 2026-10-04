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

Releases include binaries for linux/amd64 and linux/arm64. The commands below install the amd64 one; on 64-bit ARM, use `sind-linux-arm64` instead.

Download the binary and check it before you install it:

```bash
curl -fLO https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64
gh attestation verify sind-linux-amd64 --repo GSI-HPC/sind \
  --signer-workflow GSI-HPC/sind/.github/workflows/release.yml
```

Each release publishes a build provenance attestation for its binaries, and the [GitHub CLI](https://cli.github.com/) checks with it that sind's release workflow built the download. `--signer-workflow` matters: with `--repo` alone, any workflow of the repository would pass. Without the GitHub CLI, compare the download with the release's `checksums.txt`. That only detects a corrupted download, as the checksums come from the same release:

```bash
curl -fLO https://github.com/GSI-HPC/sind/releases/latest/download/checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

System-wide installation into `/usr/local/bin`:

```bash
# install sets mode 755 and copies to the target directory
sudo install ./sind-linux-amd64 /usr/local/bin/sind
```

Per-user installation into `~/.local/bin`:

```bash
install -D ./sind-linux-amd64 ~/.local/bin/sind
```

> Most distributions include `~/.local/bin` in `$PATH` by default, but only
> if the directory exists at login time. You may need to log out and back in
> after creating it.

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

sind requires a container image with systemd, munge, sshd, and Slurm installed. The default image is the official image published with your sind release, for sind v0.11.0 for example:

```
ghcr.io/gsi-hpc/sind-node:v0.11.0
```

It carries the newest Slurm release line that release supports and is published for linux/amd64 and linux/arm64. sind built from source (`go install`, `make build`) defaults to `ghcr.io/gsi-hpc/sind-node:latest` instead, the newest supported release line. To stay on a specific release line, set `defaults.image` in the cluster configuration to its tag, e.g. `ghcr.io/gsi-hpc/sind-node:25.11`. See [Official images]({{< relref "/container-images/building-images#official-images" >}}) for the available tags.

Docker pulls the image automatically when creating your first cluster, and after a sind upgrade, whose default image has a new tag. Subsequent creates reuse the cached image — use `--pull` to force a fresh pull:

```bash
sind create cluster --pull
```

Besides the node image, each realm's mesh runs `coredns/coredns:1.14.7` (DNS) from Docker Hub, and its SSH relay runs sind's default node image whatever `defaults.image` says. Hosts that cannot reach Docker Hub or ghcr.io need these images preloaded.

See [Container Images](../../container-images/building-images/) for details on building custom images.
