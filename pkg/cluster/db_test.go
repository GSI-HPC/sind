// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnableDBNode(t *testing.T) {
	var m mock.Executor
	for range 3 {
		m.AddResult("", "", nil)
	}
	c := docker.NewClient(&m)

	err := enableDBNode(t.Context(), c, "sind-dev-db", "db")

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "enable", "--now", "mariadb"}, m.Calls[0].Args)
	assert.Equal(t, []string{"exec", "sind-dev-db", "mysql", "-e",
		"CREATE DATABASE IF NOT EXISTS slurm_acct_db; " +
			"CREATE USER IF NOT EXISTS 'slurm'@'localhost'; " +
			"GRANT ALL ON slurm_acct_db.* TO 'slurm'@'localhost';"}, m.Calls[1].Args)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "enable", "--now", "slurmdbd"}, m.Calls[2].Args)
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

			err := enableDBNode(t.Context(), c, "sind-dev-db", "db")

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Len(t, m.Calls, tt.wantCalls, "stops at the failing step")
		})
	}
}
