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
| `nodes` | no | 1 controller + 1 worker | Node definitions |

## Defaults section

The `defaults` section sets values inherited by all nodes unless overridden at the node level.

| Field | Default | Description |
|-------|---------|-------------|
| `image` | `ghcr.io/gsi-hpc/sind-node:latest` | Container image |
| `cpus` | `1` | CPU limit per container |
| `memory` | `"512m"` | Memory limit per container |
| `tmpSize` | `"256m"` | tmpfs size for `/tmp` |

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

## Validation rules

- `kind` must be `"Cluster"`
- Exactly one `controller` node is required
- At most one `submitter` node is allowed
- At least one `worker` node is required
- `count` is only valid for worker nodes
- `managed` is only valid for worker nodes
- `count` must not be negative
