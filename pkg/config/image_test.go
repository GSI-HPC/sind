// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckImage(t *testing.T) {
	digest := "sha256:" + strings.Repeat("0f", 32)
	for _, s := range []string{
		"sind-node", "sind-node:26.05", "ghcr.io/gsi-hpc/sind-node:latest", "localhost:5000/own/node:dev",
		"Registry.Example.org/own__node", "[::1]:5000/node", "node@" + digest, "ghcr.io/gsi-hpc/sind-node:26.05@" + digest,
	} {
		assert.NoError(t, CheckImage(s), s)
	}
	for _, tt := range []struct{ image, want string }{
		{"sind node", "use no spaces, such as ghcr.io/gsi-hpc/sind-node:latest"},
		{"ghcr.io/GSI-HPC/sind-node:26.05", "use lowercase letters, such as ghcr.io/gsi-hpc/sind-node:26.05"},
		{"ghcr.io/GSI/Node:V1", "use lowercase letters, such as ghcr.io/gsi/node:V1"},
		{"Node@" + digest, "use lowercase letters, such as node@" + digest},
		{"node@sha256:abc", "write the digest after @ as sha256: and its 64 hex digits"},
		{"node:", "write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05"},
		{"node:-dev", "write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05"},
		{strings.Repeat("a", 256) + ":1", "use at most 255 characters before the tag; this name has 256"},
		{"-v", "write an image reference such as ghcr.io/gsi-hpc/sind-node:latest"},
		{"own/Node_:x", "write an image reference such as ghcr.io/gsi-hpc/sind-node:latest"},
	} {
		err := CheckImage(tt.image)
		require.Error(t, err, tt.image)
		assert.Equal(t, tt.want, err.Error(), tt.image)
	}
}

func TestValidateAll_Images(t *testing.T) {
	cfg := &Cluster{
		Name:     "dev",
		Defaults: Defaults{Image: "Own/Node"},
		Nodes: []Node{
			{Role: RoleController, Image: "Own/Node"},
			{Role: RoleWorker, Image: "own/node:"},
			{Role: RoleWorker},
		},
	}
	cfg.ApplyDefaults()
	var texts []string
	for _, err := range cfg.ValidateAll() {
		texts = append(texts, err.Error())
	}
	assert.Equal(t, []string{
		"image Own/Node: use lowercase letters, such as own/node",
		"image own/node:: write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05",
	}, texts, "each image once, the defaults' first")
}
