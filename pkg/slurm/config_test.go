// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestGenerateSlurmConf(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, false)

	assert.Contains(t, conf, "ClusterName=dev")
	assert.Contains(t, conf, "SlurmctldHost=controller")
	assert.Contains(t, conf, "ProctrackType=proctrack/cgroup")
	assert.Contains(t, conf, "TaskPlugin=task/cgroup,task/affinity")
	assert.Contains(t, conf, "MpiDefault=pmix")
	assert.Contains(t, conf, "ReturnToService=2")
	assert.Contains(t, conf, "include /etc/slurm/sind-nodes.conf")
	assert.Contains(t, conf, "PlugStackConfig=/etc/slurm/plugstack.conf")
}

func TestGenerateSlurmConf_DefaultCluster(t *testing.T) {
	conf := GenerateSlurmConf("default", config.Section{}, false)

	assert.Contains(t, conf, "ClusterName=default")
	assert.Contains(t, conf, "SlurmctldHost=controller")
}

func TestGenerateSlurmConf_NoTrailingWhitespace(t *testing.T) {
	conf := GenerateSlurmConf("test", config.Section{}, false)
	for _, line := range strings.Split(conf, "\n") {
		assert.Equal(t, strings.TrimRight(line, " \t"), line,
			"line has trailing whitespace: %q", line)
	}
}

func TestGenerateSlurmConf_MainStringAppend(t *testing.T) {
	main := config.Section{Content: "SchedulerType=sched/backfill\n"}
	conf := GenerateSlurmConf("dev", main, false)

	assert.Contains(t, conf, "SchedulerType=sched/backfill\n")
	// Appended after the base config
	assert.Contains(t, conf, "include /etc/slurm/sind-nodes.conf")
}

func TestGenerateSlurmConf_MainMapIncludes(t *testing.T) {
	main := config.Section{Fragments: map[string]string{
		"resources":  "SelectType=select/cons_tres\n",
		"scheduling": "SchedulerType=sched/backfill\n",
	}}
	conf := GenerateSlurmConf("dev", main, false)

	assert.Contains(t, conf, "include /etc/slurm/slurm.conf.d/resources.conf\n")
	assert.Contains(t, conf, "include /etc/slurm/slurm.conf.d/scheduling.conf\n")
	assert.NotContains(t, conf, "*")
}

func TestGenerateSlurmConf_SingleController(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, false)

	assert.Equal(t, 1, strings.Count(conf, "SlurmctldHost="))
	assert.NotContains(t, conf, "SlurmctldTimeout")
}

func TestGenerateSlurmConf_BackupController(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, true)

	assert.Contains(t, conf, "SlurmctldHost=controller\nSlurmctldHost=controller-backup\n")
	assert.Contains(t, conf, "SlurmctldTimeout=20\n")
	assert.Contains(t, conf, "StateSaveLocation=/var/spool/slurmctld\n")
}

func TestGenerateSlurmConf_BackupControllerTimeoutOverride(t *testing.T) {
	for _, main := range []config.Section{
		{Content: "SlurmctldTimeout=60\n"},
		{Fragments: map[string]string{"ha": "slurmctldtimeout=60\n"}},
	} {
		conf := GenerateSlurmConf("dev", main, true)
		assert.NotContains(t, conf, "SlurmctldTimeout=20")
	}
}

func TestGenerateCgroupConf(t *testing.T) {
	conf := GenerateCgroupConf(config.Section{})

	assert.Contains(t, conf, "CgroupPlugin=autodetect")
}

func TestGenerateCgroupConf_StringAppend(t *testing.T) {
	cgroup := config.Section{Content: "ConstrainCores=yes\n"}
	conf := GenerateCgroupConf(cgroup)

	assert.Contains(t, conf, "CgroupPlugin=autodetect")
	assert.Contains(t, conf, "ConstrainCores=yes\n")
}

func TestGenerateCgroupConf_MapIncludes(t *testing.T) {
	cgroup := config.Section{Fragments: map[string]string{
		"cores": "ConstrainCores=yes\n",
	}}
	conf := GenerateCgroupConf(cgroup)

	assert.Contains(t, conf, "CgroupPlugin=autodetect")
	assert.Contains(t, conf, "include /etc/slurm/cgroup.conf.d/cores.conf\n")
	assert.NotContains(t, conf, "*")
}

func TestGeneratePlugstackConf(t *testing.T) {
	conf := GeneratePlugstackConf(config.Section{})

	assert.Contains(t, conf, "include /etc/slurm/plugstack.conf.d/*")
}

func TestGeneratePlugstackConf_StringAppend(t *testing.T) {
	ps := config.Section{Content: "optional /usr/lib/slurm/spank_pbs.so\n"}
	conf := GeneratePlugstackConf(ps)

	assert.Contains(t, conf, "include /etc/slurm/plugstack.conf.d/*")
	assert.Contains(t, conf, "optional /usr/lib/slurm/spank_pbs.so\n")
}

func TestGenerateSectionConf(t *testing.T) {
	t.Run("string form", func(t *testing.T) {
		s := config.Section{Content: "Name=gpu Type=tesla\n"}
		conf := GenerateSectionConf("gres", s)

		assert.Equal(t, "Name=gpu Type=tesla\n", conf)
	})

	t.Run("map form", func(t *testing.T) {
		s := config.Section{Fragments: map[string]string{
			"gpu": "Name=gpu Type=tesla\n",
		}}
		conf := GenerateSectionConf("gres", s)

		assert.Equal(t, "include /etc/slurm/gres.conf.d/gpu.conf\n", conf)
	})
}
