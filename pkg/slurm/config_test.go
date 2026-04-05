// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestGenerateSlurmConf(t *testing.T) {
	conf := GenerateSlurmConf("dev")

	assert.Contains(t, conf, "ClusterName=dev")
	assert.Contains(t, conf, "SlurmctldHost=controller")
	assert.Contains(t, conf, "ProctrackType=proctrack/cgroup")
	assert.Contains(t, conf, "TaskPlugin=task/cgroup,task/affinity")
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

func TestServiceForRole(t *testing.T) {
	svc, ok := ServiceForRole(config.RoleController)
	assert.True(t, ok)
	assert.Equal(t, Slurmctld, svc)

	svc, ok = ServiceForRole(config.RoleWorker)
	assert.True(t, ok)
	assert.Equal(t, Slurmd, svc)

	_, ok = ServiceForRole(config.RoleSubmitter)
	assert.False(t, ok)

	_, ok = ServiceForRole("unknown")
	assert.False(t, ok)
}

func TestGenerateCgroupConf(t *testing.T) {
	conf := GenerateCgroupConf()

	assert.Contains(t, conf, "CgroupPlugin=autodetect")
}
