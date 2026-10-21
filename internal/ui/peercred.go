// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// peerListener accepts only connections from processes of one user. Other
// users can reach 127.0.0.1 too; they lack the token, and this closes their
// connections before a request is read.
type peerListener struct {
	net.Listener
	uid int
	// owner returns the user that owns the socket at local connected to
	// remote, as the kernel tells it.
	owner func(local, remote netip.AddrPort) (int, error)
	// refused is told about each connection closed, for the log.
	refused func(err error)
}

// Accept returns the next connection of the user, and closes the others.
func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		err = l.check(c)
		if err == nil {
			return c, nil
		}
		_ = c.Close()
		if l.refused != nil {
			l.refused(err)
		}
	}
}

// check looks up the owner of the client's end of c.
func (l *peerListener) check(c net.Conn) error {
	client, err1 := netip.ParseAddrPort(c.RemoteAddr().String())
	server, err2 := netip.ParseAddrPort(c.LocalAddr().String())
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	uid, err := l.owner(client, server)
	if err != nil {
		return fmt.Errorf("connection from %s: %w", client, err)
	}
	if uid != l.uid {
		return fmt.Errorf("connection from %s: refused a process of user %d", client, uid)
	}
	return nil
}

// procNetTCPOwner returns a function that finds the owner of a loopback
// socket in the table net/tcp below procRoot, which is /proc on a host.
func procNetTCPOwner(procRoot string) func(local, remote netip.AddrPort) (int, error) {
	return func(local, remote netip.AddrPort) (int, error) {
		f, err := os.Open(filepath.Join(procRoot, "net", "tcp"))
		if err != nil {
			return 0, err
		}
		defer func() { _ = f.Close() }()
		return socketOwner(f, local, remote)
	}
}

// socketOwner reads a table in the format of /proc/net/tcp and returns the
// uid of the socket bound to local and connected to remote. Only a socket
// that is connected (state 01, ESTABLISHED) and has an inode counts: a
// socket in TIME_WAIT that once had the same addresses has neither, and its
// uid is 0.
func socketOwner(r io.Reader, local, remote netip.AddrPort) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Scan() // the header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "01" || f[9] == "0" {
			continue
		}
		l, err1 := parseProcAddr(f[1])
		r, err2 := parseProcAddr(f[2])
		if err1 != nil || err2 != nil || l != local || r != remote {
			continue
		}
		return strconv.Atoi(f[7])
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("no such socket in /proc/net/tcp")
}

// parseProcAddr parses an IPv4 address and port as /proc/net/tcp writes
// them: the address as a 32-bit number in host byte order, the port in
// big-endian hex, such as 0100007F:1F90 for 127.0.0.1:8080 on x86.
func parseProcAddr(s string) (netip.AddrPort, error) {
	ip, port, ok := strings.Cut(s, ":")
	if !ok || len(ip) != 8 {
		return netip.AddrPort{}, fmt.Errorf("bad address %q", s)
	}
	b, err := hex.DecodeString(ip)
	if err != nil {
		return netip.AddrPort{}, err
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], binary.NativeEndian.Uint32(b))
	return netip.AddrPortFrom(netip.AddrFrom4(a), uint16(p)), nil
}
