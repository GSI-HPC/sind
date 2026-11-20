// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// withBuildInfo makes readBuildInfo return info, ok and defaultImage
// ldflag for the test.
func withBuildInfo(t *testing.T, ldflag string, info *debug.BuildInfo, ok bool) {
	t.Helper()
	saved, savedImage := readBuildInfo, defaultImage
	t.Cleanup(func() { readBuildInfo, defaultImage = saved, savedImage })
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	defaultImage = ldflag
}

func TestDefaultNodeImage(t *testing.T) {
	sind := func(v string) debug.Module { return debug.Module{Path: "github.com/GSI-HPC/sind", Version: v} }
	dep := func(m debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "example.com/tool", Version: "(devel)"}, Deps: []*debug.Module{
			{Path: "github.com/GSI-HPC/go-clikit", Version: "v0.11.0"}, &m,
		}}
	}
	replaced := sind("v0.17.0")
	replaced.Replace = &debug.Module{Path: "../sind"}
	tests := []struct {
		name   string
		ldflag string
		info   *debug.BuildInfo
		ok     bool
		want   string
	}{
		{"release binary", "ghcr.io/gsi-hpc/sind-node:v0.17.0", &debug.BuildInfo{Main: sind("v0.17.0")}, true, "ghcr.io/gsi-hpc/sind-node:v0.17.0"},
		{"go install at a release", "", &debug.BuildInfo{Main: sind("v0.17.0")}, true, "ghcr.io/gsi-hpc/sind-node:v0.17.0"},
		{"library caller of a release", "", dep(sind("v0.16.1")), true, "ghcr.io/gsi-hpc/sind-node:v0.16.1"},
		{"between releases", "", &debug.BuildInfo{Main: sind("v0.17.1-0.20261119120000-0123456789ab")}, true, "ghcr.io/gsi-hpc/sind-node:latest"},
		{"modified tree", "", &debug.BuildInfo{Main: sind("v0.17.0+dirty")}, true, "ghcr.io/gsi-hpc/sind-node:latest"},
		{"pre-release", "", dep(sind("v0.18.0-rc.1")), true, "ghcr.io/gsi-hpc/sind-node:latest"},
		{"checkout", "", &debug.BuildInfo{Main: sind("(devel)")}, true, "ghcr.io/gsi-hpc/sind-node:latest"},
		{"replaced module", "", dep(replaced), true, "ghcr.io/gsi-hpc/sind-node:latest"},
		{"no build info", "", nil, false, "ghcr.io/gsi-hpc/sind-node:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withBuildInfo(t, tt.ldflag, tt.info, tt.ok)
			assert.Equal(t, tt.want, DefaultNodeImage())
		})
	}
}

// TestApplyDefaults_NodeImageOfTheRelease checks that a node without an
// image gets the node image of the sind release the program is built
// with.
func TestApplyDefaults_NodeImageOfTheRelease(t *testing.T) {
	withBuildInfo(t, "", &debug.BuildInfo{Main: debug.Module{Path: "example.com/tool"}, Deps: []*debug.Module{{Path: "github.com/GSI-HPC/sind", Version: "v0.17.0"}}}, true)
	cfg := &Cluster{}
	cfg.ApplyDefaults()
	assert.NotEmpty(t, cfg.Nodes)
	for _, n := range cfg.Nodes {
		assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:v0.17.0", n.Image)
	}
}
