// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"errors"
	"testing"

	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func realmFlag(s string) *string { return &s }

func TestResolveRealm_Precedence(t *testing.T) {
	tests := []struct {
		name   string
		flag   *string
		env    string
		config string
		want   string
	}{
		{"flag over all", realmFlag("from-flag"), "from-env", "from-config", "from-flag"},
		{"env over config", nil, "from-env", "from-config", "from-env"},
		{"config over default", nil, "", "from-config", "from-config"},
		{"env over default", nil, "from-env", "", "from-env"},
		{"default", nil, "", "", mesh.DefaultRealm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SIND_REALM", tt.env)
			got, err := ResolveRealm(tt.flag, tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveRealm_InvalidFlag(t *testing.T) {
	t.Setenv("SIND_REALM", "")

	_, err := ResolveRealm(realmFlag("Bad_Realm"), "")

	var flagErr *RealmFlagError
	require.ErrorAs(t, err, &flagErr)
	assert.EqualError(t, err, `--realm: invalid realm name "Bad_Realm": '_' is not a letter, a digit or a hyphen`)
	assert.NotNil(t, errors.Unwrap(err))
}

func TestResolveRealm_InvalidEnv(t *testing.T) {
	t.Setenv("SIND_REALM", "ci_42")

	_, err := ResolveRealm(nil, "from-config")

	require.EqualError(t, err, `SIND_REALM: invalid realm name "ci_42": '_' is not a letter, a digit or a hyphen`)
}

// TestResolveRealm_InvalidEnvNotUsed checks that SIND_REALM is only checked
// when it is the realm: a valid flag wins over it.
func TestResolveRealm_InvalidEnvNotUsed(t *testing.T) {
	t.Setenv("SIND_REALM", "ci_42")

	got, err := ResolveRealm(realmFlag("ok"), "")

	require.NoError(t, err)
	assert.Equal(t, "ok", got)
}
