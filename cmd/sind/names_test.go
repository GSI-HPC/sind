// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInvalidClusterName checks that every way a cluster name enters the
// CLI rejects one that is not a DNS label, before docker is called.
func TestInvalidClusterName(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"create", "cluster", "my_cluster"}, `invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"delete", "cluster", "../x"}, `invalid cluster name "../x": '.' is not a letter, a digit or a hyphen`},
		{[]string{"get", "cluster", "dev-"}, `invalid cluster name "dev-": it ends with a hyphen`},
		{[]string{"get", "nodes", "dev.test"}, `invalid cluster name "dev.test": '.' is not a letter, a digit or a hyphen`},
		{[]string{"get", "munge-key", "a/b"}, `invalid cluster name "a/b": '/' is not a letter, a digit or a hyphen`},
		{[]string{"enter", "my_cluster"}, `invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"create", "worker", "my_cluster"}, `invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"exec", "my_cluster", "--", "hostname"}, `invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"get", "node", "worker-0.my_cluster"}, `invalid node name "worker-0.my_cluster": invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"power", "shutdown", "worker-0.my_cluster"}, `invalid node name "worker-0.my_cluster": invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"delete", "worker", "worker-[0-1].my_cluster"}, `invalid node name "worker-0.my_cluster": invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"ssh", "worker-0.my_cluster"}, `invalid node name "worker-0.my_cluster": invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
		{[]string{"logs", "worker-0.my_cluster"}, `invalid node name "worker-0.my_cluster": invalid cluster name "my_cluster": '_' is not a letter, a digit or a hyphen`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var m mock.Executor
			_, _, err := executeWithMock(&m, tt.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, m.Calls, "docker must not be called")
		})
	}
}

// TestInvalidRealm checks the realm from --realm and SIND_REALM.
func TestInvalidRealm(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		var m mock.Executor
		_, _, err := executeWithMock(&m, "--realm", "../x", "get", "clusters")
		require.EqualError(t, err, `--realm: invalid realm name "../x": '.' is not a letter, a digit or a hyphen`)
		assert.Empty(t, m.Calls)
	})
	t.Run("env", func(t *testing.T) {
		t.Setenv("SIND_REALM", "ci_42")
		var m mock.Executor
		_, _, err := executeWithMock(&m, "delete", "cluster", "dev")
		require.EqualError(t, err, `SIND_REALM: invalid realm name "ci_42": '_' is not a letter, a digit or a hyphen`)
		assert.Empty(t, m.Calls)
	})
	t.Run("env not used", func(t *testing.T) {
		// A command that has no realm does not look at SIND_REALM.
		t.Setenv("SIND_REALM", "ci_42")
		_, _, err := executeCommand("version")
		require.NoError(t, err)
	})
}
