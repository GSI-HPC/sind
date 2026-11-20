// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"net/netip"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// lastOf returns the last address of an IPv4 prefix, its broadcast
// address: the address with every host bit set, one bit at a time.
func lastOf(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	for bit := p.Bits(); bit < 32; bit++ {
		a[bit/8] |= 0x80 >> (bit % 8)
	}
	return netip.AddrFrom4(a)
}

// toUint32 and addrOf turn an IPv4 address into its number and back.
func toUint32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func addrOf(n uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

// sizeOf returns the number of addresses of an IPv4 prefix.
func sizeOf(p netip.Prefix) uint64 { return 1 << (32 - p.Bits()) }

// TestPinnedLayout_Properties checks pinnedLayout on any IPv4 subnet and
// gateway, such as Docker picks from its address pools.
//
// It gives a layout exactly when the subnet is a /21 or larger and the
// gateway is in it and is not the address of the DNS container. In a
// layout:
//   - the subnet is the given one, masked, and the gateway the given one;
//   - the range that Docker takes the other addresses from is the lower
//     half of the subnet, and holds an address for every container but the
//     DNS that a bridge network can take, besides the network address and
//     the gateway;
//   - the DNS address is the last one before the broadcast address, so it
//     is in the subnet, outside the range, and no container, the network
//     or the gateway has it.
func TestPinnedLayout_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var a [4]byte
		for i := range a {
			a[i] = rapid.Byte().Draw(t, "subnet byte")
		}
		given := netip.PrefixFrom(netip.AddrFrom4(a), rapid.IntRange(0, 32).Draw(t, "bits"))
		subnet := given.Masked()
		last := lastOf(subnet)
		// Mostly a gateway of the subnet, as Docker gives: one of its first
		// or last addresses, or any of them; sometimes any address at all.
		anyAddr := addrOf(rapid.Uint32().Draw(t, "gateway bits"))
		hostBits := uint32(sizeOf(subnet) - 1)
		inSubnet := addrOf(toUint32(subnet.Addr()) | toUint32(anyAddr)&hostBits)
		gateway := rapid.SampledFrom([]netip.Addr{
			subnet.Addr(), subnet.Addr().Next(), last, addrOf(toUint32(last) - 1), addrOf(toUint32(last) - 2), inSubnet, anyAddr,
		}).Draw(t, "gateway")

		ipam, dns, ok := pinnedLayout(given.String(), gateway.String())

		wantDNS := addrOf(toUint32(last) - 1)
		want := subnet.Bits() <= 21 && subnet.Contains(gateway) && gateway != wantDNS
		require.Equal(t, want, ok)
		if !ok {
			assert.Empty(t, ipam)
			assert.Empty(t, dns)
			return
		}
		assert.Equal(t, subnet.String(), ipam.Subnet)
		assert.Equal(t, gateway.String(), ipam.Gateway)

		ipRange, err := netip.ParsePrefix(ipam.IPRange)
		require.NoError(t, err)
		assert.Equal(t, subnet.Addr(), ipRange.Addr(), "the range starts where the subnet starts")
		assert.Equal(t, subnet.Bits()+1, ipRange.Bits(), "the range is one half of the subnet")
		assert.Equal(t, ipRange, ipRange.Masked())
		assert.GreaterOrEqual(t, sizeOf(ipRange)-2, uint64(docker.MaxBridgeEndpoints-1), "room for the nodes, besides the network address and the gateway")

		addr, err := netip.ParseAddr(dns)
		require.NoError(t, err)
		assert.Equal(t, wantDNS, addr)
		assert.True(t, subnet.Contains(addr))
		assert.False(t, ipRange.Contains(addr), "no other container gets the DNS address")
		assert.NotEqual(t, subnet.Addr(), addr)
		assert.NotEqual(t, last, addr)
		assert.NotEqual(t, gateway, addr)
	})
}
