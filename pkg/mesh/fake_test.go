// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"path"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/require"
)

// fakeContainer is a container of fakeDocker.
type fakeContainer struct {
	image    string
	state    docker.ContainerState
	networks []string          // networks it joins
	ip       string            // --ip on its first network
	ips      map[string]string // address per network while running
	dns      []string          // --dns servers
	files    map[string]string // file contents by absolute path
	signals  []string          // signals received with kill -s
}

// fakeNetwork is a network of fakeDocker.
type fakeNetwork struct {
	subnet  netip.Prefix
	gateway netip.Addr
	ipRange netip.Prefix // the subnet unless created with --ip-range
	labels  map[string]string
	used    map[netip.Addr]bool // addresses of running containers
}

// fakeDocker is a stateful stand-in for the docker CLI in mesh unit tests,
// for code that runs docker calls concurrently, where queued results do not
// work. It knows networks, volumes and containers, and the files the mesh
// writes into containers. A network created without --subnet gets the
// first free 10.N.0.0/16 and the gateway 10.N.0.1. A starting container
// gets its --ip, or else the lowest free address of the network's ip-range,
// and loses it when it stops, as Docker does. Calls whose joined arguments
// start with a key of fail get that result.
type fakeDocker struct {
	t          *testing.T
	networks   map[string]*fakeNetwork
	volumes    map[string]bool
	containers map[string]*fakeContainer
	fail       map[string]mock.Result
}

// newFake returns an empty fakeDocker.
func newFake(t *testing.T) *fakeDocker {
	return &fakeDocker{
		t:          t,
		networks:   map[string]*fakeNetwork{},
		volumes:    map[string]bool{},
		containers: map[string]*fakeContainer{},
		fail:       map[string]mock.Result{},
	}
}

// newFakeDocker returns an empty fakeDocker and a client that talks to it.
func newFakeDocker(t *testing.T) (*fakeDocker, *docker.Client, *mock.Executor) {
	f := newFake(t)
	m := &mock.Executor{OnCall: f.onCall}
	return f, docker.NewClient(m), m
}

// withMesh adds a complete mesh for realm, as earlier sind versions created
// it: a network without a pinned DNS address, with both containers in
// state.
func (f *fakeDocker) withMesh(realm string, state docker.ContainerState) *fakeDocker {
	return f.addMesh(realm, state, false)
}

// withPinnedMesh adds a complete mesh for realm whose network pins the DNS
// container's address, with both containers in state.
func (f *fakeDocker) withPinnedMesh(realm string, state docker.ContainerState) *fakeDocker {
	return f.addMesh(realm, state, true)
}

func (f *fakeDocker) addMesh(realm string, state docker.ContainerState, pinned bool) *fakeDocker {
	mgr := NewManager(nil, realm)
	mesh := string(mgr.NetworkName())
	nw := f.addNetwork(mesh)
	dns := &fakeContainer{
		image: DNSImage, state: docker.StateCreated, networks: []string{mesh},
		files: map[string]string{corefilePath: generateCorefile(realm, nil)},
	}
	if pinned {
		ipam, dnsIP, ok := pinnedLayout(nw.subnet.String(), nw.gateway.String())
		require.True(f.t, ok)
		nw.ipRange = netip.MustParsePrefix(ipam.IPRange)
		nw.labels[LabelDNSIP] = dnsIP
		dns.ip = dnsIP
	}
	f.volumes[string(mgr.SSHVolumeName())] = true
	f.containers[string(mgr.DNSContainerName())] = dns
	require.NoError(f.t, f.start(string(mgr.DNSContainerName())))
	f.containers[string(mgr.SSHContainerName())] = &fakeContainer{
		image: SSHImage(), state: docker.StateCreated, networks: []string{mesh}, dns: []string{dns.ips[mesh]},
		files: map[string]string{knownHostsPath: ""},
	}
	require.NoError(f.t, f.start(string(mgr.SSHContainerName())))
	if state != docker.StateRunning {
		f.stop(string(mgr.SSHContainerName()), state)
		f.stop(string(mgr.DNSContainerName()), state)
	}
	return f
}

// addNetwork adds a network with the first free 10.N.0.0/16 subnet.
func (f *fakeDocker) addNetwork(name string) *fakeNetwork {
	for n := 0; ; n++ {
		subnet := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(n), 0, 0}), 16)
		if !f.overlaps(subnet) {
			gateway := subnet.Addr().Next()
			return f.addNetworkWith(name, subnet, gateway, subnet)
		}
	}
}

func (f *fakeDocker) addNetworkWith(name string, subnet netip.Prefix, gateway netip.Addr, ipRange netip.Prefix) *fakeNetwork {
	nw := &fakeNetwork{subnet: subnet, gateway: gateway, ipRange: ipRange, labels: map[string]string{}, used: map[netip.Addr]bool{}}
	f.networks[name] = nw
	return nw
}

// overlaps reports whether subnet overlaps the subnet of a network.
func (f *fakeDocker) overlaps(subnet netip.Prefix) bool {
	for _, nw := range f.networks {
		if nw.subnet.Overlaps(subnet) {
			return true
		}
	}
	return false
}

// start gives a container its addresses and makes it run. It fails as
// Docker does when the container's --ip is taken.
func (f *fakeDocker) start(name string) error {
	c := f.containers[name]
	ips := map[string]string{}
	for i, n := range c.networks {
		nw := f.networks[n]
		if nw == nil {
			nw = f.addNetwork(n)
		}
		addr := nw.free()
		if i == 0 && c.ip != "" {
			addr = netip.MustParseAddr(c.ip)
		}
		if nw.used[addr] {
			f.release(ips)
			return fmt.Errorf("Address already in use")
		}
		nw.used[addr] = true
		ips[n] = addr.String()
	}
	c.ips = ips
	c.state = docker.StateRunning
	return nil
}

// free returns the lowest address of the network's ip-range that no
// container has, other than the subnet's first address and the gateway.
func (nw *fakeNetwork) free() netip.Addr {
	addr := nw.ipRange.Addr()
	for addr == nw.subnet.Addr() || addr == nw.gateway || nw.used[addr] {
		addr = addr.Next()
	}
	return addr
}

func (f *fakeDocker) stop(name string, state docker.ContainerState) {
	c := f.containers[name]
	f.release(c.ips)
	c.ips = nil
	c.state = state
}

// release frees the addresses that ips holds per network.
func (f *fakeDocker) release(ips map[string]string) {
	for n, ip := range ips {
		if nw := f.networks[n]; nw != nil {
			delete(nw.used, netip.MustParseAddr(ip))
		}
	}
}

func notFound(t *testing.T, stderr string) mock.Result {
	return mock.Result{Stderr: stderr, Err: testutil.ExitCode1(t)}
}

// onCall answers one docker call.
func (f *fakeDocker) onCall(args []string, stdin string) mock.Result {
	joined := strings.Join(args, " ")
	for prefix, r := range f.fail {
		// The prefix ends at a word boundary: "rm -f -v sind-ssh" does not
		// match the removal of sind-ssh-keygen.
		rest, ok := strings.CutPrefix(joined, prefix)
		if ok && (rest == "" || strings.ContainsAny(rest[:1], " :")) {
			return r
		}
	}
	last := args[len(args)-1]
	switch {
	case joined == "network inspect "+last:
		nw, ok := f.networks[last]
		if !ok {
			return notFound(f.t, testutil.NoSuchNetwork(last))
		}
		return mock.Result{Stdout: nw.inspectJSON(last)}
	case len(args) == 5 && joined == "network inspect "+args[2]+" --format {{json .Labels}}":
		nw, ok := f.networks[args[2]]
		if !ok {
			return notFound(f.t, testutil.NoSuchNetwork(args[2]))
		}
		out, _ := json.Marshal(nw.labels)
		return mock.Result{Stdout: string(out) + "\n"}
	case strings.HasPrefix(joined, "network create "):
		return f.createNetwork(args[2:])
	case joined == "network rm "+last:
		if _, ok := f.networks[last]; !ok {
			return notFound(f.t, testutil.NoSuchNetwork(last))
		}
		delete(f.networks, last)
		return mock.Result{}
	case joined == "volume inspect "+last:
		if !f.volumes[last] {
			return notFound(f.t, testutil.NoSuchVolume(last))
		}
		return mock.Result{Stdout: "[{}]\n"}
	case strings.HasPrefix(joined, "volume create "):
		f.volumes[last] = true
		return mock.Result{Stdout: last + "\n"}
	case joined == "volume rm "+last:
		if !f.volumes[last] {
			return notFound(f.t, testutil.NoSuchVolume(last))
		}
		delete(f.volumes, last)
		return mock.Result{}
	case joined == "inspect "+last, joined == "container inspect "+last:
		c, ok := f.containers[last]
		if !ok {
			return notFound(f.t, testutil.NoSuchContainer(last))
		}
		return mock.Result{Stdout: f.inspectJSON(last, c)}
	case args[0] == "create":
		return f.create(args[1:])
	case args[0] == "start":
		c, ok := f.containers[last]
		if !ok {
			return notFound(f.t, testutil.NoSuchContainer(last))
		}
		if c.state != docker.StateRunning {
			if err := f.start(last); err != nil {
				return mock.Result{Stderr: "Error response from daemon: " + err.Error() + "\n", Err: testutil.ExitCode1(f.t)}
			}
		}
		return mock.Result{Stdout: last + "\n"}
	case joined == "stop "+last:
		c, ok := f.containers[last]
		if !ok {
			return notFound(f.t, testutil.NoSuchContainer(last))
		}
		if c.state == docker.StateRunning {
			f.stop(last, docker.StateExited)
		}
		return mock.Result{Stdout: last + "\n"}
	case joined == "kill -s USR1 "+last:
		c, ok := f.containers[last]
		if !ok || c.state != docker.StateRunning {
			return mock.Result{Stderr: "Error response from daemon: container " + last + " is not running\n", Err: testutil.ExitCode1(f.t)}
		}
		c.signals = append(c.signals, "USR1")
		return mock.Result{Stdout: last + "\n"}
	case joined == "rm -f -v "+last:
		c, ok := f.containers[last]
		if !ok {
			return notFound(f.t, testutil.NoSuchContainer(last))
		}
		if c.state == docker.StateRunning {
			f.stop(last, docker.StateExited)
		}
		delete(f.containers, last)
		return mock.Result{Stdout: last + "\n"}
	case args[0] == "cp" && args[1] == "-":
		name, dir, _ := strings.Cut(args[2], ":")
		c, ok := f.containers[name]
		if !ok {
			return notFound(f.t, testutil.NoSuchContainer(name))
		}
		tr := tar.NewReader(strings.NewReader(stdin))
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return mock.Result{Err: err}
			}
			data, _ := io.ReadAll(tr)
			c.files[path.Join(dir, hdr.Name)] = string(data)
		}
		return mock.Result{}
	case args[0] == "cp" && last == "-":
		name, file, _ := strings.Cut(args[1], ":")
		c, ok := f.containers[name]
		if !ok {
			return notFound(f.t, testutil.NoSuchContainer(name))
		}
		return mock.Result{Stdout: testutil.TarArchive(path.Base(file), c.files[file])}
	case args[0] == "exec":
		return f.exec(args[1:], stdin)
	}
	f.t.Errorf("fakeDocker: unexpected call: %s", joined)
	return mock.Result{Err: fmt.Errorf("unexpected call: %s", joined)}
}

// create handles docker create with the flags the mesh uses.
func (f *fakeDocker) create(args []string) mock.Result {
	c := &fakeContainer{state: docker.StateCreated, files: map[string]string{}}
	var name string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			name = args[i+1]
			i++
		case "--network":
			c.networks = append(c.networks, args[i+1])
			i++
		case "--dns":
			c.dns = append(c.dns, args[i+1])
			i++
		case "--ip":
			c.ip = args[i+1]
			i++
		case "-v", "--label", "--pull":
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if _, ok := f.containers[name]; ok {
		return mock.Result{Stderr: "Conflict. The container name is already in use\n", Err: testutil.ExitCode1(f.t)}
	}
	c.image = rest[0]
	f.containers[name] = c
	return mock.Result{Stdout: "id-" + name + "\n"}
}

// createNetwork handles docker network create with the flags the mesh
// uses. A --subnet that overlaps another network's fails as with Docker's
// address allocator.
func (f *fakeDocker) createNetwork(args []string) mock.Result {
	name := args[len(args)-1]
	if _, ok := f.networks[name]; ok {
		return mock.Result{Stderr: "Error response from daemon: network with name " + name + " already exists\n", Err: testutil.ExitCode1(f.t)}
	}
	flags := map[string]string{}
	labels := map[string]string{}
	for i := 0; i < len(args)-1; i += 2 {
		if args[i] == "--label" {
			k, v, _ := strings.Cut(args[i+1], "=")
			labels[k] = v
			continue
		}
		flags[args[i]] = args[i+1]
	}
	var nw *fakeNetwork
	if subnet, ok := flags["--subnet"]; ok {
		prefix := netip.MustParsePrefix(subnet)
		if f.overlaps(prefix) {
			return mock.Result{Stderr: "Error response from daemon: invalid pool request: Pool overlaps with other one on this address space\n", Err: testutil.ExitCode1(f.t)}
		}
		ipRange := prefix
		if r, ok := flags["--ip-range"]; ok {
			ipRange = netip.MustParsePrefix(r)
		}
		nw = f.addNetworkWith(name, prefix, netip.MustParseAddr(flags["--gateway"]), ipRange)
	} else {
		nw = f.addNetwork(name)
	}
	nw.labels = labels
	return mock.Result{Stdout: "net-" + name + "\n"}
}

// inspectJSON renders docker network inspect output for a network.
func (nw *fakeNetwork) inspectJSON(name string) string {
	config := map[string]string{"Subnet": nw.subnet.String(), "Gateway": nw.gateway.String()}
	if nw.ipRange != nw.subnet {
		config["IPRange"] = nw.ipRange.String()
	}
	doc := []map[string]any{{
		"Id":     "abcdef0123456789",
		"Name":   name,
		"Driver": "bridge",
		"Labels": nw.labels,
		"IPAM":   map[string]any{"Config": []map[string]string{config}},
	}}
	out, _ := json.Marshal(doc)
	return string(out) + "\n"
}

// exec handles the file reads and writes the mesh runs in its containers.
func (f *fakeDocker) exec(args []string, stdin string) mock.Result {
	if args[0] == "-i" {
		args = args[1:]
	}
	name := args[0]
	c, ok := f.containers[name]
	if !ok || c.state != docker.StateRunning {
		return mock.Result{Stderr: "Error response from daemon: container " + name + " is not running\n", Err: testutil.ExitCode1(f.t)}
	}
	cmd := strings.Join(args[1:], " ")
	switch {
	case strings.HasPrefix(cmd, "cat "):
		return mock.Result{Stdout: c.files[strings.TrimPrefix(cmd, "cat ")]}
	case strings.HasPrefix(cmd, "sh -c cat >> "):
		file := strings.TrimPrefix(cmd, "sh -c cat >> ")
		c.files[file] += stdin
		return mock.Result{}
	case strings.HasPrefix(cmd, "sh -c cat > "):
		c.files[strings.TrimPrefix(cmd, "sh -c cat > ")] = stdin
		return mock.Result{}
	}
	f.t.Errorf("fakeDocker: unexpected exec: %s", cmd)
	return mock.Result{Err: fmt.Errorf("unexpected exec: %s", cmd)}
}

// inspectJSON renders docker inspect output for a container.
func (f *fakeDocker) inspectJSON(name string, c *fakeContainer) string {
	networks := map[string]map[string]string{}
	for _, n := range c.networks {
		networks[n] = map[string]string{"IPAddress": c.ips[n]}
	}
	doc := []map[string]any{{
		"Id":              "id-" + name,
		"Name":            "/" + name,
		"State":           map[string]any{"Status": string(c.state)},
		"Config":          map[string]any{"Image": c.image, "Labels": map[string]string{}},
		"HostConfig":      map[string]any{"Dns": c.dns},
		"NetworkSettings": map[string]any{"Networks": networks},
	}}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(doc)
	return buf.String()
}

// calls returns the joined arguments of the recorded calls that start with
// prefix.
func calls(m *mock.Executor, prefix string) []string {
	var out []string
	for _, c := range m.Calls {
		if joined := strings.Join(c.Args, " "); strings.HasPrefix(joined, prefix) {
			out = append(out, joined)
		}
	}
	return out
}
