// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procNetTCPListener returns the owner of the socket that listens at addr,
// from the table net/tcp below procRoot, which is /proc on a host. The
// listener of a recorded sind ui must be the user's.
func procNetTCPListener(procRoot string, addr netip.AddrPort) (int, error) {
	f, err := os.Open(filepath.Join(procRoot, "net", "tcp"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return listenerOwner(f, addr)
}

// tcpListen is the state of a listening socket in /proc/net/tcp.
const tcpListen = "0A"

// listenerOwner reads a table in the format of /proc/net/tcp and returns the
// uid of the socket that listens at addr. Only a socket with an inode
// counts: one that no process has accepted yet has none, and its uid is not
// that of a process.
func listenerOwner(r io.Reader, addr netip.AddrPort) (int, error) {
	any4 := netip.AddrPortFrom(netip.IPv4Unspecified(), 0)
	sc := bufio.NewScanner(r)
	sc.Scan() // the header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != tcpListen || f[9] == "0" {
			continue
		}
		l, err1 := parseProcAddr(f[1])
		r, err2 := parseProcAddr(f[2])
		if err1 != nil || err2 != nil || l != addr || r != any4 {
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
