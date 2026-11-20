// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"regexp"
	"runtime/debug"
)

// nodeImageRepository is the repository of the official sind-node images.
const nodeImageRepository = "ghcr.io/gsi-hpc/sind-node"

// modulePath is the path of sind's module.
const modulePath = "github.com/GSI-HPC/sind"

// defaultImage is the default node image of a release binary, which
// .goreleaser.yaml sets with -ldflags "-X"; empty in other builds.
var defaultImage string

// readBuildInfo is debug.ReadBuildInfo; the tests replace it.
var readBuildInfo = debug.ReadBuildInfo

// releaseVersion matches the version of a sind release, vX.Y.Z, and no
// pseudo-version, pre-release or build of a modified tree.
var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// DefaultNodeImage returns the node image of nodes that set none: the
// image that the image workflow publishes for a sind release,
// nodeImageRepository:vX.Y.Z, so that a sind version keeps its node image
// and its Slurm version. A release binary names its own release; a
// program built from sind's module at a release, with go install or as a
// library caller that requires that version, names that release. Other
// builds, such as one of a commit between releases or of a module that a
// replace directive takes from elsewhere, get latest, the newest Slurm
// release line.
func DefaultNodeImage() string {
	if defaultImage != "" {
		return defaultImage
	}
	if v := moduleVersion(); v != "" {
		return nodeImageRepository + ":" + v
	}
	return nodeImageRepository + ":latest"
}

// moduleVersion returns the release version of sind's module in the
// program's build info, as the main module or as a dependency, "" when it
// has none.
func moduleVersion() string {
	info, ok := readBuildInfo()
	if !ok {
		return ""
	}
	mods := append([]*debug.Module{&info.Main}, info.Deps...)
	for _, m := range mods {
		if m.Path != modulePath || m.Replace != nil {
			continue
		}
		if releaseVersion.MatchString(m.Version) {
			return m.Version
		}
	}
	return ""
}
