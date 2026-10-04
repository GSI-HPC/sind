---
weight: 358
title: "REST API"
icon: "api"
description: "Run slurmrestd on an api node and call Slurm's REST API with JSON Web Tokens"
toc: true
---

An [`api` node]({{< relref "/configuration/node-definitions#api-node" >}}) runs slurmrestd, Slurm's REST API daemon, so that tooling can drive the cluster over HTTP: a portal, a workflow engine, or a controller under test that submits jobs and reads their accounting.

## Create a cluster with an api node

```yaml
kind: Cluster
nodes: [controller, db, api, worker: 2]
```

```bash
sind create cluster --config cluster.yaml
```

slurmrestd comes with the Slurm 26.05 images, such as the default image and the `26.05` tag. The 25.11 images have none: `sind create cluster` then fails with the image's name. Without a db node, the API serves only the `/slurm/` endpoints; with a managed one, also the `/slurmdb/` endpoints for accounting.

sind starts slurmrestd on the api node and sets up JWT authentication for it: a key, `/etc/slurm/jwt_hs256.key`, readable only by `slurm`, and `AuthAltTypes=auth/jwt` with `AuthAltParameters=jwt_key=/etc/slurm/jwt_hs256.key` in `slurm.conf` and `slurmdbd.conf`. `sind get cluster` lists `slurmrestd` on the api node, and `sind logs api slurmrestd` shows its log.

## Reach the API

slurmrestd listens on port 6820 of the api node:

```text
http://api.<cluster>.<realm>.sind:6820
```

The name resolves on the host when sind could point systemd-resolved at the realm's DNS (see [Host DNS resolution]({{< relref "/architecture/networking#host-dns-resolution" >}})), and in containers that join the cluster network, `<realm>-<cluster>-net`. Otherwise, use the node's address:

```bash
API=http://$(sind get node api -o json | jq -r .ip):6820
```

sind publishes no host port: on Linux the host reaches the containers' addresses directly. With a Docker daemon on another machine, run the client in a container on the cluster network.

## Get a token and call the API

Every request carries a user name and a JSON Web Token in the headers `X-SLURM-USER-NAME` and `X-SLURM-USER-TOKEN`. Root can get a token for any user from slurmctld:

```bash
TOKEN=$(sind exec -- scontrol token username=root lifespan=3600 | cut -d= -f2)
API=http://api.default.sind.sind:6820

curl -s -H "X-SLURM-USER-NAME: root" -H "X-SLURM-USER-TOKEN: $TOKEN" \
  $API/slurm/v0.0.45/ping/
```

`scontrol token` prints `SLURM_JWT=<token>`; `lifespan` is in seconds, 1800 by default. Submit a job:

```bash
curl -s -X POST -H "X-SLURM-USER-NAME: root" -H "X-SLURM-USER-TOKEN: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"job": {"script": "#!/bin/bash\nhostname", "current_working_directory": "/data", "environment": ["PATH=/usr/bin:/bin"]}}' \
  $API/slurm/v0.0.45/job/submit
```

`/openapi/v3` describes every endpoint of the cluster's Slurm version. `v0.0.45` is the newest version of the data format in Slurm 26.05; slurmrestd serves the older ones too.

## Tokens for users

slurmrestd holds no key and looks nobody up: it passes the token on to slurmctld for `/slurm/` endpoints and to slurmdbd for `/slurmdb/` endpoints. The daemon checks the token's signature with the key, and then finds out who the user is:

- A token from `scontrol token username=alice` carries only her name. The daemon looks it up in its own node's `/etc/passwd`: the controller's for `/slurm/`, the db node's for `/slurmdb/`.
- A token can also carry the user's identity: uid, gid, home directory, shell and groups. slurmctld and slurmdbd take it from the token instead of looking the user up when `AuthAltParameters` lists `use_jwt_client_ids`, which sind sets with identity `clientIds` (see below). `scontrol token` never adds the identity: sign such a token yourself.

With [users]({{< relref "/guides/users" >}}) and the default identity mode, `local`, every node has the users, so every user's token works:

```yaml
kind: Cluster
nodes: [controller, db, api, submitter, worker: 2]
users:
  - name: alice
    accounts: [physics]
accounts: [physics]
```

```bash
TOKEN=$(sind exec -- scontrol token username=alice | cut -d= -f2)
curl -s -H "X-SLURM-USER-NAME: alice" -H "X-SLURM-USER-TOKEN: $TOKEN" \
  http://api.default.sind.sind:6820/slurmdb/v0.0.45/jobs/
```

## Identity modes

The [identity mode]({{< relref "/configuration/cluster-config#identity-section" >}}) decides which nodes have the users, and with that which tokens get through. Root has an account on every node, so root's tokens work in every mode.

| Identity mode | alice's token from `scontrol token` | alice's token with identity claims |
|---------------|-------------------------------------|-------------------------------------|
| `local`, `nssSlurm` | `/slurm/` and `/slurmdb/` | `/slurm/` and `/slurmdb/` (the claims are ignored, the lookup decides) |
| `clientIds` | rejected | `/slurm/` and `/slurmdb/` |
| `clientIds` with `controllerUsers: true` | `/slurm/` only: the db node has no users | `/slurm/` and `/slurmdb/` |

With `clientIds`, only the login node has the users, and slurmctld and slurmdbd learn them from sackd's tokens. The REST API counterpart is a token that carries the identity, which sind accepts there with `AuthAltParameters=jwt_key=/etc/slurm/jwt_hs256.key,use_jwt_client_ids`.

## Sign your own tokens

A token is an HS256 JSON Web Token signed with the cluster's key, which `sind get auth-key` exports, base64-encoded:

```bash
sind get auth-key --type jwt
```

With the key, tooling can sign tokens without asking slurmctld: for any user, with any lifespan, and with the user's identity. Any JWT library does, for example PyJWT:

```python
import base64, subprocess, time
import jwt  # pip install pyjwt

key = base64.b64decode(subprocess.check_output(
    ["sind", "get", "auth-key", "--type", "jwt"]))
now = int(time.time())
token = jwt.encode({
    "sun": "alice", "iat": now, "exp": now + 3600,
    # The identity, for identity clientIds:
    "uid": 2001, "gid": 2001,
    "id": {"name": "alice", "gecos": "alice", "dir": "/home/alice",
           "shell": "/bin/bash", "gids": [2001]},
}, key, algorithm="HS256")
```

- `sun` is the user name, and `exp` when the token expires.
- The identity needs `uid`, `gid` and `id` with `name`, which must equal `sun`, a non-empty `gecos`, `dir`, `shell`, and either `gids` (a list of group IDs) or `groups` (group names mapped to IDs). An incomplete identity is ignored.

Whoever holds the key can sign a token for any user, root included, so treat it like root's password. The [MCP server]({{< relref "/guides/mcp" >}}) leaves `sind get auth-key` out of its tools. To test your own token setup instead, such as a JWKS file of an identity provider, set `AuthAltParameters` in the [`main` section]({{< relref "/configuration/cluster-config#slurm-section" >}}): sind then writes no value of its own, and its key goes unused.
