// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPStream_ListensOnLocalhost(t *testing.T) {
	stream, _, err := NewRootCommand().Find([]string{"mcp", "stream"})
	require.NoError(t, err)

	host := stream.Flags().Lookup("host")
	require.NotNil(t, host)
	assert.Equal(t, "127.0.0.1", host.DefValue)
	assert.Equal(t, "127.0.0.1", host.Value.String())
	assert.Equal(t, "8080", stream.Flags().Lookup("port").DefValue)
}
