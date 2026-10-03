---
weight: 353
title: "CI/CD"
icon: "deployed_code"
description: "Using sind in GitHub Actions and other CI systems"
toc: true
---

## GitHub Action

The [sind-action](https://github.com/GSI-HPC/sind-action) GitHub Action installs sind and creates clusters in your workflow. sind runs rootless on standard GitHub-hosted Ubuntu runners, x64 (`ubuntu-latest`) and ARM64 (`ubuntu-24.04-arm`) — no privileged containers or custom runner images required.

See the [sind-action documentation](https://github.com/GSI-HPC/sind-action#readme) for inputs, outputs, cluster definitions, and examples including parallel job isolation via realms.

## Other CI systems

sind works in any CI environment that provides Docker. Install the binary from a [GitHub release](https://github.com/GSI-HPC/sind/releases) and run `sind doctor` to verify prerequisites:

```bash
curl -fsSL -o sind \
  "https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64"
chmod +x sind
./sind doctor
./sind create cluster --config cluster.yml
```

On 64-bit ARM, download `sind-linux-arm64` instead.

sind requires Docker Engine 28.0+ and a Linux host with cgroupv2 and `nsdelegate`. Most modern CI runners meet these requirements out of the box.

## Data directory

By default every node mounts the job's working directory, usually the checked-out workspace, read-write at `/data` (see [Data mount]({{< relref "/usage/node-access#data-mount" >}})). Files that root writes there from a node belong to root on the runner, so a later step that cleans the workspace may need `sudo`. `--data volume`, or `storage.dataStorage.type: volume` in the config, keeps the workspace out of the cluster.

The directory is resolved on the Docker host. When the CI job runs in a container that shares the host's Docker socket, the job's paths do not exist there and `sind create cluster` fails; use `--data volume` in that case.
