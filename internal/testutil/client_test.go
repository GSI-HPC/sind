// SPDX-License-Identifier: LGPL-3.0-or-later

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	client, rec := NewClient(t)
	require.NotNil(t, client)
	require.NotNil(t, rec)
}
