---
weight: 360
title: "Node Arguments"
icon: "label"
description: "DNS-style node naming and node set expressions"
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

sind splits a name at its last `.`: what follows is the cluster, which must be a valid cluster name.

## Node sets

A node argument is a node set expression in the syntax of [ClusterShell](https://clustershell.readthedocs.io/en/latest/tools/nodeset.html), which Slurm and pdsh users know too. sind parses it with [go-nodeset](https://github.com/GSI-HPC/go-nodeset); its [language reference](https://github.com/GSI-HPC/go-nodeset/blob/v1.0.0/doc/language.md) has the full rules.

| Expression | Nodes |
|------------|-------|
| `worker-[0-3]` | worker-0, worker-1, worker-2, worker-3 |
| `worker-[0,2,4]` | worker-0, worker-2, worker-4 |
| `worker-[0-2,5]` | worker-0, worker-1, worker-2, worker-5 |
| `worker-[0-6/2]` | worker-0, worker-2, worker-4, worker-6 (a step) |
| `worker-[0-1].dev` | worker-0.dev, worker-1.dev |
| `worker-[0-1].dev[1-2]` | worker-0.dev1, worker-0.dev2, worker-1.dev1, worker-1.dev2 |

Every run of digits in a name is a number, the cluster's included. A bracket may stand for any of them, and the padding and order rules below apply to each.

## Set operators

Operators combine node sets:

| Operator | Meaning | Example | Nodes |
|----------|---------|---------|-------|
| `,` or whitespace | union | `controller,worker-0` | controller, worker-0 |
| `!` | difference | `worker-[0-5]!worker-[2-3]` | worker-0, worker-1, worker-4, worker-5 |
| `&` | intersection | `worker-[0-5]&worker-[4-9]` | worker-4, worker-5 |
| `^` | symmetric difference | `worker-[0-3]^worker-[2-5]` | worker-0, worker-1, worker-4, worker-5 |

The operators have no precedence: sind evaluates an expression from left to right, so `worker-[0-9]!worker-[0-4]&worker-[3-6]` is worker-5 and worker-6.

`!`, `&` and `^` need an operand on each side, so `worker-[0-3]&` is an error rather than worker-0 to worker-3. An empty operand of a union is nothing: `worker-0,` is worker-0.

A command joins its node arguments with a space, which is a union, so these name the same three nodes:

```bash
sind power on 'worker-[0-3]!worker-2'
sind power on worker-[0-3] '!worker-2'
sind power on worker-0 worker-1 worker-3
```

Quote an expression with `!`, `&` or `^`, which the shell would otherwise read itself.

The operators compare the names as written, cluster suffix included: `worker-[0-3]!worker-2.default` removes nothing, because `worker-2.default` is not one of the names `worker-[0-3]` gives. Write the suffix the same way throughout an expression.

## Zero padding

Zero padding is not part of a node's identity: `worker-1,worker-01` is one node, and `worker-[1-3]!worker-02` is worker-1 and worker-3.

This holds for the numbers of the cluster suffix too, but `dev1` and `dev01` are two clusters to sind. An expression whose names differ only in the padding of their cluster, such as `worker-0.dev1,worker-0.dev01`, would act on one of the two nodes, so sind rejects it; name the nodes of each such cluster in a command of its own.

A node keeps the spelling it was first given, and sind looks the node up by that spelling: `worker-01,worker-1` is worker-01. In a range, the padding of the first bound applies to the whole range: `worker-[00-03]` is worker-00 to worker-03. sind names its workers without padding, so that range names none of them.

## Order

sind acts on the nodes in the order go-nodeset lists them:

1. by the name with each number taken as a placeholder that sorts before letters, `-` and `.`,
2. then by number, the last number varying fastest.

`worker-10 worker-2.dev controller worker-1` is controller, worker-1, worker-10, worker-2.dev. `sind power` and `sind delete worker` take the nodes cluster by cluster, the clusters in the order of their first node.

## Errors

sind rejects these before it acts on any node, with exit status 2:

- a malformed expression, such as `worker-[`, `worker-[3-1]` or `worker-[1-010]` (bounds padded to different widths)
- a group, such as `@compute`: sind has no node groups
- an expression that names no node, such as `worker-[0-1]!worker-[0-1]`
- an expression that names more than 1,048,576 (2^20) nodes, refused before the names are allocated
- a name that begins with `-`, which a command it is passed to could read as an option
- a name with an empty short name or cluster, or an invalid cluster name: `.dev`, `worker-0.`, `worker-0.my_cluster`
- names that differ only in the zero padding of their cluster, which a node set takes for one node: `worker-0.dev1,worker-0.dev01`

## Commands that accept node sets

| Command | Accepts node sets |
|---------|-------------------|
| `sind power <action>` | Yes — one or more nodes |
| `sind delete worker` | Yes — one or more nodes |
| `sind ssh` | No — exactly one node |
| `sind logs` | No — exactly one node |
