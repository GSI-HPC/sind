---
weight: 50
title: "Introduction"
description: "What is sind and why use it?"
---

# What is sind?

**sind** (Slurm-IN-Docker) creates and manages containerized [Slurm](https://slurm.schedmd.com/) clusters for development, testing, and CI/CD workflows. Each node runs as a separate Docker container with systemd as init, providing a realistic multi-node Slurm environment without requiring bare-metal infrastructure.

Inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker), sind offers a familiar CLI experience for quickly spinning up and tearing down complete Slurm clusters in seconds.

## Why sind?

Setting up a multi-node Slurm cluster for development or testing traditionally requires dedicated machines, VMs, or complex provisioning. Tools like Vagrant and Docker Compose can get you there, but they demand significant configuration effort and maintenance. sind complements these approaches by trading some flexibility for ease of use and speed — a single command creates a fully working cluster.

- **Instant clusters** — go from zero to a working Slurm cluster in seconds, not hours
- **Reproducible environments** — every team member gets the same setup, every CI run starts clean
- **No infrastructure needed** — just Docker on your laptop or CI runner

## Features

### Multi-node, multi-cluster & multi-realm

Each cluster consists of individual containers for controller, submitter, and worker nodes. Run multiple clusters simultaneously with shared networking, and organize them into isolated realms for federation and multi-tenant testing scenarios.

### System containers

Unlike typical Docker containers that run a single application process, sind nodes are full system containers running systemd as init — closely emulating bare-metal machines. Services like munge, sshd, and slurmctld start and interact exactly as they would on real nodes, with proper service dependencies, process supervision, and signal handling. This means you can apply the same configuration management tools you use on bare metal — Ansible, Chef, Puppet, Salt — directly to sind nodes.

### Cross-cluster networking

Run multiple clusters simultaneously with a shared mesh network and automatic DNS. Test multi-cluster communication and federation scenarios without any manual networking setup.

### Worker lifecycle

Dynamically add and remove worker nodes from running clusters. Test how your workloads react to nodes joining and leaving — without touching the controller.

### Power cycle simulation

Shutdown, reboot, freeze, and power-cycle individual nodes to simulate real-world failure scenarios. Validate your fault-tolerance strategies before they matter.

### Minimal dependencies

sind needs nothing but Docker and a container image. Install a single binary and you're ready to go. sind is also usable as a Go library for embedding cluster management into your own tooling.

## Next steps

Ready to try it? Head to the [Getting Started]({{< relref "getting-started" >}}) section to install sind and create your first cluster.
