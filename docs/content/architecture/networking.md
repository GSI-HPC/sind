---
weight: 420
title: "Networking"
icon: "hub"
description: "Cluster networks, mesh, DNS, and SSH infrastructure"
toc: true
---

## Cluster network

Each cluster has an isolated Docker bridge network:

- Name: `<realm>-<cluster>-net` (e.g., `sind-dev-net`)
- Nodes can reach each other by container hostname within this network

Slurm uses these short hostnames (`controller`, `db`, `worker-0`). Docker's embedded DNS answers them from the first of a node's networks that knows the name, and a node's hostname is a name on the mesh network too, once per cluster of the realm. Nodes therefore join their cluster network with gateway priority 1, ahead of the mesh's 0 (`--network name=<realm>-<cluster>-net,gw-priority=1`, Docker 28 or later), so the names resolve to the node's own cluster whatever the clusters are called. Without the priority, Docker orders the networks by name, and in a cluster whose network sorts after `<realm>-mesh`, such as `test` or `prod`, `controller` would resolve to the controllers of every cluster in the realm. A name the cluster lacks, such as `db` in a cluster without a db node, still falls through to the mesh and reaches another cluster's node. The cluster network is also the nodes' default gateway.

## Mesh network

Every node also joins its realm's mesh network, which carries the realm's DNS server and SSH relay:

| Event | Result |
|-------|--------|
| First cluster created | Creates `sind-mesh` network, starts `sind-dns` and `sind-ssh` |
| Subsequent clusters | Connects nodes to `sind-mesh`, updates DNS |
| Cluster deleted | Disconnects nodes, updates DNS (unless it was the last cluster) |
| Last cluster deleted | Removes `sind-dns`, `sind-ssh`, and `sind-mesh` |

The mesh does not route traffic between clusters by their DNS names: the `*.<realm>.sind` names resolve to cluster network addresses (see below), which the SSH relay and the host reach, but the nodes of other clusters do not, as Docker isolates bridge networks from each other. Nodes of different clusters reach each other on the mesh by container name, such as `sind-dev-controller`.

## DNS

The `sind-dns` container runs CoreDNS and names every node of the realm, for the SSH relay and the host, using a realm-aware zone:

```
<node>.<cluster>.<realm>.sind → container IP
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

DNS records use each node's **cluster network IP** (not mesh network IP), so the SSH relay and the host reach the nodes through the cluster's network.

sind writes the records when it creates or deletes nodes, and again when `sind power on`, `reboot` or `cycle` start nodes: Docker can give a node another address each time it starts. CoreDNS reloads its configuration in place on `SIGUSR1`, without dropping queries.

The DNS container is lightweight — no systemd or sshd.

### After a host reboot or a Docker daemon restart

sind sets no restart policy, so the mesh containers and the nodes stay stopped after a host reboot or a Docker daemon restart. `sind get cluster` then shows `dns` and `ssh` with ✗. `sind power on` starts them again: first the realm's DNS container, then the SSH relay, then the nodes, whose DNS records it points at their new addresses. It also applies host DNS again. `sind create cluster` and `sind create worker` start a stopped mesh too.

```bash
sind power on controller,worker-[0-1]
```

Docker fixes the DNS server of the nodes and the relay (`--dns`) when it creates them. Starting the DNS container before anything else on the mesh gets it its old address back. If it gets another one, for example after containers were started by hand in another order, sind prints a warning: the containers created before resolve neither `*.<realm>.sind` names nor external names until they are created again. Delete and create the affected clusters to repair them; the SSH relay is created again with the realm's mesh after its last cluster is deleted.

### Host DNS resolution

When systemd-resolved is available on the host, sind configures it to route `*.<realm>.sind` queries to the mesh DNS container. This enables resolving cluster node names directly from the host:

```bash
ping controller.default.sind.sind
resolvectl query worker-0.dev.sind.sind
```

sind also sets the search domain `default.<realm>.sind` on the mesh bridge, in every realm, so that bare hostnames resolve to the realm's `default` cluster:

```bash
ping controller    # → controller.default.sind.sind
```

This feature is **best-effort**: it is silently skipped when systemd-resolved is not running or when the required polkit authorization is missing. Run `sind doctor` to check prerequisites and get a copyable install command.

Host DNS is configured during every `sind create cluster` (the settings are idempotent), when `sind power on` starts a stopped mesh, and reverted during `sind delete cluster` (last cluster).

#### Polkit policy

sind needs polkit authorization to configure systemd-resolved. Install one of these rules depending on how you access the Docker host:

{{< tabs "polkit" >}}
{{< tab "Desktop" >}}

Allows docker group members to configure DNS from local sessions only (direct keyboard/display access, not SSH):

```bash
sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules <<'RULES'
polkit.addRule(function(action, subject) {
    if (["org.freedesktop.resolve1.set-dns-servers",
         "org.freedesktop.resolve1.set-domains",
         "org.freedesktop.resolve1.revert"].indexOf(action.id) >= 0 &&
        subject.isInGroup("docker") &&
        subject.active && subject.local) {
        return polkit.Result.YES;
    }
});
RULES
```

{{< /tab >}}
{{< tab "Server" >}}

Allows docker group members to configure DNS from any active session, including SSH:

```bash
sudo tee /etc/polkit-1/rules.d/50-sind-resolved.rules <<'RULES'
polkit.addRule(function(action, subject) {
    if (["org.freedesktop.resolve1.set-dns-servers",
         "org.freedesktop.resolve1.set-domains",
         "org.freedesktop.resolve1.revert"].indexOf(action.id) >= 0 &&
        subject.isInGroup("docker") &&
        subject.active) {
        return polkit.Result.YES;
    }
});
RULES
```

{{< /tab >}}
{{< /tabs >}}

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

Once sshd runs on a node, one `docker exec` injects the public key and collects the host key that sshd serves:

```bash
docker exec <node> sh -c 'mkdir -p /root/.ssh && printf "%s\n" "$1" >> /root/.ssh/authorized_keys && ssh-keyscan -t ed25519 localhost' sh "$pubkey"
```

The host key is stored in `known_hosts` with the node's DNS name:

```
controller.dev.sind.sind ssh-ed25519 AAAA...
```

### Access model

sind configures SSH access for root and for the cluster [users]({{< relref "/configuration/cluster-config#users-section" >}}): the realm's key is in each of their `authorized_keys`. Anything beyond that, such as sudo or keys for logins between nodes, is left to the user.
