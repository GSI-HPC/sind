---
weight: 70
title: "Video Course"
icon: "smart_display"
description: "Learn sind in short videos: a numbered course, refreshers, and clips on the guide pages"
---

Learn sind from Sindy, the course's virtual presenter, as she builds clusters, runs jobs and breaks things on purpose. Every video has captions and chapters, and every command in it is on the episode's page, ready to copy.

- **The course:** ten numbered episodes of a few minutes each, from the first cluster to CI pipelines and AI assistants. Each episode stands on its own; the numbers suggest an order, not a prerequisite.
- **[Refreshers]({{< relref "refreshers" >}}):** about a minute of background each, on Slurm, Docker networks, MPI, accounting and more. Shortened versions also appear in the course episodes.
- **Guide clips:** a short video at the top of most hands-on guides, showing that page in action.

## Start here

{{< video "course-01-why-slurm-in-docker" >}}

## The course

| # | Episode | What you'll see | Refresher |
| --- | --- | --- | --- |
| 01 | [Why Slurm in Docker?]({{< relref "01-why-slurm-in-docker" >}}) | Testing on a real cluster is slow and risky; here is a cluster in seconds instead | Slurm in 90 seconds |
| 02 | [Your first cluster]({{< relref "02-first-cluster" >}}) | Install, doctor, create, submit, ssh in, delete | |
| 03 | [Inside a sind cluster]({{< relref "03-inside-a-cluster" >}}) | Roles, networks, DNS and the SSH relay, drawn and then explored | Docker networks and DNS |
| 04 | [Test your job scripts before the queue]({{< relref "04-job-scripts" >}}) | The data directory, sbatch and srun, MPI across workers | MPI ranks and srun |
| 05 | [Model your site]({{< relref "05-model-your-site" >}}) | A config file: nodes, limits, users and accounts, CVMFS | Accounting and associations |
| 06 | [Rehearse a change]({{< relref "06-rehearse-a-change" >}}) | A slurm.conf change, or the next Slurm release, before production gets it | slurm.conf on one screen |
| 07 | [Failure drills]({{< relref "07-failure-drills" >}}) | Power off, freeze, scale and fail over, and see how Slurm notices | Node states |
| 08 | [Slurm in your pipeline]({{< relref "08-slurm-in-ci" >}}) | sind-action, realms for parallel jobs, fast failure tests | |
| 09 | [Bring your own provisioning]({{< relref "09-own-provisioning" >}}) | Unmanaged clusters for Ansible or Chef, custom node images | systemd as PID 1 |
| 10 | [Let your assistant drive]({{< relref "10-assistant" >}}) | MCP, and an AI assistant that builds and debugs a cluster | What MCP is |

## Paths through the course

- **Deciding whether sind fits:** 01, then whichever of 06, 07 and 08 matches your use case.
- **Testing job scripts and MPI codes:** 01, 02 and 04, then the [MPI]({{< relref "/guides/mpi-jobs" >}}) and [CVMFS]({{< relref "/guides/cvmfs" >}}) guide clips.
- **Running a Slurm site:** 01, 05, 06, 07 and 09, then the [Controller Failover]({{< relref "/guides/controller-failover" >}}), [Users and Identity]({{< relref "/guides/users" >}}) and [Unmanaged Cluster]({{< relref "/guides/unmanaged-cluster" >}}) clips.
- **Building software that talks to Slurm:** 01, 02, 08 and 10, then the [CI/CD]({{< relref "/guides/ci-cd" >}}) clip.
- **Learning Slurm:** the whole course in order, with the refreshers as they come up.

## Guide clips

These guide pages open with a clip of one to two minutes:

| Section | Pages |
| --- | --- |
| Getting Started | [Installation]({{< relref "/getting-started/installation" >}}) |
| Configuration | [Cluster Configuration]({{< relref "/configuration/cluster-config" >}}), [Node Definitions]({{< relref "/configuration/node-definitions" >}}), [Realms]({{< relref "/configuration/realms" >}}) |
| Usage | [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle" >}}), [Node Access]({{< relref "/usage/node-access" >}}), [Worker Management]({{< relref "/usage/worker-management" >}}), [Power Control]({{< relref "/usage/power-control" >}}), [Diagnostics]({{< relref "/usage/diagnostics" >}}) |
| Guides | [Running MPI Jobs]({{< relref "/guides/mpi-jobs" >}}), [CI/CD]({{< relref "/guides/ci-cd" >}}), [Controller Failover]({{< relref "/guides/controller-failover" >}}), [Unmanaged Cluster]({{< relref "/guides/unmanaged-cluster" >}}), [Using CVMFS]({{< relref "/guides/cvmfs" >}}), [MCP Integration]({{< relref "/guides/mcp" >}}), [Users and Identity]({{< relref "/guides/users" >}}) |
| Container Images | [Building Images]({{< relref "/container-images/building-images" >}}) |
| Troubleshooting | [Container exits with code 255]({{< relref "/troubleshooting/container-exit-255" >}}) |
