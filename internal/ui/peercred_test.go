// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// procNetTCP is /proc/net/tcp with a listener of user 1000 on
// 127.0.0.1:7878, a listener that no process has accepted on port 7879, a
// client of user 1001 connected to the first, and rows that do not count.
const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1EC6 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 101 1 0000000000000000 100 0 0 10 0
   1: short line
   2: 0100007F:1EC7 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1001        0 0 1 0000000000000000 100 0 0 10 0
   3: zz00007F:1EC8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1002        0 104 1 0000000000000000 100 0 0 10 0
   4: 0100007F:D431 0100007F:1EC6 01 00000000:00000000 00:00000000 00000000  1001        0 102 1 0000000000000000 20 4 30 10 -1
`

func TestListenerOwner(t *testing.T) {
	table := func() *strings.Reader { return strings.NewReader(procNetTCP) }
	uid, err := listenerOwner(table(), netip.MustParseAddrPort("127.0.0.1:7878"))
	require.NoError(t, err)
	assert.Equal(t, 1000, uid)

	_, err = listenerOwner(table(), netip.MustParseAddrPort("127.0.0.1:1"))
	assert.ErrorContains(t, err, "no such socket")
	_, err = listenerOwner(table(), netip.MustParseAddrPort("127.0.0.1:7879"))
	assert.ErrorContains(t, err, "no such socket", "a socket without an inode does not count")
	_, err = listenerOwner(table(), netip.MustParseAddrPort("127.0.0.1:54321"))
	assert.ErrorContains(t, err, "no such socket", "a client's socket is no listener")

	// A line longer than the scanner takes is an error, not "no such socket".
	_, err = listenerOwner(strings.NewReader("header\n"+strings.Repeat("x", 1<<20)), netip.MustParseAddrPort("127.0.0.1:7878"))
	assert.ErrorContains(t, err, "too long")
}

func TestParseProcAddr(t *testing.T) {
	got, err := parseProcAddr("0100007F:1F90")
	require.NoError(t, err)
	assert.Equal(t, netip.MustParseAddrPort("127.0.0.1:8080"), got)
	for _, bad := range []string{"", "0100007F", "01007F:1F90", "zz00007F:1F90", "0100007F:fffff"} {
		_, err := parseProcAddr(bad)
		assert.Error(t, err, bad)
	}
}
