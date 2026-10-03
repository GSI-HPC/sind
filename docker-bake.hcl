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
      version = "26.05.4"
      sha256  = "035f4b193d4de979ba5381beca206a50b6b886b2793b06f68a1ce7e67022b06a"
    },
    {
      version = "25.11.8"
      sha256  = "34ace13f81011add6094569d13bfc4006ad8868201c2236e2905443c7e526393"
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

variable "LIBJWT_VERSION" {
  default = "1.18.4"
}

# Release line of a Slurm version, e.g. "25.11" for "25.11.8".
function "release_line" {
  params = [version]
  result = regex("^[0-9]+\\.[0-9]+", version)
}

group "default" {
  targets = ["slurm"]
}

# Official images are published for both platforms; CI builds each one on a
# native runner. `make image` builds for the host platform only
# (--set '*.platform=local').
target "slurm" {
  name       = "slurm-${replace(release_line(r.version), ".", "-")}"
  matrix     = { r = SLURM_RELEASES }
  context    = "."
  dockerfile = "Dockerfile"
  network    = "host"
  platforms  = ["linux/amd64", "linux/arm64"]
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
