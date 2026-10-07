---
title: "sind"
geekdocNav: false
geekdocAlign: center
geekdocAnchor: false
---

<img src="images/sind-icon-tagline.svg" alt="sind — Slurm in Docker" width="200" />

**Create and manage containerized [Slurm](https://slurm.schedmd.com/) clusters for development, testing, and CI/CD workflows.**

Inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker), sind offers a familiar CLI experience for quickly spinning up and tearing down Slurm clusters.

{{< button size="large" relref="getting-started/installation" >}}🚀 Getting Started{{< /button >}}
{{< button size="large" relref="introduction/" >}}📖 Documentation{{< /button >}}
{{< button size="large" href="https://github.com/GSI-HPC/sind" style="white-space: nowrap">}}<i class="gdoc-icon" style="width: 1em; height: 1em; vertical-align: -0.15em">gdoc_github</i> Source Code{{< /button >}}

---

{{< columns >}}

## Multi-node, multi-cluster & multi-realm

Run controller, database, REST API, submitter, and worker nodes side by side — or spin up multiple clusters across isolated realms with shared networking.

<--->

## System containers

Full systemd-based nodes that emulate bare metal — use the same config management tools you already have.

<--->

## Designed for CI/CD

Runs on standard GitHub Actions runners with the runner's Docker daemon, no sudo or privileged containers. [sind-action](https://github.com/GSI-HPC/sind-action) sets up clusters in a single step.

{{< /columns >}}

{{< columns >}}

## Worker lifecycle

Dynamically add and remove worker nodes from running clusters.

<--->

## Power cycle simulation

Shutdown, reboot, freeze, and power-cycle nodes to simulate real-world failure scenarios.

<--->

## Minimal dependencies

Just Docker and a sind container image. Usable as both a CLI tool and a Go library.

{{< /columns >}}

{{< columns >}}

## AI-ready via MCP

Built-in [MCP](https://modelcontextprotocol.io/) server lets AI assistants manage your Slurm clusters — just `sind mcp start`.

<--->

## Multiple Slurm versions

Official node images for Slurm 26.05 and 25.11 on linux/amd64 and linux/arm64 — or bring your own.

<--->

## Job accounting

A [db node]({{< relref "configuration/node-definitions#database-node" >}}) runs MariaDB and slurmdbd, so `sacct` and `sacctmgr` work as on a production cluster.

{{< /columns >}}

{{< columns >}}

## Users and identity

[Linux users, groups and Slurm accounts]({{< relref "guides/users" >}}) with limits, and identity modes from local accounts to nss_slurm and auth/slurm.

<--->

## REST API

slurmrestd on an [api node]({{< relref "guides/rest-api" >}}) with JWT authentication, for portals, workflow engines and tests (Slurm 26.05).

<--->

## CVMFS

[Mount `/cvmfs`]({{< relref "guides/cvmfs" >}}) read-only on every node, from the Docker host or a Docker volume plugin.

{{< /columns >}}
