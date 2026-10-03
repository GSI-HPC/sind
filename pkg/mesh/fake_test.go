// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// fakeContainer is a container of fakeDocker.
type fakeContainer struct {
	image    string
	state    docker.ContainerState
	networks []string          // networks it joins
	ips      map[string]string // address per network while running
	dns      []string          // --dns servers
	files    map[string]string // file contents by absolute path
	signals  []string          // signals received with kill -s
}

// fakeDocker is a stateful stand-in for the docker CLI in mesh unit tests,
// for code that runs docker calls concurrently, where queued results do not
// work. It knows networks, volumes and containers, and the files the mesh
// writes into containers. Addresses come from one counter per network,
// starting at .2, and are released when a container stops, as Docker does.
// Calls whose joined arguments start with a key of fail get that result.
type fakeDocker struct {
	t          *testing.T
	networks   map[string]bool
	volumes    map[string]bool
	containers map[string]*fakeContainer
	used       map[string]map[int]bool // network → taken host numbers
	fail       map[string]mock.Result
}

// newFake returns an empty fakeDocker.
func newFake(t *testing.T) *fakeDocker {
	return &fakeDocker{
		t:          t,
		networks:   map[string]bool{},
		volumes:    map[string]bool{},
		containers: map[string]*fakeContainer{},
		used:       map[string]map[int]bool{},
		fail:       map[string]mock.Result{},
	}
}

// newFakeDocker returns an empty fakeDocker and a client that talks to it.
func newFakeDocker(t *testing.T) (*fakeDocker, *docker.Client, *mock.Executor) {
	f := newFake(t)
	m := &mock.Executor{OnCall: f.onCall}
	return f, docker.NewClient(m), m
}

// withMesh adds a complete mesh for realm, with both containers in state.
func (f *fakeDocker) withMesh(realm string, state docker.ContainerState) *fakeDocker {
	mgr := NewManager(nil, realm)
	mesh := string(mgr.NetworkName())
	f.networks[mesh] = true
	f.volumes[string(mgr.SSHVolumeName())] = true
	f.containers[string(mgr.DNSContainerName())] = &fakeContainer{
		image: DNSImage, state: docker.StateCreated, networks: []string{mesh},
		files: map[string]string{corefilePath: generateCorefile(realm, nil)},
	}
	f.start(string(mgr.DNSContainerName()))
	dnsIP := f.containers[string(mgr.DNSContainerName())].ips[mesh]
	f.containers[string(mgr.SSHContainerName())] = &fakeContainer{
		image: SSHImage(), state: docker.StateCreated, networks: []string{mesh}, dns: []string{dnsIP},
		files: map[string]string{knownHostsPath: ""},
	}
	f.start(string(mgr.SSHContainerName()))
	if state != docker.StateRunning {
		f.stop(string(mgr.SSHContainerName()), state)
		f.stop(string(mgr.DNSContainerName()), state)
	}
	return f
}

func (f *fakeDocker) start(name string) {
	c := f.containers[name]
	c.state = docker.StateRunning
	c.ips = map[string]string{}
	for _, n := range c.networks {
		if f.used[n] == nil {
			f.used[n] = map[int]bool{}
		}
		host := 2
		for f.used[n][host] {
			host++
		}
		f.used[n][host] = true
		c.ips[n] = fmt.Sprintf("10.0.0.%d", host)
	}
}

func (f *fakeDocker) stop(name string, state docker.ContainerState) {
	c := f.containers[name]
	for n, ip := range c.ips {
		var host int
		_, _ = fmt.Sscanf(ip, "10.0.0.%d", &host)
		delete(f.used[n], host)
	}
	c.ips = nil
	c.state = state
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
		if !f.networks[last] {
			return notFound(f.t, testutil.NoSuchNetwork(last))
		}
		return mock.Result{Stdout: `[{"Id":"abcdef0123456789","Name":"` + last + `","Driver":"bridge"}]`}
	case strings.HasPrefix(joined, "network create "):
		if f.networks[last] {
			return mock.Result{Stderr: "Error response from daemon: network with name " + last + " already exists\n", Err: testutil.ExitCode1(f.t)}
		}
		f.networks[last] = true
		return mock.Result{Stdout: "net-" + last + "\n"}
	case joined == "network rm "+last:
		if !f.networks[last] {
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
			f.start(last)
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
