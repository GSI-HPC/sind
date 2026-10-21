---
weight: 550
title: "Troubleshooting"
description: "Common issues and solutions"
---

Solutions for common problems when running sind clusters.

| Symptom | Cause | Where |
|---------|-------|-------|
| Container exits with code 255 while the cluster is created | host limits such as `fs.inotify.max_user_instances`, or memory | [Container exits with code 255]({{< relref "/troubleshooting/container-exit-255" >}}) |
| `the Docker daemon runs containers on cgroup v1`, or `cgroupv2` fails in `sind doctor` | the host does not boot with the unified cgroup hierarchy | [Doctor]({{< relref "/usage/diagnostics#doctor" >}}) |
| `nsdelegate not found` | the cgroup2 mount lacks the `nsdelegate` option | [Doctor]({{< relref "/usage/diagnostics#doctor" >}}) |
| `rootless mode` or `userns-remap` errors | Docker refuses the writable cgroups of the nodes | [Doctor]({{< relref "/usage/diagnostics#doctor" >}}) |
| `permission denied` on the Docker socket, or Docker is not reachable | your user is not in the `docker` group, or the daemon is not running | [Doctor]({{< relref "/usage/diagnostics#doctor" >}}) |
| `all predefined address pools have been fully subnetted` when a cluster is created | Docker's default address pools are used up | [Limits]({{< relref "/architecture/networking#limits" >}}) |
| `waiting for another sind command in realm` that never ends | a killed command left the realm lock behind | [Advisory locking]({{< relref "/configuration/realms#advisory-locking" >}}) |
