// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// FuzzParse checks that no cluster configuration makes Parse, or what sind
// calls on its result, panic, and that a configuration that validates
// reads back as the same cluster once it is written as YAML.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"kind: Cluster\n",
		"kind: Cluster\nname: dev\nrealm: test\nmonitoring: true\n",
		"kind: Cluster\nnodes:\n  - controller\n  - worker: 3\n  - role: worker\n    count: 2\n    cpus: 4\n    memory: 2g\n",
		"kind: Cluster\nnodes:\n  - {}\n",
		"kind: Cluster\nnodes:\n  - controller: 2\n  - db\n  - api\n  - submitter\n",
		"kind: Cluster\ndefaults:\n  image: ghcr.io/gsi-hpc/sind-node:latest\n  cpus: 2\n  memory: 1g\n  tmpSize: 512m\n",
		"kind: Cluster\nslurm:\n  main: |\n    SchedulerType=sched/backfill\n  cgroup:\n    a.conf: ConstrainCores=yes\n",
		"kind: Cluster\nstorage:\n  dataStorage:\n    type: hostPath\n    hostPath: /srv/data\n    mountPath: /data\n",
		"kind: Cluster\nusers:\n  - alice\n  - name: bob\n    uid: 2000\n    groups: [hpc]\ngroups:\n  - hpc\n  - name: ops\n    gid: 3000\n",
		"kind: Cluster\nnodes: [controller, db, worker]\naccounts:\n  - physics\n  - name: bio\n    parent: physics\n    users: [alice]\n    limits: {GrpTRES: cpu=4, MaxJobs: 2}\nusers: [alice]\n",
		"kind: Cluster\nidentity: local\n",
		"kind: Cluster\nidentity:\n  mode: nss_slurm\n",
		"kind: Pod\n",
		"kind: Cluster\nnodes: 3\n",
		"kind: Cluster\n? [a, b]\n: c\n",
		"a: &a [*a]\n",
		"kind: Cluster\nnodes:\n  - role: worker\n    capAdd: [SYS_ADMIN]\n    devices: [/dev/fuse]\n    securityOpt: [apparmor=unconfined]\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := Parse(data)
		if err != nil {
			return
		}
		require.NotNil(t, cfg)
		cfg.ApplyDefaults()
		_ = cfg.Warnings()
		if len(cfg.ValidateAll()) > 0 {
			return
		}

		out, err := yaml.Marshal(cfg)
		require.NoError(t, err)
		back, err := Parse(out)
		require.NoError(t, err, "the valid cluster does not read back:\n%s", out)
		back.ApplyDefaults()
		assert.Empty(t, back.ValidateAll(), "the valid cluster reads back invalid:\n%s", out)
		again, err := yaml.Marshal(back)
		require.NoError(t, err)
		assert.Equal(t, string(out), string(again), "the cluster changes when it is written and read")
	})
}

// FuzzLinePairs checks the reader of slurm.conf lines: no line makes it
// panic, a key holds no "=" and no blank around it, LineParameter finds
// the first key, and the comment that StripComment cuts holds no pair.
func FuzzLinePairs(f *testing.F) {
	for _, seed := range []string{
		"",
		"ClusterName=dev",
		"NodeName=worker-0 CPUs=2 RealMemory=1024 State=UNKNOWN",
		`PartitionName=all Nodes="a,b" Default=YES # the default`,
		`Key=a\#b # comment`,
		`Key = "quoted value" Other=1`,
		"Key+=1 Key-=2",
		`Epilog=/bin/true\`,
		"# only a comment",
		"=",
		`"`,
		"include other.conf",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		pairs := LinePairs(line)
		for _, p := range pairs {
			assert.NotContains(t, p[0], "=", "key of %q", line)
			assert.Equal(t, strings.TrimSpace(p[0]), p[0], "key of %q", line)
		}
		if len(pairs) > 0 {
			_, ok := LineParameter(line, pairs[0][0])
			assert.True(t, ok, "LineParameter finds the first key of %q", line)
		}
		code := StripComment(line)
		assert.True(t, strings.HasPrefix(line, code), "StripComment(%q) = %q is no prefix", line, code)
		assert.Equal(t, pairs, LinePairs(code), "the comment of %q changes its pairs", line)
	})
}

// FuzzMemoryMB checks the reader of Docker's size syntax: no value makes
// it panic, and a value it takes is at least MinMemory and passes
// CheckMemory.
func FuzzMemoryMB(f *testing.F) {
	for _, seed := range []string{"", "512m", "2g", "2GB", "2GiB", "1.5g", "2048k", "1073741824", "1e3g", "-1g", "0x10g", "9999999999999999999p", ".5g", "1.g", "６g"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		mb, err := MemoryMB(s)
		if err != nil {
			return
		}
		assert.GreaterOrEqual(t, mb, MinMemory>>20, "MemoryMB(%q)", s)
		assert.NoError(t, CheckMemory("memory", s))
	})
}
