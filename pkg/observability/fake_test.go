// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/require"
)

// fakeContainer is a container of fakeDocker.
type fakeContainer struct {
	args   []string // the docker create arguments
	labels map[string]string
	state  docker.ContainerState
	ip     string
	files  map[string]string // by absolute path
}

// fakeDocker is a stateful stand-in for the docker CLI: containers,
// volumes, the files copied into containers, and the curl and sh calls
// run in them. Calls whose joined arguments start with a key of fail get
// that result; curl calls whose URL is in down fail.
type fakeDocker struct {
	t          *testing.T
	mu         sync.Mutex
	containers map[string]*fakeContainer
	volumes    map[string]map[string]string
	fail       map[string]mock.Result
	down       map[string]bool
	execs      [][]string
}

// newFakeDocker returns an empty fakeDocker and a client that talks to it.
func newFakeDocker(t *testing.T) (*fakeDocker, *docker.Client) {
	f := &fakeDocker{
		t:          t,
		containers: map[string]*fakeContainer{},
		volumes:    map[string]map[string]string{},
		fail:       map[string]mock.Result{},
		down:       map[string]bool{},
	}
	m := &mock.Executor{OnCall: f.onCall}
	return f, docker.NewClient(m)
}

// exitErr is the error of a docker call that exits with 1.
func (f *fakeDocker) exitErr() error {
	return testutil.ExitCode1(f.t)
}

func (f *fakeDocker) onCall(args []string, stdin string) mock.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	joined := strings.Join(args, " ")
	for prefix, r := range f.fail {
		if strings.HasPrefix(joined, prefix) {
			return r
		}
	}
	switch args[0] {
	case "inspect":
		var out []map[string]any
		for _, name := range args[1:] {
			c, ok := f.containers[name]
			if !ok {
				return mock.Result{Stderr: testutil.NoSuchContainer(name), Err: f.exitErr()}
			}
			networks := map[string]any{}
			if c.state == docker.StateRunning {
				networks["sind-mesh"] = map[string]string{"IPAddress": c.ip}
			}
			image, _ := lastImage(c.args)
			out = append(out, map[string]any{
				"Id": "id-" + name, "Name": "/" + name,
				"State":           map[string]any{"Status": c.state},
				"Config":          map[string]any{"Image": image, "Labels": c.labels},
				"NetworkSettings": map[string]any{"Networks": networks},
			})
		}
		b, _ := json.Marshal(out)
		return mock.Result{Stdout: string(b)}
	case "create":
		name, _ := testutil.ArgValue(args, "--name")
		labels := map[string]string{}
		for _, l := range testutil.ArgValues(args, "--label") {
			k, v, _ := strings.Cut(l, "=")
			labels[k] = v
		}
		ip, _ := testutil.ArgValue(args, "--ip")
		if ip == "" {
			ip = "172.30.0.50"
		}
		f.containers[name] = &fakeContainer{args: args[1:], labels: labels, state: docker.StateCreated, ip: ip, files: map[string]string{}}
		return mock.Result{Stdout: "id-" + name + "\n"}
	case "start":
		f.containers[args[1]].state = docker.StateRunning
		return mock.Result{}
	case "rm":
		name := args[len(args)-1]
		if _, ok := f.containers[name]; !ok {
			return mock.Result{Stderr: testutil.NoSuchContainer(name), Err: f.exitErr()}
		}
		delete(f.containers, name)
		return mock.Result{}
	case "cp":
		name, dir, _ := strings.Cut(args[2], ":")
		tr := tar.NewReader(strings.NewReader(stdin))
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(f.t, err)
			if h.Typeflag == tar.TypeDir {
				continue
			}
			b, err := io.ReadAll(tr)
			require.NoError(f.t, err)
			f.containers[name].files[path.Join(dir, h.Name)] = string(b)
		}
		return mock.Result{}
	case "exec":
		c, ok := f.containers[args[1]]
		if !ok || c.state != docker.StateRunning {
			return mock.Result{Stderr: "Error response from daemon: container " + args[1] + " is not running\n", Err: f.exitErr()}
		}
		f.execs = append(f.execs, args[1:])
		if args[2] == "curl" && f.down[args[len(args)-1]] {
			return mock.Result{Stderr: "curl: (7) Failed to connect\n", Err: f.exitErr()}
		}
		return mock.Result{}
	case "ps":
		var out []string
		for name, c := range f.containers {
			if matchesFilters(args, c.labels) {
				b, _ := json.Marshal(map[string]string{"ID": "id-" + name, "Names": name, "State": string(c.state), "Labels": joinLabels(c.labels)})
				out = append(out, string(b))
			}
		}
		return mock.Result{Stdout: strings.Join(out, "\n")}
	case "volume":
		name := args[len(args)-1]
		switch args[1] {
		case "ls":
			var out []string
			for name, labels := range f.volumes {
				if matchesFilters(args, labels) {
					b, _ := json.Marshal(map[string]string{"Name": name, "Labels": joinLabels(labels)})
					out = append(out, string(b))
				}
			}
			return mock.Result{Stdout: strings.Join(out, "\n")}
		case "create":
			labels := map[string]string{}
			for _, l := range testutil.ArgValues(args, "--label") {
				k, v, _ := strings.Cut(l, "=")
				labels[k] = v
			}
			f.volumes[name] = labels
		case "rm":
			if _, ok := f.volumes[name]; !ok {
				return mock.Result{Stderr: testutil.NoSuchVolume(name), Err: f.exitErr()}
			}
			delete(f.volumes, name)
		}
		return mock.Result{}
	}
	f.t.Fatalf("fake docker: unexpected call %v", args)
	return mock.Result{}
}

// lastImage returns the image of docker create arguments: the first
// argument after the flags that names a stack image.
func lastImage(args []string) (string, bool) {
	for _, a := range args {
		for _, img := range Images() {
			if a == img {
				return a, true
			}
		}
	}
	return "", false
}

// curls returns the URLs curl was run on, in order.
func (f *fakeDocker) curls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var urls []string
	for _, e := range f.execs {
		if e[1] == "curl" {
			urls = append(urls, e[len(e)-1])
		}
	}
	return urls
}

// matchesFilters reports whether labels match every label filter of a
// docker ps or volume ls call: "label=key" or "label=key=value".
func matchesFilters(args []string, labels map[string]string) bool {
	for _, filter := range testutil.ArgValues(args, "--filter") {
		spec, ok := strings.CutPrefix(filter, "label=")
		if !ok {
			continue
		}
		key, value, hasValue := strings.Cut(spec, "=")
		got, present := labels[key]
		if !present || hasValue && got != value {
			return false
		}
	}
	return true
}

// joinLabels joins labels as docker ps prints them.
func joinLabels(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}
