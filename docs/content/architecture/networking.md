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

Slurm uses these short hostnames (`controller`, `db`, `worker-0`). Docker's embedded DNS answers them from the first of a node's networks that knows the name, and a node's hostname is a name on the mesh network too, once per cluster of the realm. Nodes therefore join their cluster network with gateway priority 1, ahead of the mesh's 0 (`--network name=<realm>-<cluster>-net,gw-priority=1`, Docker 28 or later), so the names resolve to the node's own cluster whatever the clusters are called. Without the priority, Docker orders the networks by name, and in a cluster whose network sorts after `<realm>-mesh`, such as `test` or `prod`, `controller` would resolve to the controllers of every cluster in the realm. A name the cluster lacks, such as `db` in a cluster without a db node, still falls through to the mesh and reaches another cluster's node. The cluster network is also the nodes' default gateway. `docker create` attaches a node to both networks, the cluster network first (`--network name=<realm>-<cluster>-net,gw-priority=1 --network <realm>-mesh`), and Docker joins both when the container starts.

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

The DNS container keeps its address for as long as the realm's mesh exists (see [below](#after-a-host-reboot-or-a-docker-daemon-restart)). `sind get mesh` shows it, so DNS servers outside sind, such as the CoreDNS of a kind cluster on the same host, can forward the `<realm>.sind` zone to it.

Within a cluster, short names work via the search domain: a node in the `dev` cluster can reach `controller` without the full `controller.dev.sind.sind`.

DNS records use each node's **cluster network IP** (not mesh network IP), so the SSH relay and the host reach the nodes through the cluster's network.

sind writes the records when it creates or deletes nodes, and again when `sind power on`, `reboot` or `cycle` start nodes: Docker can give a node another address each time it starts. CoreDNS reloads its configuration in place on `SIGUSR1`, without dropping queries.

The DNS container is lightweight — no systemd or sshd.

### After a host reboot or a Docker daemon restart

sind sets no restart policy, so the mesh containers and the nodes stay stopped after a host reboot or a Docker daemon restart. `sind get cluster` then shows `dns` and `ssh` with ✗. `sind power on` starts them again: first the realm's DNS container, then the SSH relay, then the nodes, whose DNS records it points at their new addresses. It also applies host DNS again. `sind create cluster` and `sind create worker` start a stopped mesh too, and so do `sind delete cluster` and `sind delete worker` when they remove nodes from a mesh that other clusters keep, so that the nodes leave the relay's `known_hosts` as well as the DNS records.

```bash
sind power on controller,worker-[0-1]
```

Docker fixes the DNS server of the nodes and the relay (`--dns`) when it creates them, and does not keep the address of a stopped container. sind therefore pins the DNS container's address. The mesh network gets a subnet of its own, which sind lets Docker choose from its default address pools: Docker hands out the lower half to the nodes and the relay (`--ip-range`), and the DNS container gets a fixed address in the upper half (`--ip`), the last one before the broadcast address, such as `172.18.255.254` in `172.18.0.0/16`. It gets that address back in whatever order the containers start. The mesh network records it in its label `sind.dns.ip`.

A mesh without a pinned address works as before: one created by an earlier sind version, or on a daemon whose address pools give networks smaller than `/21` (see [Limits](#limits)). Starting its DNS container before anything else on the mesh gets it its old address back. If it gets another one, for example after containers were started by hand in another order, sind prints a warning: the containers created before resolve neither `*.<realm>.sind` names nor external names until they are created again. Delete and create the affected clusters to repair them; the SSH relay is created again with the realm's mesh after its last cluster is deleted, and the new mesh pins the address.

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

## Limits

- **Address pools.** Each cluster network and each realm's mesh take one network from Docker's default address pools. A stock daemon has room for about 30 such networks (`172.17.0.0/16` to `172.31.0.0/16`, and `192.168.0.0/16` in `/20` steps), shared with Compose projects and every other network on the host. When they run out, `sind create cluster` fails with Docker's `all predefined address pools have been fully subnetted` and a pointer to `default-address-pools`. Hosts that run many clusters or realms at once can give Docker more, smaller pools in `/etc/docker/daemon.json` and restart the daemon:

  ```json
  {
    "default-address-pools": [
      {"base": "10.200.0.0/13", "size": 21}
    ]
  }
  ```

  This yields 256 networks of 2,046 addresses each, from `10.200.0.0` to `10.207.255.255`; pick a base that no network of the host or its LAN uses. A pool `size` above 21 gives more networks, but ones too small for a mesh to pin its DNS address (see [above](#after-a-host-reboot-or-a-docker-daemon-restart)) and for as many containers as a bridge holds: 253 with a `size` of 24.
- **Bridge ports.** A Linux bridge has 1,024 ports, one of which the kernel reserves, so a Docker bridge network holds at most 1,023 containers. The mesh holds the DNS container, the SSH relay and every node of every cluster in the realm; a cluster network holds the cluster's nodes and the relay. `sind create cluster` and `sind create worker` count the containers connected to both networks, stopped ones included, before they create any node, and refuse nodes that would not fit:

  ```
  network sind-mesh has 1014 containers and would get 10 more, but a Docker bridge network holds at most 1023 (a Linux bridge has 1,024 ports): the realm's mesh holds the nodes of all its clusters, while another realm (--realm) has a mesh of its own
  ```

  Host limits such as `fs.inotify.max_user_instances` and memory usually bind long before (see [Container exits with code 255]({{< relref "/troubleshooting/container-exit-255" >}})).
- **Clients sharing a Docker daemon.** sind clients that share one daemon, such as several users of a host or CI jobs that share the host's Docker socket, are serialized by the realm lock's part on the daemon, the network `<realm>-lock` (see [Realms]({{< relref "/configuration/realms#advisory-locking" >}})). A lock that a command killed on another host or in another container left behind stays until it is removed with `docker network rm <realm>-lock`, as sind cannot tell whether that command still runs. Go programs that take the lock without their Docker client (`LockOptions.Client`) are not serialized against other clients. A shared realm is not isolated: `sind delete cluster --all` deletes every client's clusters, and each user's exported SSH configuration follows only that user's commands, so independent jobs still use a realm each.

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
