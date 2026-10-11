---
weight: 78
title: "08 · Slurm in your pipeline"
description: "sind-action, realms for parallel jobs, fast failure tests"
---

{{< video "course-08-slurm-in-ci" >}}

Software that talks to Slurm, such as a workflow engine, a web portal or a client library, needs a real Slurm in every CI run. This episode installs sind in a pipeline, gives each parallel job its own realm, parses sind's JSON and exit status, shortens failure tests, and covers what is special on a CI runner.

## In this episode

- [CI/CD]({{< relref "/guides/ci-cd" >}}): sind-action on GitHub, and the install steps for any other CI system
- [Realms]({{< relref "/configuration/realms" >}}): one realm per job, and why a shared realm is not isolated
- [Diagnostics]({{< relref "/usage/diagnostics#json-output" >}}): JSON output of `sind version`, `sind doctor` and every `sind get`
- [Exit Status]({{< relref "/usage/exit-status" >}}): the statuses a script can branch on
- [CI/CD › Node failure tests]({{< relref "/guides/ci-cd#node-failure-tests" >}}): a shorter `SlurmdTimeout`, as in [Failure drills]({{< relref "07-failure-drills" >}})
- [CI/CD › Data directory]({{< relref "/guides/ci-cd#data-directory" >}}) and [Untrusted changes]({{< relref "/guides/ci-cd#untrusted-changes" >}}): the workspace at `/data`, and configs from pull requests

## Commands

Every command, output and config snippet shown in the video, in order.

Install sind in a CI job without sind-action ([CI/CD]({{< relref "/guides/ci-cd#other-ci-systems" >}})):

```bash
curl -fsSL -o sind-linux-amd64 \
  "https://github.com/GSI-HPC/sind/releases/latest/download/sind-linux-amd64"
gh attestation verify sind-linux-amd64 --repo GSI-HPC/sind \
  --signer-workflow GSI-HPC/sind/.github/workflows/release.yml
install -m 755 sind-linux-amd64 ./sind
./sind doctor
./sind create cluster --config cluster.yml
```

Two realms, each with a cluster named `app` ([Realms]({{< relref "/configuration/realms#example" >}})):

```bash
sind --realm dev create cluster app
sind --realm staging create cluster app
sind --realm dev delete cluster app
sind --realm staging delete cluster app
```

Machine-readable output for scripts, from `sind version` ([Diagnostics › Version]({{< relref "/usage/diagnostics#version" >}})):

```bash
sind version --json
```

```json
{"version":"0.10.0","commit":"0a57d68","goVersion":"go1.26.8","platform":"linux/amd64"}
```

and from `sind doctor` ([Diagnostics › Machine-readable output]({{< relref "/usage/diagnostics#machine-readable-output" >}})):

```bash
sind doctor -o json
```

```json
[
  {
    "name": "Docker Engine",
    "status": "ok",
    "detail": "28.1.1 (>= 28.0)"
  },
  {
    "name": "Docker daemon",
    "status": "ok",
    "detail": "rootful, no userns-remap"
  },
  {
    "name": "Docker host",
    "status": "ok",
    "detail": "this machine (unix:///var/run/docker.sock)"
  },
  {
    "name": "cgroupv2",
    "status": "failed",
    "detail": "nsdelegate not found",
    "remediation": "Enable nsdelegate on the Docker host temporarily:\n\nsudo mount -o remount,nsdelegate /sys/fs/cgroup\n..."
  },
  {
    "name": "inotify",
    "status": "ok",
    "detail": "max_user_instances 8192 (>= 1024)"
  }
]
```

A usage error exits with status `2` ([Exit Status]({{< relref "/usage/exit-status#usage-errors" >}})):

```console
$ sind get cluster dev extra
12:00:00.000 ERRO accepts at most 1 arg(s), received 2
$ echo $?
2
```

A shorter `SlurmdTimeout` for node failure tests, in the cluster config ([CI/CD]({{< relref "/guides/ci-cd#node-failure-tests" >}})):

```yaml
slurm:
  main: |
    SlurmdTimeout=30
```

On a runner, `--data volume` keeps the workspace out of the cluster ([Data directory]({{< relref "/guides/ci-cd#data-directory" >}})), and a later sind command of the job removes the realm lock that a job timeout left behind ([Realms › Advisory locking]({{< relref "/configuration/realms#advisory-locking" >}})).

## Go deeper

- [sind-action documentation](https://github.com/GSI-HPC/sind-action#readme): inputs, outputs, cluster definitions and examples, including parallel jobs in realms
- [Realms › Advisory locking]({{< relref "/configuration/realms#advisory-locking" >}}): how the realm lock serializes jobs that share a Docker daemon
- [Power Control › Node failure detection]({{< relref "/usage/power-control#node-failure-detection" >}}): how slurmctld notices a lost worker, and why a short timeout has a cost
- [Networking › Limits]({{< relref "/architecture/networking#limits" >}}): address pools for runners that host many realms at once
- Guide clips: [CI/CD]({{< relref "/guides/ci-cd" >}}), [Realms]({{< relref "/configuration/realms" >}}), [Diagnostics]({{< relref "/usage/diagnostics" >}}) and [Power Control]({{< relref "/usage/power-control" >}})
