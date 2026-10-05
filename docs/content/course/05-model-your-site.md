---
weight: 75
title: "05 · Model your site"
description: "A config file: nodes, limits, users and accounts, CVMFS"
---

{{< video "course-05-model-your-site" >}}

A fresh test cluster runs everything as root, with no Slurm accounts and no job limits. For an admin or research software engineer who wants to test against something shaped like their site, this episode builds one config file step by step, with node roles, CPU limits, users with Slurm accounts and limits, and software from CVMFS, and then creates the cluster from it.

## In this episode

- Node roles in the shorthand `nodes` list: [Node Definitions]({{< relref "/configuration/node-definitions" >}})
- CPU and memory limits for every node: [Cluster Configuration]({{< relref "/configuration/cluster-config#defaults-section" >}})
- Users, groups, Slurm accounts and their limits: [Users and Identity]({{< relref "/guides/users" >}})
- Software from CVMFS on every node: [Using CVMFS]({{< relref "/guides/cvmfs" >}})
- A cluster from a config file: [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle" >}})

## Commands

The episode builds `cluster.yaml` from the example in [Users and Identity]({{< relref "/guides/users#users-and-accounts" >}}):

```yaml
kind: Cluster
defaults:
  cpus: 2
nodes: [controller, db, submitter, worker: 3]
slurm:
  main: |
    AccountingStorageEnforce=associations,limits
groups:
  - name: physics
    gid: 3000
accounts:
  - name: physics
    limits:
      GrpTRES: cpu=4
  - name: theory
    parent: physics
    limits:
      MaxJobs: 1
users:
  - name: alice
    uid: 2001
    groups: [physics]
    accounts: [theory]
  - name: bob
    uid: 2002
    groups: [physics]
    accounts: [physics, theory]
    coordinator: [physics]
  - name: carol
    uid: 2003
    accounts: [physics]
    adminLevel: operator
  - name: dave
    uid: 2004
```

It adds the storage section from [Using CVMFS]({{< relref "/guides/cvmfs#mount-cvmfs-on-the-nodes" >}}), which needs CVMFS on the Docker host or a `cvmfs` Docker volume plugin:

```yaml
storage:
  cvmfs: true
```

Create the cluster:

```bash
sind create cluster --config cluster.yaml
```

Run commands as a user:

```bash
sind ssh alice@submitter -- sbatch --wrap hostname
sind exec --user alice -- squeue
sind enter --user bob
```

Once slurmctld has registered the cluster, sind creates this account tree with `sacctmgr`:

```text
root
├── root          user root
└── physics       GrpTRES=cpu=4
    ├── bob       default · coordinator of physics
    ├── carol     default · AdminLevel=Operator
    └── theory    MaxJobs=1
        ├── alice default
        └── bob
```

## Refresher: Accounting and associations

slurmdbd stores the jobs, and associations tie users to accounts on a cluster and carry the limits; see [the full refresher]({{< relref "refreshers#accounting-and-associations" >}}).

## Go deeper

- [Cluster Configuration]({{< relref "/configuration/cluster-config" >}}): every field of the config file, including `accounts`, `users` and `identity`
- [Node Definitions]({{< relref "/configuration/node-definitions" >}}): roles, per-node resources and the db node
- [Users and Identity]({{< relref "/guides/users" >}}): what the example tests, and the identity modes
- [Using CVMFS]({{< relref "/guides/cvmfs" >}}): the two CVMFS backends, and provisioning the client inside the nodes
- [Cluster Lifecycle]({{< relref "/usage/cluster-lifecycle" >}}): creating, listing and deleting clusters
