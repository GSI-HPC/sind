---
weight: 380
title: "CI/CD"
icon: "deployed_code"
description: "Using sind in GitHub Actions and other CI systems"
toc: true
---

## GitHub Action

The [sind-action](https://github.com/GSI-HPC/sind-action) GitHub Action installs sind and creates clusters in your workflow. sind runs rootless on standard `ubuntu-latest` runners — no privileged containers or custom runner images required.

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

sind requires Docker Engine 28.0+ and a Linux host with cgroupv2 and `nsdelegate`. Most modern CI runners meet these requirements out of the box.
