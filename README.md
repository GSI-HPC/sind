<p align="center">
  <img src="docs/static/images/sind-icon-tagline.svg" alt="sind — Slurm in Docker" width="200" />
</p>

<p align="center">
  <strong>Create and manage containerized <a href="https://slurm.schedmd.com/">Slurm</a> clusters for development, testing, and CI/CD workflows.</strong>
</p>

<p align="center">
  <a href="https://gsi-hpc.github.io/sind/getting-started/installation/"><strong>🚀 Getting Started</strong></a>&nbsp;&nbsp;&nbsp;&nbsp;<a href="https://gsi-hpc.github.io/sind/"><strong>📖 Documentation</strong></a>
</p>

---

Inspired by [kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker), **sind** offers a familiar CLI experience for quickly spinning up and tearing down Slurm clusters.

## Features

- **Multi-node, multi-cluster & multi-realm** — run controller, submitter, and worker nodes side by side, or spin up multiple clusters across isolated realms with shared networking
- **System containers** — full systemd-based nodes that emulate bare metal, compatible with Ansible, Chef, and other config management tools
- **Designed for CI/CD** — runs rootless on standard GitHub Actions runners; [sind-action](https://github.com/GSI-HPC/sind-action) sets up clusters in a single step
- **Worker lifecycle** — dynamically add and remove worker nodes from running clusters
- **Power cycle simulation** — shutdown, reboot, freeze, and power-cycle nodes to simulate real-world failure scenarios
- **Minimal dependencies** — just Docker and a sind container image; usable as both a CLI tool and a Go library
- **AI-ready via MCP** — built-in [MCP](https://modelcontextprotocol.io/) server lets AI assistants manage your Slurm clusters

## AI disclosure

Parts of this codebase were developed with the assistance of AI tools. All contributions are reviewed by humans.

## License

sind is licensed under the [GNU Lesser General Public License v3.0](LICENSE).

Copyright © GSI Helmholtzzentrum für Schwerionenforschung GmbH
