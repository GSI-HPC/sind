---
weight: 420
title: "Networking"
icon: "hub"
description: "Cluster networks, mesh, DNS, and SSH infrastructure"
toc: true
---

## Cluster network

Each cluster has an isolated Docker bridge network:

- Name: `sind-<cluster>-net` (e.g., `sind-dev-net`)
- Nodes can reach each other by container hostname within this network

## Mesh network

All clusters join a shared mesh network for cross-cluster communication:

| Event | Result |
|-------|--------|
| First cluster created | Creates `sind-mesh` network, starts `sind-dns` and `sind-ssh` |
| Subsequent clusters | Connects nodes to `sind-mesh`, updates DNS |
| Cluster deleted | Disconnects nodes, updates DNS |
| Last cluster deleted | Removes `sind-dns`, `sind-ssh`, and `sind-mesh` |

## DNS

The `sind-dns` container runs CoreDNS and provides name resolution across all clusters using a realm-aware zone:

```
<role>.<cluster>.<realm>.sind → container IP
```

Examples (default realm `sind`):

- `controller.default.sind.sind`
- `worker-0.dev.sind.sind`

Nodes are configured with:

```
--dns <sind-dns-ip>
--dns-search <cluster>.<realm>.sind
```

Within a cluster, short names work via the search domain: a node in the `dev` cluster can reach `controller` without the full `controller.dev.sind.sind`.

DNS records use each node's **cluster network IP** (not mesh network IP), so traffic between nodes routes through the cluster's isolated network.

The DNS container is lightweight — no systemd or sshd.

### Host DNS resolution

When systemd-resolved is available on the host, sind configures it to route `*.<realm>.sind` queries to the mesh DNS container. This enables resolving cluster node names directly from the host:

```bash
ping controller.default.sind.sind
resolvectl query worker-0.dev.sind.sind
```

For the default realm, a search domain is also configured so that bare hostnames resolve for the default cluster:

```bash
ping controller    # → controller.default.sind.sind
```

This feature is **best-effort**: it is silently skipped when systemd-resolved is not running or when the required polkit authorization is missing. Run `sind doctor` to check prerequisites and get a copyable command to install the polkit rule.

Host DNS is configured during `sind create cluster` (first cluster) and reverted during `sind delete cluster` (last cluster).

## SSH infrastructure

### Global resources

| Resource | Purpose |
|----------|---------|
| `sind-ssh` container | SSH client for accessing nodes |
| `sind-ssh-config` volume | SSH keypair and known_hosts |

The `sind-ssh-config` volume contains:

| File | Description |
|------|-------------|
| `id_ed25519` | Private key (generated on first cluster) |
| `id_ed25519.pub` | Public key (injected into nodes) |
| `known_hosts` | Host keys of all nodes (updated dynamically) |

### Lifecycle

| Event | Result |
|-------|--------|
| First cluster created | Generates Ed25519 keypair, starts `sind-ssh` |
| Cluster created | Connects `sind-ssh` to cluster network |
| Node created | Public key injected, host key collected and added to `known_hosts` |
| Node deleted | Entry removed from `known_hosts` |
| Cluster deleted | Disconnects `sind-ssh` from cluster network |
| Last cluster deleted | SSH container and volume removed |

The SSH relay connects to each cluster network so it can reach nodes at their cluster network IPs (which are the addresses registered in DNS).

### Key injection

The public key is injected into each node after container start:

```bash
docker exec <node> mkdir -p /root/.ssh
docker exec <node> sh -c 'cat >> /root/.ssh/authorized_keys' < pubkey
```

Host keys are then collected:

```bash
docker exec <node> cat /etc/ssh/ssh_host_ed25519_key.pub
```

And stored in `known_hosts` with the node's DNS name:

```
controller.dev.sind.sind ssh-ed25519 AAAA...
```

### Access model

sind only configures SSH access for the root user. Additional user management is left to the user.
