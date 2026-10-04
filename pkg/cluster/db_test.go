// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnableDBNode(t *testing.T) {
	var m mock.Executor
	for range 3 {
		m.AddResult("", "", nil)
	}
	c := docker.NewClient(&m)

	err := enableDBNode(t.Context(), c, "sind-dev-db", "db", "")

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "enable", "--now", "mariadb"}, m.Calls[0].Args)
	// Only the OS user slurm, which slurmdbd runs as, logs in as slurm.
	assert.Equal(t, []string{"exec", "sind-dev-db", "mysql", "-e",
		"CREATE DATABASE IF NOT EXISTS slurm_acct_db; " +
			"CREATE USER IF NOT EXISTS 'slurm'@'localhost' IDENTIFIED VIA unix_socket; " +
			"GRANT ALL ON slurm_acct_db.* TO 'slurm'@'localhost';"}, m.Calls[1].Args)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "enable", "--now", "slurmdbd"}, m.Calls[2].Args)
}

func TestEnableDBNode_StoragePass(t *testing.T) {
	var m mock.Executor
	for range 3 {
		m.AddResult("", "", nil)
	}
	c := docker.NewClient(&m)

	err := enableDBNode(t.Context(), c, "sind-dev-db", "db", "password")

	require.NoError(t, err)
	// The password's hash, never the password, is on the command line.
	assert.Equal(t, []string{"exec", "sind-dev-db", "mysql", "-e",
		"CREATE DATABASE IF NOT EXISTS slurm_acct_db; " +
			"CREATE USER IF NOT EXISTS 'slurm'@'localhost' IDENTIFIED BY PASSWORD '*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19'; " +
			"GRANT ALL ON slurm_acct_db.* TO 'slurm'@'localhost';"}, m.Calls[1].Args)
}

func TestEnableDBNode_Errors(t *testing.T) {
	tests := []struct {
		failAt    int
		wantErr   string
		wantCalls int // a failed systemctl enable adds a journalctl call
	}{
		{0, "enabling mariadb on db: ", 2},
		{1, "initializing accounting database on db: ", 2},
		{2, "enabling slurmdbd on db: ", 4},
	}
	for _, tt := range tests {
		t.Run(tt.wantErr, func(t *testing.T) {
			var m mock.Executor
			for i := 0; i < tt.failAt; i++ {
				m.AddResult("", "", nil)
			}
			m.AddResult("", "", fmt.Errorf("boom"))
			c := docker.NewClient(&m)

			err := enableDBNode(t.Context(), c, "sind-dev-db", "db", "")

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Len(t, m.Calls, tt.wantCalls, "stops at the failing step")
		})
	}
}

func TestNativePasswordHash(t *testing.T) {
	// MariaDB: SELECT PASSWORD('password')
	assert.Equal(t, "*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19", nativePasswordHash("password"))
	assert.Equal(t, "*23775EA4B561FABD936AAEC47409D55A0F6DD72F", nativePasswordHash("a#b c"))
}

func TestSlurmdbdStoragePass(t *testing.T) {
	tests := []struct {
		name    string
		section config.Section
		want    string
	}{
		{"none", config.Section{}, ""},
		{"other keys", config.Section{Content: "ArchiveEvents=yes\nStorageUser=slurm\n"}, ""},
		{"plain", config.Section{Content: "StoragePass=s3cret\n"}, "s3cret"},
		{"case and spaces", config.Section{Content: "  storagepass = s3cret  \n"}, "s3cret"},
		{"no newline", config.Section{Content: "StoragePass=s3cret"}, "s3cret"},
		{"quoted", config.Section{Content: `StoragePass="a b"` + "\n"}, "a b"},
		{"quoted with escaped #", config.Section{Content: `StoragePass="a\#b c"` + "\n"}, "a#b c"},
		// As in Slurm, the comment goes first, which leaves an open quote.
		{"quoted with unescaped #", config.Section{Content: `StoragePass="a#b c"` + "\n"}, `"a`},
		{"several pairs", config.Section{Content: "StorageUser=slurm StoragePass=s3cret DebugLevel=info\n"}, "s3cret"},
		{"after a quoted pair", config.Section{Content: `StorageParameters="x y" StoragePass=s3cret` + "\n"}, "s3cret"},
		{"comment", config.Section{Content: "StoragePass=s3cret # the password\n# StoragePass=old\n"}, "s3cret"},
		{"escaped #", config.Section{Content: `StoragePass=a\#b` + "\n"}, "a#b"},
		{"escaped backslash before #", config.Section{Content: `StoragePass=a\\#b` + "\n"}, `a\`},
		{"trailing backslash", config.Section{Content: `StoragePass=ab\`}, "ab"},
		{"last wins", config.Section{Content: "StoragePass=old\nStoragePass=new\n"}, "new"},
		{"other key ending in it", config.Section{Content: "XStoragePass=x\n"}, ""},
		{"fragments in include order", config.Section{Fragments: map[string]string{
			"b": "StoragePass=second\n",
			"a": "StoragePass=first\n",
			"c": "ArchiveEvents=yes\n",
		}}, "second"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, slurmdbdStoragePass(tt.section))
		})
	}
}

func TestNodeRunConfigs_StoragePass(t *testing.T) {
	cfg := &config.Cluster{
		Name:  "dev",
		Slurm: config.Slurm{Slurmdbd: config.Section{Content: "StoragePass=s3cret\n"}},
		Nodes: []config.Node{{Role: config.RoleController}, {Role: config.RoleDB}, {Role: config.RoleWorker}},
	}

	configs := NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")

	require.Len(t, configs, 3)
	assert.Empty(t, configs[0].StoragePass, "controller")
	assert.Equal(t, "s3cret", configs[1].StoragePass, "db")
	assert.Empty(t, configs[2].StoragePass, "worker")

	// An unmanaged db node runs no MariaDB of sind's.
	cfg.Nodes[1].Managed = new(bool)
	configs = NodeRunConfigs(cfg, mesh.DefaultRealm, "", "", "")
	assert.Empty(t, configs[1].StoragePass)
}
