---
weight: 60
title: "Related Tools"
description: "Where sind sits among the other ways to run a Slurm cluster for development, tests and teaching"
toc: true
---

sind is one of many ways to get a Slurm cluster without a machine room. The projects on this page solve neighbouring problems with different trade-offs, and several of them work well together with sind. This page says what each approach is good at, where sind differs, and how to choose.

## The landscape

The approaches differ most in what stands in for a node. A lighter node starts faster, and more of them fit on one machine. A heavier node is closer to the machines that run production jobs.

```
  A node is ...             Examples
 ───────────────────────────────────────────────────────────────────────────────────
  a slurmd process          Slurm's multiple-slurmd build, Slurm simulators   ▲ lighter,
                                                                              │ starts faster
  one daemon in a           slurm-docker-cluster and other Compose setups,    │
  container                 single-container images and actions for CI        │
                                                                              │
  a system container        sind, FireHPC, Charmed HPC on LXD,                │
  with its own init         Magic Castle on Incus                             │
                                                                              │
  a virtual machine         Vagrant with Ansible, Puppet or Chef              │
                                                                              │
  a Kubernetes pod          Slinky slurm-operator, Soperator, SUNK            │
                                                                              │ closer to
  a cloud instance or       Magic Castle, StackHPC Slurm appliance,           ▼ production
  a real machine            the provisioning of your site
```

## sind in short

sind follows the model of [kind](https://kind.sigs.k8s.io/): one binary, one command for a default cluster, and one YAML file for any other. Each node is a Docker container that runs systemd, so munge, sshd and the Slurm daemons start as services, as on a real machine. sind writes the Slurm configuration, gives the nodes DNS names and SSH access, and removes all of it again with one command. It aims at tests that need several nodes, several clusters, or nodes that fail: on a laptop, and on a standard CI runner with [sind-action](https://github.com/GSI-HPC/sind-action).

These choices have a price, and some of the tools below do not pay it:

- sind needs Linux with a rootful Docker Engine and cgroup v2 (see [Installation]({{< relref "/getting-started/installation" >}})). It does not run on Docker Desktop, rootless Docker or Podman.
- All nodes share one host and one kernel. sind shows whether a job, a configuration or an integration is correct. It does not show how fast they are, and it does not set up GPUs or a high-speed interconnect.
- sind creates a cluster once and does not reconcile it afterwards (see [Design Overview]({{< relref "/architecture/overview" >}})). It is not a way to run a production cluster.
- The [official images]({{< relref "/container-images/building-images#official-images" >}}) are Rocky Linux with the supported Slurm release lines. Another distribution or an older Slurm needs an image of your own.

## Docker Compose clusters

[slurm-docker-cluster](https://github.com/giovtorres/slurm-docker-cluster) is a widely used Slurm cluster in containers, and many projects build on it. You clone the repository, and `make` and Docker Compose start one container for each Slurm daemon: the database, slurmdbd, slurmctld, slurmrestd and the compute nodes, which register themselves, so the cluster scales up and down while it runs. It also offers things that sind does not have: GPU compute nodes on NVIDIA's CUDA images, an Open OnDemand portal, job indexing with Elasticsearch and Kibana, and Spack with Lmod for software modules. [DockerSlurmCluster](https://github.com/NOAA-GSL/DockerSlurmCluster) of NOAA GSL is a smaller Compose cluster on Ubuntu, and many more exist, each shaped by the project that it serves.

The difference to sind is one of shape more than of result. A Compose cluster is a set of files that you own: you read them, change them and keep them next to your project. sind is a tool that keeps the same details out of sight, behind a config schema. A Compose service runs one daemon, where a sind node is a small machine with systemd, SSH and a journal. And sind manages many clusters side by side, where a Compose project is one cluster.

A Compose cluster is the better choice if you want to see and change every part of the setup, if you need its GPU nodes or its Open OnDemand portal, or if Compose is already how your project runs its other services.

## Single-node Slurm for CI

Many tests only need to know that a tool submits a job and reads its state. One node is enough for that, and the following projects give you one with less to learn than a multi-node tool:

- [setup-slurm-action](https://github.com/koesterlab/setup-slurm-action) installs Slurm on the GitHub Actions runner itself, with the [ansible-slurm](https://github.com/galaxyproject/ansible-slurm) role of the Galaxy project. The runner becomes a one-node cluster with accounting, and jobs run on it directly.
- [Slurm-Test-Environment](https://github.com/pitt-crc/Slurm-Test-Environment) of the University of Pittsburgh publishes one image for each of many Slurm versions, older ones included, with the control daemons, mock accounts and partitions. A matrix job then tests a tool against every version.
- [docker-slurm](https://github.com/nathan-hess/docker-slurm) provides Ubuntu images with a single-node Slurm, as a place to experiment or to run tests.

sind-action serves the tests that these do not reach: jobs that span nodes, MPI, nodes that leave and come back, controller failover, or more than one cluster. For a test that does not need them, a single node is simpler and starts sooner. For a wide range of Slurm versions, Slurm-Test-Environment covers more than sind's official images do.

## Other system containers

Docker is not the only way to run a container that behaves like a machine. Three projects do so with other container managers, and each brings a deployment method that real sites use.

[FireHPC](https://github.com/rackslab/FireHPC) of Rackslab is the closest relative of sind. It emulates HPC clusters in systemd-nspawn containers, several clusters at once, each in its own network. A [RacksDB](https://github.com/rackslab/RacksDB) file describes the cluster, and Ansible deploys it, on a choice of Linux distributions. The clusters come with an LDAP directory, Slurm accounting and REST API, and optionally with Slurm-web and a metrics stack. FireHPC is a good choice if you want an emulated cluster that is deployed the way a site deploys one, with a directory service, or if you develop for Slurm-web. sind needs only Docker on the host, which most CI runners have.

[Charmed HPC](https://documentation.ubuntu.com/charmed-hpc/latest/) of Canonical deploys Slurm with Juju charms. The same charms deploy to LXD on one machine and to clouds, so a test on a laptop uses the code that operates the production cluster. If Ubuntu and Juju are how you run Slurm, this is the most faithful local test that you can have.

[Magic Castle](https://github.com/ComputeCanada/magic_castle) of the Digital Research Alliance of Canada builds clusters with Terraform and Puppet that give users the experience of the Alliance's systems, with its software stack from CVMFS. It targets several clouds and, with Incus, also containers on your own machine.

## Virtual machines

A virtual machine has its own kernel, and that is what containers cannot give. With Vagrant or a similar tool, and Ansible, Puppet or Chef to provision the nodes, you can test kernel modules, the boot and provisioning of a node, security modules, clients of parallel file systems, and a node image as it will run on the hardware. Victor Penso's [Slurm test cluster with Vagrant](https://web-docs.gsi.de/~vpenso/notes/posts/hpc/cluster/slurm/vagrant-test-environment.html) shows a setup with libvirt, and NVIDIA DeepOps has a [virtual cluster](https://github.com/NVIDIA/deepops/tree/master/virtual) to try its playbooks before they run on hardware.

The price is time and memory: a cluster of virtual machines takes minutes to build and gigabytes to hold, and hosted CI runners do not always allow nested virtualization. The two approaches go well together. An [unmanaged cluster]({{< relref "/guides/unmanaged-cluster" >}}) runs the same Ansible or Chef code against sind nodes for a fast first check, and virtual machines then test what depends on the kernel.

## Slurm on Kubernetes

[Slinky](https://slinky.schedmd.com/) is SchedMD's project that joins Slurm and Kubernetes. Its [slurm-operator](https://github.com/SlinkyProject/slurm-operator) runs the Slurm daemons as pods and keeps the cluster in the state that its resources declare, and [slurm-bridge](https://github.com/SlinkyProject/slurm-bridge) lets Slurm schedule Kubernetes workloads. [Soperator](https://github.com/nebius/soperator) of Nebius is an open-source operator with a focus on GPU clusters for AI training, and [SUNK](https://docs.coreweave.com/products/sunk) is CoreWeave's offering of the same kind.

These projects operate Slurm in production: they upgrade, scale and repair a cluster, which sind does not do. They also run on a local Kubernetes for development, and the slurm-operator repository has a script that prepares a kind cluster for it. If your production cluster runs on Kubernetes, test with the operator and the charts that you will deploy. sind is for the classic layout, where Slurm daemons run as services on machines, and it needs no Kubernetes.

Both can run on one host. [Connecting kind]({{< relref "/guides/kind" >}}) attaches the nodes of a kind cluster to a sind realm, so that pods reach sind's Slurm nodes by name, for example to develop a Kubernetes service that submits jobs to a Slurm cluster.

## Emulation inside Slurm

Slurm itself can [emulate a larger cluster](https://slurm.schedmd.com/faq.html#multi_slurmd): built with `--enable-multiple-slurmd`, one host runs many slurmd daemons, each as a node of its own. This is the lightest way to give slurmctld hundreds or thousands of nodes, for tests of scheduler behaviour at a size that no laptop holds as containers. The nodes are not isolated from each other, so it suits tests of the scheduler more than tests of jobs.

The [Slurm simulator](https://github.com/ubccr-slurm-simulator/slurm_sim_tools) of the University at Buffalo goes one step further and replays a workload faster than real time, to study how scheduling parameters change wait times and utilization. sind runs real jobs in real time, on as many nodes as the host has memory for. It answers whether a setup works, and these tools answer how a policy behaves at scale.

## Clouds and real hardware

Performance, GPUs, interconnects, parallel file systems and behaviour at full scale need the real thing. Magic Castle, the [StackHPC Slurm appliance](https://github.com/stackhpc/ansible-slurm-appliance), which is tested on OpenStack, and the cluster services of the cloud providers build such clusters on demand, and a test partition of your own cluster is often the best test bed of all. sind finds the errors that do not need hardware before they cost an allocation.

## How to choose

| You want to | Look first at |
|-------------|---------------|
| Run multi-node Slurm tests on a laptop or a standard CI runner, with one command | sind |
| Own and change every file of the setup, or have GPU nodes or Open OnDemand included | [slurm-docker-cluster](#docker-compose-clusters) |
| Have one Slurm node in a CI job with the least setup | [setup-slurm-action, docker-slurm](#single-node-slurm-for-ci) |
| Test a tool against many Slurm versions, older ones included | [Slurm-Test-Environment](#single-node-slurm-for-ci) |
| Emulate a cluster with a directory service, deployed with Ansible | [FireHPC](#other-system-containers) |
| Test locally with the charms that operate your cluster | [Charmed HPC](#other-system-containers) |
| Test what depends on the kernel: modules, boot, provisioning, file system clients | [Virtual machines](#virtual-machines) |
| Operate Slurm on Kubernetes, or develop for it | [Slinky, Soperator](#slurm-on-kubernetes) |
| Study the scheduler with thousands of nodes or months of jobs | [multiple slurmd, Slurm simulator](#emulation-inside-slurm) |
| Measure performance, or test GPUs and interconnects | [A cloud cluster or real hardware](#clouds-and-real-hardware) |

Many teams use more than one of these: a fast check on every commit, a slower and more faithful one before a release, and the real cluster at the end.

## Corrections

These projects change quickly, and their authors know them best. If a description here is out of date or wrong, or a project is missing, please open an [issue](https://github.com/GSI-HPC/sind/issues) or a pull request.
