---
weight: 357
title: "Users and Identity"
icon: "group"
description: "Run jobs as regular users with Slurm accounts, and test where your site resolves them"
toc: true
---

sind runs everything as root unless the cluster config declares users. With [`users`, `groups`]({{< relref "/configuration/cluster-config#users-section" >}}) and [`accounts`]({{< relref "/configuration/cluster-config#accounts-section" >}}), a cluster gets Linux user accounts, Slurm accounts and associations. The [identity mode]({{< relref "/configuration/cluster-config#identity-section" >}}) decides which nodes have the Linux accounts, to mirror how a site resolves its users.

## Users and accounts

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

Every node gets the users and groups with the same IDs, and each user a home directory on the shared home volume, `/home/<user>`. Once slurmctld has registered the cluster, sind creates the account tree with `sacctmgr`:

```
root
├── root          user root
└── physics       GrpTRES=cpu=4
    ├── bob       default · coordinator of physics
    ├── carol     default · AdminLevel=Operator
    └── theory    MaxJobs=1
        ├── alice default
        └── bob
```

With the enforcement in `slurm.main`, this tests that alice can have one job at a time, that all of physics shares 4 of the cluster's 6 CPUs (three workers with two each), so that a fifth CPU requested under physics stays pending with the reason `AssocGrpCpuLimit` in `squeue`, that bob can manage limits and users below physics, that carol can run operator commands, and that dave's `sbatch` is rejected with "Invalid account or account/partition combination specified". The Linux group `physics` and the Slurm account `physics` are unrelated; they only share a name, as they often do at real sites.

Run commands as a user:

```bash
sind ssh alice@submitter -- sbatch --wrap hostname
sind exec --user alice -- squeue
sind enter --user bob
```

## Identity modes

Slurm needs a user's uid and groups on the nodes that run its daemons and jobs. Sites provide them in different ways, and sind can mirror three of them:

| | `local` (default) | `nssSlurm` | `clientIds` |
|---|---|---|---|
| Slurm settings | munge | munge, `LaunchParameters=enable_nss_slurm` | `AuthType=auth/slurm`, `CredType=cred/slurm`, `AuthInfo=use_client_ids`, `enable_nss_slurm` |
| Linux accounts on | every node | every node but the managed workers: controllers, db, api, submitter | the login node: the submitter, or the controllers without one; the controllers too with `controllerUsers` |
| Authentication key | munge key | munge key | `slurm.key`; munge is masked, `sackd` runs on the submitter |
| SSH as a user to | every node | every node but the managed workers | login node, and the controllers with `controllerUsers` |
| Prolog, epilog and health checks know user names | yes | not on workers | not on workers |
| A job sees other users' names (`ls -l`, `id bob`) | yes | no | no |
| Mirrors a site where | every node has LDAP or SSSD | compute nodes have no directory service | only login nodes have a directory service |

How each mode resolves a user:

- **Controller:** with `local` and `nssSlurm`, slurmctld looks up each job's user and groups, and the uids of Slurm users, in its own passwd and group files. With `clientIds` it takes them from the user's token, which `sackd` on the login node creates with the caller's passwd and group entries. slurmctld then fills in the uid on the user's associations, before it handles the request, so even the first `sbatch` under `AccountingStorageEnforce` finds the association.
- **Submitter:** users run `sbatch` and `srun` there in every mode.
- **db:** slurmdbd checks admin levels, coordinators and default-account changes against its own lookups, or, with `clientIds`, the uids it learns from tokens.
- **Workers:** with `nssSlurm` and `clientIds`, sind puts `slurm` first on the `passwd` and `group` lines of `/etc/nsswitch.conf`. nss_slurm answers only for the job's own user and groups, from the job credential, and only to processes inside a job step. sshd, prolog, epilog and health checks run outside a step, so SSH as a user to a worker fails by design.
- **Every node:** root and the `slurm` user stay local. Unmanaged nodes, such as `managed: false` workers, get the Linux accounts as with `local`, since sind does not manage their Slurm.

### Choosing a mode

1. **No users other than root?** Leave out `users`. The same goes for testing your own account provisioning: leave out `users` and let it create the accounts.
2. **Need SSH as a user to workers, or prolog and epilog scripts that look up users?** Use `local`.
3. **Mirroring a site?** Pick the mode it runs: `clientIds` for `auth/slurm` with `use_client_ids`, `nssSlurm` for nss_slurm with munge, `local` otherwise.
4. **Making sure job scripts and tools don't depend on lookups on compute nodes?** Use `nssSlurm`, even if the site runs `local`. It is the stricter test.

### nssSlurm

```yaml
kind: Cluster
identity: nssSlurm
nodes: [controller, db, submitter, worker: 2]
users:
  - name: alice
    uid: 2001
```

| Node | alice in `/etc/passwd` | How alice resolves |
|------|------------------------|--------------------|
| controller, submitter, db | yes | `files` |
| worker-0, worker-1 | no | inside job steps: nss_slurm; outside: not at all |

`srun` and `sbatch` work as before; inside a job, `id`, `ls -l ~` and `getent passwd alice` see alice. `sind ssh alice@worker-0` fails. sind checks that each managed worker's image has `libnss_slurm.so.2` and fails `sind create cluster` and `sind create worker` with the image's name if not. If you used sind before identity modes existed, Docker may still have the official image of that time cached under the same tag: sind refuses it before it creates anything and tells you to pull a current one with `--pull`.

### clientIds

```yaml
kind: Cluster
identity: clientIds
nodes: [controller, db, submitter, worker: 2]
users:
  - name: alice
    uid: 2001
    accounts: [physics]
accounts:
  - physics
```

| Node | alice in `/etc/passwd` | How alice resolves |
|------|------------------------|--------------------|
| submitter | yes | `files`; `sackd` puts her entries into each token |
| controller, db | no | from her signed tokens, learned on her first request |
| worker-0, worker-1 | no | inside job steps: nss_slurm; outside: not at all |

sind generates `/etc/slurm/slurm.key`, readable only by `slurm`, instead of a munge key, masks `munge.service` on every node and enables `sackd` on the submitter. `sind get cluster` lists `sackd` on the submitter and munge nowhere, and `sind get auth-key` prints `slurm.key`. Root's client commands on the controller, db node and workers get their tokens from slurmctld, slurmdbd and slurmd.

slurmctld checks a partition's `AllowGroups` with local lookups only. With such a partition, give the controllers the users and groups too:

```yaml
identity:
  mode: clientIds
  controllerUsers: true
```

Without a submitter, the controller is the login node and gets the accounts anyway.

With an [api node]({{< relref "/configuration/node-definitions#api-node" >}}), REST API tokens from `scontrol token` carry only the user name, which slurmctld and slurmdbd cannot look up under `clientIds`. sind lets tokens carry the user's identity there instead; see [REST API]({{< relref "/guides/rest-api#identity-modes" >}}).
