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
# tagged latest, the image development builds of sind use by default.
# sha256 is the checksum of
# https://download.schedmd.com/slurm/slurm-<version>.tar.bz2. The other
# components (UCX, PMIx, PRRTE, Open MPI, libjwt) are pinned only in the
# Dockerfile, by the ARG defaults and checksums of their builder stages.
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

# sind release (vX.Y.Z) to tag the images for instead. The image workflow
# sets it when it builds a release tag: each target is then tagged
# vX.Y.Z-<YY>.<MM>, and the first one also vX.Y.Z, the image the release's
# binaries use by default (.goreleaser.yaml).
variable "SIND_RELEASE" {
  default = ""
  validation {
    condition     = SIND_RELEASE == "" || can(regex("^v[0-9]+\\.[0-9]+\\.[0-9]+", SIND_RELEASE))
    error_message = "SIND_RELEASE must be empty or a sind release tag such as v1.2.3."
  }
}

# Build the runtime stage without the cache, so its packages are current;
# the compiled components still come from the cache. The image workflow's
# weekly rebuild sets it.
variable "REFRESH_PACKAGES" {
  default = false
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
  # A variable, not --set: buildx up to v0.37 ignores --set *.no-cache-filter.
  no-cache-filter = REFRESH_PACKAGES ? ["runtime"] : []
  args = {
    SLURM_VERSION = r.version
    SLURM_SHA256  = r.sha256
  }
  tags = (SIND_RELEASE == ""
    ? concat(
      [
        "${REGISTRY}/${IMAGE_NAME}:${r.version}",
        "${REGISTRY}/${IMAGE_NAME}:${release_line(r.version)}",
      ],
      r.version == SLURM_RELEASES[0].version ? ["${REGISTRY}/${IMAGE_NAME}:latest"] : [],
    )
    : concat(
      ["${REGISTRY}/${IMAGE_NAME}:${SIND_RELEASE}-${release_line(r.version)}"],
      r.version == SLURM_RELEASES[0].version ? ["${REGISTRY}/${IMAGE_NAME}:${SIND_RELEASE}"] : [],
    )
  )
}
