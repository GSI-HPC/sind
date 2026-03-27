---
weight: 360
title: "Node Arguments"
icon: "label"
description: "DNS-style node naming and nodeset expansion"
toc: true
---

## Format

Commands that accept node arguments use DNS-style names:

```
<role>.<cluster>
<role>-<N>.<cluster>
```

The cluster suffix defaults to `default` if omitted:

```bash
sind ssh controller          # → controller.default
sind ssh worker-0            # → worker-0.default
sind ssh worker-0.dev        # → worker-0.dev
```

## Nodeset expansion

sind supports Slurm-style nodeset notation for specifying multiple nodes:

| Pattern | Expansion |
|---------|-----------|
| `worker-[0-3]` | worker-0, worker-1, worker-2, worker-3 |
| `worker-[0,2,4]` | worker-0, worker-2, worker-4 |
| `worker-[0-2,5]` | worker-0, worker-1, worker-2, worker-5 |
| `worker-[0-1].dev` | worker-0.dev, worker-1.dev |
| `worker-[00-03]` | worker-00, worker-01, worker-02, worker-03 |

Zero-padding is preserved: `worker-[00-03]` produces `worker-00` through `worker-03`.

## Multiple nodes

Comma-separate multiple nodesets:

```bash
sind power shutdown controller,worker-[0-3]
sind power cycle worker-[0-1].dev,worker-[0-3].default
```

## Commands that accept nodeset arguments

| Command | Accepts nodesets |
|---------|-----------------|
| `sind power <action>` | Yes — one or more nodes |
| `sind delete worker` | Yes — one or more nodes |
| `sind ssh` | No — exactly one node |
| `sind logs` | No — exactly one node |
