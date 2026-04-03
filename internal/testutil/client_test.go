// SPDX-License-Identifier: LGPL-3.0-or-later

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRealm(t *testing.T) {
	r := Realm("it-test")
	require.NotEmpty(t, r)
}

func TestNewClient(t *testing.T) {
	client, rec := NewClient(t)
	require.NotNil(t, client)
	require.NotNil(t, rec)
}
