// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateSlurmConf(t *testing.T) {
	conf := GenerateSlurmConf("dev")

	assert.Contains(t, conf, "ClusterName=dev")
	assert.Contains(t, conf, "SlurmctldHost=controller")
	assert.Contains(t, conf, "ReturnToService=2")
	assert.Contains(t, conf, "include /etc/slurm/sind-nodes.conf")
}

func TestGenerateSlurmConf_DefaultCluster(t *testing.T) {
	conf := GenerateSlurmConf("default")

	assert.Contains(t, conf, "ClusterName=default")
	assert.Contains(t, conf, "SlurmctldHost=controller")
}

func TestGenerateSlurmConf_NoTrailingWhitespace(t *testing.T) {
	conf := GenerateSlurmConf("test")
	for _, line := range strings.Split(conf, "\n") {
		assert.Equal(t, strings.TrimRight(line, " \t"), line,
			"line has trailing whitespace: %q", line)
	}
}
