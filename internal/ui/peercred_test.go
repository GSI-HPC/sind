// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// procNetTCP is /proc/net/tcp with a listener on 127.0.0.1:7878, a client
// of user 1001 connected to it from port 54321, and the server's end.
const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1EC6 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 101 1 0000000000000000 100 0 0 10 0
   1: short line
   2: zz00007F:D431 0100007F:1EC6 01 00000000:00000000 00:00000000 00000000  1002        0 104 1 0000000000000000 20 4 30 10 -1
   3: 0100007F:D431 0100007F:1EC6 01 00000000:00000000 00:00000000 00000000  1001        0 102 1 0000000000000000 20 4 30 10 -1
   4: 0100007F:1EC6 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 103 1 0000000000000000 20 4 30 10 -1
`

func TestSocketOwner(t *testing.T) {
	client := netip.MustParseAddrPort("127.0.0.1:54321")
	server := netip.MustParseAddrPort("127.0.0.1:7878")
	uid, err := socketOwner(strings.NewReader(procNetTCP), client, server)
	require.NoError(t, err)
	assert.Equal(t, 1001, uid, "the client's end")
	uid, err = socketOwner(strings.NewReader(procNetTCP), server, client)
	require.NoError(t, err)
	assert.Equal(t, 1000, uid, "the server's end")

	_, err = socketOwner(strings.NewReader(procNetTCP), netip.MustParseAddrPort("127.0.0.1:1"), server)
	assert.ErrorContains(t, err, "no such socket")

	// A line longer than the scanner takes is an error, not "no such socket".
	_, err = socketOwner(strings.NewReader("header\n"+strings.Repeat("x", 1<<20)), client, server)
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

func TestProcNetTCPOwnerReadsBelowTheRoot(t *testing.T) {
	root := t.TempDir()
	owner := procNetTCPOwner(root)
	client := netip.MustParseAddrPort("127.0.0.1:54321")
	server := netip.MustParseAddrPort("127.0.0.1:7878")

	_, err := owner(client, server)
	require.ErrorIs(t, err, os.ErrNotExist, "no table below the root")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "net"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(procNetTCP), 0o600))
	uid, err := owner(client, server)
	require.NoError(t, err)
	assert.Equal(t, 1001, uid)
}

// TestPeerListenerOwnUser connects to a real listener: the kernel's table
// must name this process's user as the client's owner.
func TestPeerListenerOwnUser(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("no /proc/net/tcp")
	}
	inner, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	var refused []error
	l := &peerListener{Listener: inner, uid: os.Getuid(), owner: procNetTCPOwner("/proc"), refused: func(err error) { refused = append(refused, err) }}
	defer func() { _ = l.Close() }()
	go func() {
		c, err := net.Dial("tcp4", inner.Addr().String())
		if err == nil {
			time.Sleep(100 * time.Millisecond)
			_ = c.Close()
		}
	}()
	c, err := l.Accept()
	require.NoError(t, err)
	_ = c.Close()
	assert.Empty(t, refused)
}

func TestPeerListenerRefusesOtherUsers(t *testing.T) {
	inner, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	owners := []int{1001, 0, 1000}
	var refused []error
	l := &peerListener{
		Listener: inner, uid: 1000,
		owner: func(netip.AddrPort, netip.AddrPort) (int, error) {
			uid := owners[0]
			owners = owners[1:]
			if uid == 0 {
				return 0, errors.New("no such socket")
			}
			return uid, nil
		},
		refused: func(err error) { refused = append(refused, err) },
	}
	defer func() { _ = l.Close() }()
	closed := make(chan bool, 3)
	for range 3 {
		go func() {
			c, err := net.Dial("tcp4", inner.Addr().String())
			if err != nil {
				closed <- false
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err = c.Read(make([]byte, 1))
			closed <- err != nil && !errors.Is(err, os.ErrDeadlineExceeded)
		}()
	}
	c, err := l.Accept()
	require.NoError(t, err)
	_ = c.Close()
	require.Len(t, refused, 2)
	assert.ErrorContains(t, refused[0], "refused a process of user 1001")
	assert.ErrorContains(t, refused[1], "no such socket")
	for range 3 {
		assert.True(t, <-closed, "a connection was neither accepted and closed nor refused")
	}
}

func TestPeerListenerAcceptError(t *testing.T) {
	inner, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	l := &peerListener{Listener: inner, uid: 1000}
	require.NoError(t, l.Close())
	_, err = l.Accept()
	assert.ErrorIs(t, err, net.ErrClosed)
}

// fakeConn is a connection with made-up addresses.
type fakeConn struct {
	net.Conn
	local, remote string
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func (c *fakeConn) LocalAddr() net.Addr  { return fakeAddr(c.local) }
func (c *fakeConn) RemoteAddr() net.Addr { return fakeAddr(c.remote) }

func TestPeerListenerCheckBadAddresses(t *testing.T) {
	l := &peerListener{uid: 1000, owner: func(netip.AddrPort, netip.AddrPort) (int, error) { return 1000, nil }}
	assert.NoError(t, l.check(&fakeConn{local: "127.0.0.1:7878", remote: "127.0.0.1:54321"}))
	assert.Error(t, l.check(&fakeConn{local: "127.0.0.1:7878", remote: "unix"}))
	assert.Error(t, l.check(&fakeConn{local: "pipe", remote: "127.0.0.1:54321"}))
}
