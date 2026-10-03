---
weight: 353
title: "CI/CD"
icon: "deployed_code"
description: "Using sind in GitHub Actions and other CI systems"
toc: true
---

## GitHub Action

The [sind-action](https://github.com/GSI-HPC/sind-action) GitHub Action installs sind and creates clusters in your workflow. sind runs on standard GitHub-hosted Ubuntu runners, x64 (`ubuntu-latest`) and ARM64 (`ubuntu-24.04-arm`), with the runner's Docker daemon — no sudo, privileged containers or custom runner images required.

See the [sind-action documentation](https://github.com/GSI-HPC/sind-action#readme) for inputs, outputs, cluster definitions, and examples including parallel job isolation via realms.

## Other CI systems

sind works in any CI environment that provides a rootful Docker daemon. Install the binary from a [GitHub release](https://github.com/GSI-HPC/sind/releases), check its build provenance before running it, and run `sind doctor` to verify prerequisites:

```bash
curl -fsSL -o sind-linux-amd64 \
  "https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64"
gh attestation verify sind-linux-amd64 --repo GSI-HPC/sind \
  --signer-workflow GSI-HPC/sind/.github/workflows/release.yml
install -m 755 sind-linux-amd64 ./sind
./sind doctor
./sind create cluster --config cluster.yml
```

On 64-bit ARM, download `sind-linux-arm64` instead. `gh attestation verify` needs the [GitHub CLI](https://cli.github.com/) and a token in `GH_TOKEN`; see [Installation]({{< relref "/getting-started/installation" >}}) for the checksum alternative.

sind requires Docker Engine 28.0+, running rootful and without `userns-remap`, and a Linux host with cgroupv2 and `nsdelegate`. Most modern CI runners meet these requirements out of the box. Rootless Docker and `userns-remap` do not work: Docker refuses the writable cgroups of sind's nodes there, which `sind doctor` and `sind create cluster` report before anything is pulled.

Jobs that share a Docker daemon, such as jobs on one self-hosted runner or jobs that mount the host's Docker socket, must each use their own realm (`SIND_REALM`): sind's realm lock does not reach across them. Each realm's mesh and each cluster take one network from the daemon's default address pools, which a stock daemon fills at about 30 networks; a runner that hosts many such jobs at once needs smaller pools (see [Limits]({{< relref "/architecture/networking#limits" >}})).

## Data directory

By default every node mounts the job's working directory, usually the checked-out workspace, read-write at `/data` (see [Data mount]({{< relref "/usage/node-access#data-mount" >}})). Files that root writes there from a node belong to root on the runner, so a later step that cleans the workspace may need `sudo`. `--data volume`, or `storage.dataStorage.type: volume` in the config, keeps the workspace out of the cluster.

The directory is resolved on the Docker host. When the CI job runs in a container that shares the host's Docker socket, the job's paths do not exist there and `sind create cluster` fails; use `--data volume` in that case.

## Untrusted changes

A cluster config is as trusted as a script with the runner's Docker access (see [Trust]({{< relref "/configuration/cluster-config#trust" >}})): do not create clusters from configs that untrusted pull requests can change in a `pull_request_target` or other privileged workflow.

## Node failure tests

Tests that take a worker away with `sind power freeze`, `cut` or `shutdown` wait for slurmctld to mark it `DOWN`, which takes about five minutes with Slurm's default `SlurmdTimeout=300`. Lower it in the cluster config to keep such a job short (see [Node failure detection]({{< relref "/usage/power-control#node-failure-detection" >}})):

```yaml
slurm:
  main: |
    SlurmdTimeout=30
```
