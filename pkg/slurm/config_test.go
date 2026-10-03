// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestGenerateSlurmConf(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, ConfOptions{})

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
	conf := GenerateSlurmConf("default", config.Section{}, ConfOptions{})

	assert.Contains(t, conf, "ClusterName=default")
	assert.Contains(t, conf, "SlurmctldHost=controller")
}

func TestGenerateSlurmConf_NoTrailingWhitespace(t *testing.T) {
	conf := GenerateSlurmConf("test", config.Section{}, ConfOptions{})
	for _, line := range strings.Split(conf, "\n") {
		assert.Equal(t, strings.TrimRight(line, " \t"), line,
			"line has trailing whitespace: %q", line)
	}
}

func TestGenerateSlurmConf_MainStringAppend(t *testing.T) {
	main := config.Section{Content: "SchedulerType=sched/backfill\n"}
	conf := GenerateSlurmConf("dev", main, ConfOptions{})

	assert.Contains(t, conf, "SchedulerType=sched/backfill\n")
	// Appended after the base config
	assert.Contains(t, conf, "include /etc/slurm/sind-nodes.conf")
}

func TestGenerateSlurmConf_MainMapIncludes(t *testing.T) {
	main := config.Section{Fragments: map[string]string{
		"resources":  "SelectType=select/cons_tres\n",
		"scheduling": "SchedulerType=sched/backfill\n",
	}}
	conf := GenerateSlurmConf("dev", main, ConfOptions{})

	assert.Contains(t, conf, "include /etc/slurm/slurm.conf.d/resources.conf\n")
	assert.Contains(t, conf, "include /etc/slurm/slurm.conf.d/scheduling.conf\n")
	assert.NotContains(t, conf, "*")
}

func TestGenerateSlurmConf_SingleController(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, ConfOptions{})

	assert.Equal(t, 1, strings.Count(conf, "SlurmctldHost="))
	assert.NotContains(t, conf, "SlurmctldTimeout")
}

func TestGenerateSlurmConf_BackupController(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, ConfOptions{BackupController: true})

	assert.Contains(t, conf, "SlurmctldHost=controller\nSlurmctldHost=controller-backup\n")
	assert.Contains(t, conf, "SlurmctldTimeout=20\n")
	assert.Contains(t, conf, "StateSaveLocation=/var/spool/slurmctld\n")
}

func TestGenerateSlurmConf_BackupControllerTimeoutOverride(t *testing.T) {
	for _, main := range []config.Section{
		{Content: "SlurmctldTimeout=60\n"},
		{Fragments: map[string]string{"ha": "slurmctldtimeout=60\n"}},
	} {
		conf := GenerateSlurmConf("dev", main, ConfOptions{BackupController: true})
		assert.NotContains(t, conf, "SlurmctldTimeout=20")
	}
}

func TestGenerateSlurmConf_Accounting(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, ConfOptions{Accounting: true})

	assert.Contains(t, conf, "\nAccountingStorageType=accounting_storage/slurmdbd\n"+
		"AccountingStorageHost=db\n"+
		"JobAcctGatherType=jobacct_gather/cgroup\n")
	// The defaults come before the include so slurm.main can still add to them.
	assert.Less(t, strings.Index(conf, "AccountingStorageType"), strings.Index(conf, "include "))
}

func TestGenerateSlurmConf_Identity(t *testing.T) {
	local := GenerateSlurmConf("dev", config.Section{}, ConfOptions{Identity: config.IdentityLocal})
	for _, key := range []string{"LaunchParameters", "AuthType", "CredType", "AuthInfo"} {
		assert.NotContains(t, local, key)
	}
	assert.Equal(t, local, GenerateSlurmConf("dev", config.Section{}, ConfOptions{}))

	nss := GenerateSlurmConf("dev", config.Section{}, ConfOptions{Identity: config.IdentityNSSSlurm})
	assert.Contains(t, nss, "\nLaunchParameters=enable_nss_slurm\n")
	assert.NotContains(t, nss, "AuthType")

	// The identity settings come before the accounting ones and the
	// include, so slurm.main can still add to them.
	clientIDs := GenerateSlurmConf("dev", config.Section{}, ConfOptions{Identity: config.IdentityClientIDs, Accounting: true})
	assert.Contains(t, clientIDs, "\n\nAuthType=auth/slurm\n"+
		"CredType=cred/slurm\n"+
		"AuthInfo=use_client_ids\n"+
		"LaunchParameters=enable_nss_slurm\n"+
		"\nAccountingStorageType=accounting_storage/slurmdbd\n")
	assert.Less(t, strings.Index(clientIDs, "AuthType"), strings.Index(clientIDs, "include "))
}

func TestGenerateSlurmConf_IdentityOverrides(t *testing.T) {
	main := config.Section{Content: "LaunchParameters=enable_nss_slurm,use_interactive_step\nAuthInfo=use_client_ids,cred_expire=30\n"}
	conf := GenerateSlurmConf("dev", main, ConfOptions{Identity: config.IdentityClientIDs})

	assert.Contains(t, conf, "AuthType=auth/slurm\nCredType=cred/slurm\n\n")
	assert.Equal(t, 1, strings.Count(conf, "LaunchParameters="))
	assert.Equal(t, 1, strings.Count(conf, "AuthInfo="))
}

func TestGenerateSlurmConf_NoAccounting(t *testing.T) {
	conf := GenerateSlurmConf("dev", config.Section{}, ConfOptions{})

	assert.NotContains(t, conf, "AccountingStorage")
	assert.NotContains(t, conf, "JobAcctGather")
}

func TestGenerateSlurmConf_AccountingOverrides(t *testing.T) {
	for _, main := range []config.Section{
		{Content: "JobAcctGatherType=jobacct_gather/linux\n"},
		{Fragments: map[string]string{"acct": "jobacctgathertype=jobacct_gather/linux\n"}},
	} {
		conf := GenerateSlurmConf("dev", main, ConfOptions{Accounting: true})
		assert.Contains(t, conf, "AccountingStorageType=accounting_storage/slurmdbd\n")
		assert.Contains(t, conf, "AccountingStorageHost=db\n")
		assert.NotContains(t, conf, "JobAcctGatherType=jobacct_gather/cgroup")
	}
}

func TestGenerateSlurmdbdConf(t *testing.T) {
	conf := GenerateSlurmdbdConf(config.Section{}, config.IdentityLocal)

	assert.Equal(t, "# Generated by sind\n"+
		"AuthType=auth/munge\n"+
		"DbdHost=db\n"+
		"SlurmUser=slurm\n"+
		"LogFile=/var/log/slurm/slurmdbd.log\n"+
		"PidFile=/run/slurmdbd/slurmdbd.pid\n"+
		"\n"+
		"StorageType=accounting_storage/mysql\n"+
		"StorageHost=localhost\n"+
		"StorageLoc=slurm_acct_db\n"+
		"StorageUser=slurm\n", conf)
	assert.NotContains(t, conf, "StoragePass")
}

func TestGenerateSlurmdbdConf_ClientIDs(t *testing.T) {
	conf := GenerateSlurmdbdConf(config.Section{}, config.IdentityClientIDs)

	assert.True(t, strings.HasPrefix(conf, "# Generated by sind\n"+
		"AuthType=auth/slurm\n"+
		"AuthInfo=use_client_ids\n"+
		"DbdHost=db\n"))
	assert.NotContains(t, conf, "munge")
	// nssSlurm keeps munge.
	assert.Contains(t, GenerateSlurmdbdConf(config.Section{}, config.IdentityNSSSlurm), "AuthType=auth/munge\n")
}

func TestGenerateSlurmdbdConf_StringAppend(t *testing.T) {
	conf := GenerateSlurmdbdConf(config.Section{Content: "ArchiveEvents=yes\n"}, config.IdentityLocal)

	assert.True(t, strings.HasSuffix(conf, "StorageUser=slurm\nArchiveEvents=yes\n"))
}

func TestGenerateSlurmdbdConf_MapIncludes(t *testing.T) {
	conf := GenerateSlurmdbdConf(config.Section{Fragments: map[string]string{
		"purge":   "PurgeJobAfter=1month\n",
		"archive": "ArchiveEvents=yes\n",
	}}, config.IdentityLocal)

	assert.True(t, strings.HasSuffix(conf, "StorageUser=slurm\n"+
		"include /etc/slurm/slurmdbd.conf.d/archive.conf\n"+
		"include /etc/slurm/slurmdbd.conf.d/purge.conf\n"))
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
