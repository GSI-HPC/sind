// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// fakeDaemon is a Docker daemon in memory, for the calls of CreateInRealm
// and Delete in a realm without a monitoring stack: it keeps the networks,
// the volumes and the containers, and the files that sind writes into
// containers and reads back. A test can thus state what a run left
// behind, whatever calls it made. Unlike realmState, a layer over
// happyOnCall for the resources of one cluster, it answers every call from
// what it holds, the mesh included, and fails the test on a call that it
// does not know.
//
// What runs inside a node (systemd, sshd, Slurm) is not modelled: those
// calls get the answers of happyOnCall, a node that comes up and works,
// once the fake has checked that the container runs. A file goes with its
// container, also one on a volume.
type fakeDaemon struct {
	t          *testing.T
	mu         sync.Mutex // docker events start outside the executor's lock
	networks   map[string]docker.Labels
	volumes    map[string]docker.Labels
	containers map[string]*fakeContainer
	files      map[string]string // "<container>:<path>" → content
	node       func(args []string, stdin string) mock.Result
	exit1      error

	// fault answers a call in place of the daemon when it returns true.
	fault func(args []string) bool
	// calls counts the calls, those that fault answered included.
	calls int
	// faulted are the calls that fault answered.
	faulted []string
}

// fakeContainer is a container of a fakeDaemon.
type fakeContainer struct {
	labels   docker.Labels
	networks []string
	running  bool
}

// errFault is the error of a call that a fakeDaemon's fault answered.
var errFault = errors.New("docker daemon unavailable")

// failAt makes the k-th call from now fail, and with onward every call
// after it too, as when the daemon went away.
func (d *fakeDaemon) failAt(k int, onward bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	first := d.calls + k
	d.fault = func([]string) bool { return d.calls == first || onward && d.calls > first }
}

// heal ends the failures, and returns the calls that failed.
func (d *fakeDaemon) heal() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	failed := d.faulted
	d.fault, d.faulted = nil, nil
	return failed
}

// count returns the number of calls so far.
func (d *fakeDaemon) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func newFakeDocker(t *testing.T) *fakeDaemon {
	t.Helper()
	return &fakeDaemon{
		t:          t,
		networks:   map[string]docker.Labels{},
		volumes:    map[string]docker.Labels{},
		containers: map[string]*fakeContainer{},
		files:      map[string]string{},
		node:       happyOnCall(t, nil),
		exit1:      testutil.ExitCode1(t),
	}
}

// executor returns an executor on the daemon, with docker events that
// tell nothing.
func (d *fakeDaemon) executor() *mock.Executor {
	pipes := &mock.Pipes{}
	d.t.Cleanup(pipes.CloseAll)
	return &mock.Executor{OnCall: d.onCall, OnStart: func(args []string) mock.StreamResult {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.faults(args) {
			return mock.StreamResult{Err: errFault}
		}
		return pipes.OnStart(args)
	}}
}

// state describes all that the daemon holds, one line each, sorted: the
// networks, the volumes, the containers with what they are attached to,
// and the files, with each line of those that list the nodes of the realm
// (the Corefile and known_hosts); the others hold keys, new ones each run.
func (d *fakeDaemon) state() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for name := range d.networks {
		out = append(out, "network "+name)
	}
	for name := range d.volumes {
		out = append(out, "volume "+name)
	}
	for name, c := range d.containers {
		state := "stopped"
		if c.running {
			state = "running"
		}
		out = append(out, fmt.Sprintf("container %s %s on %s", name, state, strings.Join(slices.Sorted(slices.Values(c.networks)), ",")))
	}
	for file, content := range d.files {
		if base := path.Base(file); base != "Corefile" && base != "known_hosts" {
			out = append(out, "file "+file)
			continue
		}
		for line := range strings.Lines(content) {
			out = append(out, "file "+file+": "+strings.TrimSpace(line))
		}
	}
	slices.Sort(out)
	return out
}

// stateDiff returns the lines that got lacks (-) and has more (+) than
// want, or "" for equal states.
func stateDiff(want, got []string) string {
	var out []string
	for _, line := range want {
		if !slices.Contains(got, line) {
			out = append(out, "- "+line)
		}
	}
	for _, line := range got {
		if !slices.Contains(want, line) {
			out = append(out, "+ "+line)
		}
	}
	return strings.Join(out, "\n")
}

// faults counts a call, and reports whether fault answers it.
func (d *fakeDaemon) faults(args []string) bool {
	d.calls++
	if d.fault == nil || !d.fault(args) {
		return false
	}
	d.faulted = append(d.faulted, strings.Join(args, " "))
	return true
}

func (d *fakeDaemon) notFound(stderr string) mock.Result {
	return mock.Result{Stderr: stderr, Err: d.exit1}
}

func (d *fakeDaemon) unexpected(args []string) mock.Result {
	d.t.Errorf("fakeDaemon: unexpected docker call: %s", strings.Join(args, " "))
	return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
}

func (d *fakeDaemon) onCall(args []string, stdin string) mock.Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.faults(args) {
		return mock.Result{Err: errFault}
	}
	switch args[0] {
	case "network":
		return d.network(args)
	case "volume":
		return d.volume(args)
	case "image":
		return d.node(args, stdin)
	case "run", "create":
		return d.create(args, stdin)
	case "start", "stop", "kill":
		c, ok := d.containers[args[len(args)-1]]
		if !ok {
			return d.notFound(testutil.NoSuchContainer(args[len(args)-1]))
		}
		switch {
		case args[0] == "start":
			c.running = true
		case args[0] == "stop", len(args) == 2:
			c.running = false
		case !c.running: // kill -s SIGNAL
			return mock.Result{Stderr: "Error response from daemon: container " + args[len(args)-1] + " is not running\n", Err: d.exit1}
		}
		return mock.Result{}
	case "rm":
		name := args[len(args)-1]
		if _, ok := d.containers[name]; !ok {
			return d.notFound(testutil.NoSuchContainer(name))
		}
		delete(d.containers, name)
		for file := range d.files {
			if strings.HasPrefix(file, name+":") {
				delete(d.files, file)
			}
		}
		return mock.Result{}
	case "logs":
		if _, ok := d.containers[args[len(args)-1]]; !ok {
			return d.notFound(testutil.NoSuchContainer(args[len(args)-1]))
		}
		return mock.Result{}
	case "ps":
		return d.ps(args)
	case "inspect":
		return d.inspect(args)
	case "cp":
		return d.cp(args, stdin)
	case "exec":
		return d.exec(args, stdin)
	}
	return d.unexpected(args)
}

// ls answers docker network ls and docker volume ls.
func ls(objects map[string]docker.Labels, args []string) mock.Result {
	f := filtersOf(args)
	var out strings.Builder
	for _, name := range slices.Sorted(maps.Keys(objects)) {
		if !hasLabels(objects[name], f["label"]) {
			continue
		}
		if names := f["name"]; len(names) > 0 && !slices.ContainsFunc(names, func(n string) bool { return strings.Contains(name, n) }) {
			continue
		}
		line, _ := json.Marshal(map[string]string{"Name": name, "Driver": "bridge", "Labels": joinLabels(objects[name])})
		out.Write(line)
		out.WriteByte('\n')
	}
	return mock.Result{Stdout: out.String()}
}

func (d *fakeDaemon) network(args []string) mock.Result {
	name := args[len(args)-1]
	switch args[1] {
	case "ls":
		return ls(d.networks, args)
	case "create":
		if _, ok := d.networks[name]; ok {
			return mock.Result{Stderr: "Error response from daemon: network with name " + name + " already exists\n", Err: d.exit1}
		}
		d.networks[name] = labelFlags(args)
		return mock.Result{Stdout: "id-" + name + "\n"}
	case "rm":
		// By name, or by the ID that create returned.
		name = strings.TrimPrefix(name, "id-")
		if _, ok := d.networks[name]; !ok {
			return d.notFound(testutil.NoSuchNetwork(name))
		}
		for cname, c := range d.containers {
			if slices.Contains(c.networks, name) {
				return mock.Result{Stderr: "Error response from daemon: error while removing network: network " + name + " has active endpoints (name:\"" + cname + "\")\n", Err: d.exit1}
			}
		}
		delete(d.networks, name)
		return mock.Result{}
	case "connect", "disconnect":
		net, cname := args[len(args)-2], name
		c, ok := d.containers[cname]
		if _, netOK := d.networks[net]; !netOK {
			return d.notFound(testutil.NoSuchNetwork(net))
		}
		if !ok {
			return d.notFound(testutil.NoSuchContainer(cname))
		}
		c.networks = slices.DeleteFunc(c.networks, func(n string) bool { return n == net })
		if args[1] == "connect" {
			c.networks = append(c.networks, net)
		}
		return mock.Result{}
	case "inspect":
		return d.networkInspect(args)
	}
	return d.unexpected(args)
}

// subnet returns the subnet of a network: its place in the sorted names
// tells its second octet.
func (d *fakeDaemon) subnet(name string) int {
	return 20 + slices.Index(slices.Sorted(maps.Keys(d.networks)), name)
}

func (d *fakeDaemon) networkInspect(args []string) mock.Result {
	format, _ := testutil.ArgValue(args, "--format")
	var names []string
	for i := 2; i < len(args) && args[i] != "--format"; i++ {
		names = append(names, args[i])
	}
	var found []map[string]any
	var stderr strings.Builder
	for _, name := range names {
		labels, ok := d.networks[name]
		if !ok {
			stderr.WriteString(testutil.NoSuchNetwork(name))
			continue
		}
		n := d.subnet(name)
		found = append(found, map[string]any{
			"Id": "id-" + name, "Name": name, "Driver": "bridge", "Created": "2026-01-01T00:00:00Z", "Labels": labels,
			"IPAM": map[string]any{"Config": []map[string]string{{"Subnet": fmt.Sprintf("10.%d.0.0/16", n), "Gateway": fmt.Sprintf("10.%d.0.1", n)}}},
		})
	}
	res := mock.Result{Stderr: stderr.String()}
	if stderr.Len() > 0 {
		res.Err = d.exit1
	}
	var out any = found
	switch {
	case format == "" && len(found) == 0:
		res.Stdout = "[]\n"
		return res
	case format == "":
	case len(found) == 0:
		return res
	case format == "{{json .Labels}}":
		out = found[0]["Labels"]
	default:
		out = found[0]
	}
	data, _ := json.Marshal(out)
	res.Stdout = string(data) + "\n"
	return res
}

func (d *fakeDaemon) volume(args []string) mock.Result {
	name := args[len(args)-1]
	switch args[1] {
	case "ls":
		return ls(d.volumes, args)
	case "create":
		if _, ok := d.volumes[name]; !ok {
			d.volumes[name] = labelFlags(args)
		}
		return mock.Result{Stdout: name + "\n"}
	case "rm":
		if _, ok := d.volumes[name]; !ok {
			return d.notFound(testutil.NoSuchVolume(name))
		}
		delete(d.volumes, name)
		return mock.Result{}
	case "inspect":
		labels, ok := d.volumes[args[2]]
		if !ok {
			return d.notFound(testutil.NoSuchVolume(args[2]))
		}
		data, _ := json.Marshal(labels)
		return mock.Result{Stdout: string(data) + "\n"}
	}
	return d.unexpected(args)
}

// create answers docker create and docker run: a container that is gone
// once it ran (--rm) is the node's business.
func (d *fakeDaemon) create(args []string, stdin string) mock.Result {
	if slices.Contains(args, "--rm") {
		return d.node(args, stdin)
	}
	name, ok := testutil.ArgValue(args, "--name")
	if !ok {
		return d.unexpected(args)
	}
	if _, exists := d.containers[name]; exists {
		return mock.Result{Stderr: "Error response from daemon: Conflict. The container name \"/" + name + "\" is already in use\n", Err: d.exit1}
	}
	c := &fakeContainer{labels: labelFlags(args), running: args[0] == "run"}
	for _, n := range testutil.ArgValues(args, "--network") {
		for field := range strings.SplitSeq(n, ",") {
			if net, named := strings.CutPrefix(field, "name="); named {
				n = net
			}
		}
		if n, _, _ = strings.Cut(n, ","); n == "none" {
			continue
		}
		if _, ok := d.networks[n]; !ok {
			return d.notFound(testutil.NoSuchNetwork(n))
		}
		c.networks = append(c.networks, n)
	}
	// A named volume that does not exist is created with the container.
	for _, flag := range []string{"-v", "--volume"} {
		for _, v := range testutil.ArgValues(args, flag) {
			if src, _, _ := strings.Cut(v, ":"); !strings.HasPrefix(src, "/") {
				if _, ok := d.volumes[src]; !ok {
					d.volumes[src] = docker.Labels{}
				}
			}
		}
	}
	d.containers[name] = c
	return mock.Result{Stdout: "id-" + name + "\n"}
}

func (d *fakeDaemon) ps(args []string) mock.Result {
	f := filtersOf(args)
	var entries []testutil.PsEntry
	for _, name := range slices.Sorted(maps.Keys(d.containers)) {
		c := d.containers[name]
		if !hasLabels(c.labels, f["label"]) {
			continue
		}
		if !anyOf(c.networks, f["network"]) {
			continue
		}
		if names := f["name"]; len(names) > 0 && !slices.ContainsFunc(names, func(n string) bool { return strings.Contains(name, n) }) {
			continue
		}
		state := "exited"
		if c.running {
			state = "running"
		}
		entries = append(entries, testutil.PsEntry{ID: "id-" + name, Names: name, State: state, Image: "img:1",
			Labels: joinLabels(c.labels), Networks: strings.Join(c.networks, ",")})
	}
	if len(entries) == 0 {
		return mock.Result{}
	}
	return mock.Result{Stdout: testutil.NDJSON(entries...)}
}

func (d *fakeDaemon) inspect(args []string) mock.Result {
	var outputs []string
	var stderr strings.Builder
	for _, name := range args[1:] {
		if strings.HasPrefix(name, "--") {
			break
		}
		c, ok := d.containers[name]
		if !ok {
			stderr.WriteString(testutil.NoSuchContainer(name))
			continue
		}
		// The address of a container follows from its name, so that it
		// is the same in every run.
		h := fnv.New32a()
		h.Write([]byte(name))
		sum := h.Sum32()
		nets := map[docker.NetworkName]string{}
		for _, n := range c.networks {
			nets[docker.NetworkName(n)] = fmt.Sprintf("10.%d.%d.%d", d.subnet(n), sum>>8&0x7f, sum&0xff)
		}
		status := "exited"
		if c.running {
			status = "running"
		}
		outputs = append(outputs, nodeInspectJSON(d.t, name, nodeInspect{Status: status, Image: "sha256:local", Labels: c.labels, Networks: nets}))
	}
	res := mock.Result{Stdout: inspectBatch(outputs...), Stderr: stderr.String()}
	if stderr.Len() > 0 {
		res.Err = d.exit1
	}
	return res
}

// cp answers docker cp: files that sind copies into a container are kept
// and copied out again.
func (d *fakeDaemon) cp(args []string, stdin string) mock.Result {
	if args[1] == "-" {
		name, dir, _ := strings.Cut(args[2], ":")
		if _, ok := d.containers[name]; !ok {
			return d.notFound(testutil.NoSuchContainer(name))
		}
		for tr := tar.NewReader(strings.NewReader(stdin)); ; {
			h, err := tr.Next()
			if err != nil {
				break
			}
			if h.Typeflag == tar.TypeReg {
				content, _ := io.ReadAll(tr)
				d.files[name+":"+path.Join(dir, h.Name)] = string(content)
			}
		}
		return mock.Result{}
	}
	name, _, _ := strings.Cut(args[1], ":")
	if _, ok := d.containers[name]; !ok {
		return d.notFound(testutil.NoSuchContainer(name))
	}
	content, ok := d.files[args[1]]
	if !ok {
		return mock.Result{Stderr: "Error response from daemon: Could not find the file " + args[1] + "\n", Err: d.exit1}
	}
	return mock.Result{Stdout: testutil.TarArchive(path.Base(args[1]), content)}
}

// exec answers docker exec in a container that runs: the files that sind
// writes with ReplaceFile and WriteFile are kept, and cat reads them; the
// rest is the node's business.
func (d *fakeDaemon) exec(args []string, stdin string) mock.Result {
	rest := args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return d.unexpected(args)
	}
	name, cmd := rest[0], rest[1:]
	c, ok := d.containers[name]
	if !ok {
		return d.notFound(testutil.NoSuchContainer(name))
	}
	if !c.running {
		return mock.Result{Stderr: "Error response from daemon: container id-" + name + " is not running\n", Err: d.exit1}
	}
	switch {
	case len(cmd) == 2 && cmd[0] == "cat":
		if content, ok := d.files[name+":"+cmd[1]]; ok {
			return mock.Result{Stdout: content}
		}
	case len(cmd) >= 5 && cmd[0] == "sh" && cmd[1] == "-c" && strings.HasPrefix(cmd[2], `tmp="$1.tmp.$$"; cat > "$tmp"`),
		len(cmd) == 5 && cmd[0] == "sh" && cmd[2] == `cat > "$1"`:
		d.files[name+":"+cmd[4]] = stdin
		return mock.Result{}
	}
	return d.node(args, stdin)
}
