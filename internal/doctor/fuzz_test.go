// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// FuzzParseVersion checks the reader of Docker's version: no string makes
// it panic, and CheckDockerVersion refuses what it cannot read.
func FuzzParseVersion(f *testing.F) {
	for _, seed := range []string{"", "28.0.0", "29.3.1-beta.1", "v28.0", "28", "28.", ".5", "-1.2", "99999999999999999999.0", "0x1c.0", " 28.0", "28 .0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		major, _, err := ParseVersion(s)
		if err != nil {
			assert.ErrorContains(t, CheckDockerVersion(s), "unable to parse version")
			return
		}
		assert.Equal(t, major >= MinDockerMajor, CheckDockerVersion(s) == nil, "version %q", s)
	})
}

// FuzzParseCgroupInfo checks the reader of a mount table, which a
// container of any image prints: no table makes it panic, nsdelegate is
// reported only with cgroup v2, and cgroup v2 only at the cgroup root.
func FuzzParseCgroupInfo(f *testing.F) {
	for _, seed := range []string{
		"",
		"cgroup2 /sys/fs/cgroup cgroup2 rw,nosuid,nodev,noexec,relatime,nsdelegate,memory_recursiveprot 0 0\n",
		"cgroup2 /sys/fs/cgroup cgroup2 rw 0 0\n",
		"cgroup2 /sys/fs/cgroup/unified cgroup2 rw,nsdelegate 0 0\ntmpfs /sys/fs/cgroup tmpfs ro 0 0\n",
		"a b cgroup2\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, mounts string) {
		path, v2, nsdelegate := parseCgroupInfo(mounts)
		if nsdelegate {
			assert.True(t, v2)
		}
		if v2 {
			assert.Equal(t, CgroupRoot, path)
		}
	})
}
