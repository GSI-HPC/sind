# SPDX-License-Identifier: LGPL-3.0-or-later

variable "REGISTRY" {
  default = "ghcr.io/gsi-hpc"
}

variable "IMAGE_NAME" {
  default = "sind-node"
}

# Must match the ARG defaults in the Dockerfile. Pinned here because the
# Dockerfile checksums are coupled to these exact versions.
variable "SLURM_VERSION" {
  default = "25.11.4"
}

variable "UCX_VERSION" {
  default = "1.20.0"
}

variable "PMIX_VERSION" {
  default = "6.1.0"
}

variable "PRRTE_VERSION" {
  default = "4.1.0"
}

variable "OMPI_VERSION" {
  default = "5.0.10"
}

target "default" {
  context    = "."
  dockerfile = "Dockerfile"
  network    = "host"
  tags = [
    "${REGISTRY}/${IMAGE_NAME}:${SLURM_VERSION}",
  ]
}
