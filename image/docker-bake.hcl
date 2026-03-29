# SPDX-License-Identifier: LGPL-3.0-or-later

variable "REGISTRY" {
  default = "ghcr.io/gsi-hpc"
}

variable "IMAGE_NAME" {
  default = "sind-node"
}

# Must match the ARG SLURM_VERSION in the Dockerfile. Pinned here because the
# Dockerfile checksum is coupled to this exact version.
variable "SLURM_VERSION" {
  default = "25.11.4"
}

target "default" {
  context    = "."
  dockerfile = "Dockerfile"
  tags = [
    "${REGISTRY}/${IMAGE_NAME}:${SLURM_VERSION}",
  ]
}
