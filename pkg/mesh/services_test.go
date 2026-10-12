// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinnedDNSIP(t *testing.T) {
	var m mock.Executor
	m.AddResult(`{"sind.dns.ip":"172.18.255.254","sind.realm":"sind"}`+"\n", "", nil)
	m.AddResult(`{"sind.realm":"sind"}`+"\n", "", nil)
	m.AddResult("", "Error response from daemon: network sind-mesh not found\n", testutil.ExitCode1(t))
	m.AddResult("", "permission denied\n", fmt.Errorf("exit status 1"))
	mgr := NewManager(docker.NewClient(&m), DefaultRealm)

	ip, err := mgr.PinnedDNSIP(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "172.18.255.254", ip)

	ip, err = mgr.PinnedDNSIP(t.Context())
	require.NoError(t, err)
	assert.Empty(t, ip, "a mesh from before pinned addresses")

	ip, err = mgr.PinnedDNSIP(t.Context())
	require.NoError(t, err)
	assert.Empty(t, ip, "no mesh")

	_, err = mgr.PinnedDNSIP(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network: ")
}

func TestServiceIP(t *testing.T) {
	assert.Equal(t, "172.18.255.253", ServiceIP("172.18.255.254", 1))
	assert.Equal(t, "172.18.255.251", ServiceIP("172.18.255.254", 3))
	assert.Equal(t, "10.1.0.255", ServiceIP("10.1.1.0", 1))
	assert.Empty(t, ServiceIP("", 1))
	assert.Empty(t, ServiceIP("fd00::1", 1))
}
