---
title: "sind"
geekdocNav: false
geekdocAlign: center
geekdocAnchor: false
---

<img src="images/sind-icon-tagline.svg" alt="sind — Slurm in Docker" width="200" />

**Create and manage containerized [Slurm](https://slurm.schedmd.com/) clusters for development, testing, and CI/CD workflows.**

Inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker), sind offers a familiar CLI experience for quickly spinning up and tearing down Slurm clusters.

{{< button size="large" relref="getting-started/" >}}🚀 Getting Started{{< /button >}}
{{< button size="large" relref="getting-started/" >}}📖 Documentation{{< /button >}}

---

{{< columns >}}

### Multi-node clusters

Controller, submitter, and worker nodes as individual containers with systemd init.

<--->

### Realistic environment

Munge authentication, SSH access, DNS resolution — everything you need for real Slurm testing.

<--->

### Cross-cluster networking

Shared mesh network with DNS for multi-cluster setups and inter-cluster communication.

{{< /columns >}}

{{< columns >}}

### Worker lifecycle

Dynamically add and remove worker nodes from running clusters.

<--->

### Power simulation

Shutdown, reboot, freeze, and cycle nodes to test Slurm failure handling.

<--->

### Minimal dependencies

Just Docker and a sind container image. Usable as both a CLI tool and a Go library.

{{< /columns >}}
