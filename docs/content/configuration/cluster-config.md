---
weight: 210
title: "Cluster Configuration"
icon: "description"
description: "YAML configuration schema reference"
toc: true
---

## Minimal configuration

The simplest valid configuration creates a cluster with one controller and one worker using the default image:

```yaml
kind: Cluster
```

This is equivalent to the fully expanded form:

```yaml
kind: Cluster
name: default
defaults:
  image: ghcr.io/gsi-hpc/sind-node:latest
  cpus: 1
  memory: 512m
  tmpSize: 256m
nodes:
  - role: controller
  - role: worker
```

## Full example

```yaml
kind: Cluster
name: test-cluster

defaults:
  image: ghcr.io/gsi-hpc/sind-node:latest
  cpus: 1
  memory: 512m
  tmpSize: 256m

storage:
  dataStorage:
    type: volume
    mountPath: /data

slurm:
  main: |
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |
    ConstrainCores=yes

nodes:
  - role: controller
    cpus: 2
    memory: 1g
    tmpSize: 512m

  - role: submitter

  - role: worker
    count: 3
    cpus: 2
    memory: 1g

  - role: worker
    count: 2
    managed: false
```

## Top-level fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `kind` | yes | — | Must be `"Cluster"` |
| `name` | no | `"default"` | Cluster name, used in resource naming |
| `realm` | no | `"sind"` | Realm namespace for resource isolation |
| `defaults` | no | — | Default settings applied to all nodes |
| `storage` | no | — | Shared storage configuration |
| `slurm` | no | — | Slurm configuration extension |
| `nodes` | no | 1 controller + 1 worker | Node definitions |

## Defaults section

The `defaults` section sets values inherited by all nodes unless overridden at the node level.

| Field | Default | Description |
|-------|---------|-------------|
| `image` | `ghcr.io/gsi-hpc/sind-node:latest` | Container image |
| `cpus` | `1` | CPU limit per container |
| `memory` | `"512m"` | Memory limit per container |
| `tmpSize` | `"256m"` | tmpfs size for `/tmp` |
| `capAdd` | none | Extra Linux capabilities |
| `capDrop` | none | Dropped Linux capabilities |
| `devices` | none | Host devices to expose |
| `securityOpt` | none | Extra security options |

Scalar fields (`image`, `cpus`, `memory`, `tmpSize`) are overridden by per-node values. List fields (`capAdd`, `capDrop`, `devices`, `securityOpt`) are merged with per-node values.

## Storage section

```yaml
storage:
  dataStorage:
    type: volume       # "volume" or "hostPath"
    hostPath: ./data   # required if type is hostPath
    mountPath: /data   # default: /data
```

| Field | Default | Description |
|-------|---------|-------------|
| `type` | `"volume"` | `"volume"` for a Docker volume, `"hostPath"` for a bind mount |
| `hostPath` | — | Path on the host (required when `type: hostPath`) |
| `mountPath` | `"/data"` | Mount point inside containers |

## Slurm section

The `slurm` section extends the generated Slurm configuration. Each key maps to a config file:

| Key | Config file | sind generates defaults |
|-----|-------------|:----------------------:|
| `main` | `slurm.conf` | yes |
| `cgroup` | `cgroup.conf` | yes |
| `gres` | `gres.conf` | no |
| `topology` | `topology.conf` | no |
| `plugstack` | `plugstack.conf` | yes (always scaffolded) |

Each key supports two forms:

**String form** — content appended to the config file:

```yaml
slurm:
  main: |
    SelectType=select/cons_tres
    SelectTypeParameters=CR_Core_Memory
  cgroup: |
    ConstrainCores=yes
```

**Map form** — named fragments placed in a `.conf.d/` directory:

```yaml
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
      SchedulerParameters=bf_continue
    resources: |
      SelectType=select/cons_tres
```

Fragment validation:

- Names must be plain filenames (no path separators)
- Names and content must not be empty

See [Slurm Configuration]({{< relref "/architecture/slurm-config" >}}) for details on the generated files.

## Validation rules

- `kind` must be `"Cluster"`
- Exactly one `controller` node is required
- At most one `submitter` node is allowed
- At least one `worker` node is required
- `count` is only valid for worker nodes
- `managed` is only valid for worker nodes
- `count` must not be negative
- `capAdd`/`capDrop` values must be recognized Linux capability names (e.g. `SYS_ADMIN`, `NET_ADMIN`, `ALL`)
- `devices` paths must be absolute (start with `/`)
