// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/observability"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func TestWrite(t *testing.T) {
	var b strings.Builder
	require.NoError(t, write(&b))
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	assert.Equal(t, mesh.DNSImage, lines[0])
	assert.Equal(t, observability.Images(), lines[1:])
	assert.Len(t, lines, 5, "CoreDNS and the four of the monitoring stack")
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestWrite_Error(t *testing.T) {
	assert.EqualError(t, write(failingWriter{}), "closed")
}
