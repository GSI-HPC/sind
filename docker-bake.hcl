# SPDX-License-Identifier: LGPL-3.0-or-later

variable "REGISTRY" {
  default = "ghcr.io/gsi-hpc"
}

variable "IMAGE_NAME" {
  default = "sind-node"
}

# Slurm releases with an official node image: the newest release of each
# supported release line, newest line first. Each entry builds the target
# slurm-<YY>-<MM>, tagged <version> and <YY>.<MM>; the first entry is also
# tagged latest, the image sind uses by default. sha256 is the checksum of
# https://download.schedmd.com/slurm/slurm-<version>.tar.bz2.
variable "SLURM_RELEASES" {
  default = [
    {
      version = "25.11.6"
      sha256  = "6695aee51a36799917a4db4b1d787610af926b27b17c2e4246bf14c0fd029664"
    },
  ]
}

# Must match the ARG defaults in the Dockerfile. Pinned here because the
# Dockerfile checksums are coupled to these exact versions.
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

# Release line of a Slurm version, e.g. "25.11" for "25.11.6".
function "release_line" {
  params = [version]
  result = regex("^[0-9]+\\.[0-9]+", version)
}

group "default" {
  targets = ["slurm"]
}

target "slurm" {
  name       = "slurm-${replace(release_line(r.version), ".", "-")}"
  matrix     = { r = SLURM_RELEASES }
  context    = "."
  dockerfile = "Dockerfile"
  network    = "host"
  args = {
    SLURM_VERSION = r.version
    SLURM_SHA256  = r.sha256
  }
  tags = concat(
    [
      "${REGISTRY}/${IMAGE_NAME}:${r.version}",
      "${REGISTRY}/${IMAGE_NAME}:${release_line(r.version)}",
    ],
    r.version == SLURM_RELEASES[0].version ? ["${REGISTRY}/${IMAGE_NAME}:latest"] : [],
  )
}
