// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// stateContainer is a container of realmState.
type stateContainer struct {
	labels   docker.Labels
	networks []string // networks it joins, from --network and docker network connect
	volumes  []string // named volumes it mounts
	state    docker.ContainerState
}

// realmState is a stateful layer over happyOnCall, for the tests of what a
// failed Create leaves behind. It records the containers, networks and
// volumes that docker create, docker run -d, docker network create and
// docker volume create make, and their removal. It answers docker ps,
// docker volume ls and the label lookup of a network from that record, as
// the rollback finds what to remove with them, and refuses to remove a
// network or volume still in use, as Docker does. Other calls go on to
// happyOnCall.
type realmState struct {
	t          *testing.T
	mu         sync.Mutex
	containers map[string]*stateContainer
	networks   map[string]docker.Labels
	volumes    map[string]docker.Labels
	made       []string // "kind name" of every resource made, in order
}

// newRealmState returns an empty realmState.
func newRealmState(t *testing.T) *realmState {
	return &realmState{
		t:          t,
		containers: map[string]*stateContainer{},
		networks:   map[string]docker.Labels{},
		volumes:    map[string]docker.Labels{},
	}
}

// override returns a happyOnCall override that answers from the state,
// after next, which may be nil, has had its turn: a call that next fails
// changes nothing.
func (s *realmState) override(next func([]string, string) (mock.Result, bool)) func([]string, string) (mock.Result, bool) {
	return func(args []string, stdin string) (mock.Result, bool) {
		if next != nil {
			if r, ok := next(args, stdin); ok {
				return r, true
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.answer(args)
	}
}

func (s *realmState) answer(args []string) (mock.Result, bool) {
	last := args[len(args)-1]
	switch {
	case args[0] == "create":
		return s.create(args[1:], docker.StateCreated), true
	case len(args) > 1 && args[0] == "run" && args[1] == "-d":
		return s.create(args[2:], docker.StateRunning), true
	case args[0] == "start" && s.containers[last] != nil:
		s.containers[last].state = docker.StateRunning
		return mock.Result{Stdout: last + "\n"}, true
	case args[0] == "rm" && s.containers[last] != nil:
		delete(s.containers, last)
		return mock.Result{Stdout: last + "\n"}, true
	case args[0] == "logs":
		return mock.Result{}, true
	case args[0] == "ps":
		return s.ps(args), true
	case len(args) > 1 && args[0] == "network":
		return s.network(args)
	case len(args) > 1 && args[0] == "volume":
		return s.volume(args)
	}
	return mock.Result{}, false
}

// create records a container made with args, the flags of docker create.
func (s *realmState) create(args []string, state docker.ContainerState) mock.Result {
	c := &stateContainer{labels: docker.Labels{}, state: state}
	var name string
	for i := 0; i < len(args)-1; i++ {
		switch value := args[i+1]; args[i] {
		case "--name":
			name = value
		case "--label":
			k, v, _ := strings.Cut(value, "=")
			c.labels[k] = v
		case "--network":
			// name=NET,gw-priority=1 or NET
			network, _, _ := strings.Cut(strings.TrimPrefix(value, "name="), ",")
			c.networks = append(c.networks, network)
		case "-v":
			if source, _, _ := strings.Cut(value, ":"); !strings.HasPrefix(source, "/") {
				c.volumes = append(c.volumes, source)
			}
		case "--mount":
			if strings.Contains(value, "type=volume") {
				for field := range strings.SplitSeq(value, ",") {
					if source, ok := strings.CutPrefix(field, "source="); ok {
						c.volumes = append(c.volumes, source)
					}
				}
			}
		default:
			continue
		}
		i++
	}
	if _, ok := s.containers[name]; ok {
		return mock.Result{Stderr: "Conflict. The container name \"/" + name + "\" is already in use\n", Err: testutil.ExitCode1(s.t)}
	}
	s.containers[name] = c
	s.made = append(s.made, "container "+name)
	return mock.Result{Stdout: "id-" + name + "\n"}
}

// filtersOf returns the --filter values of args by key: label, network,
// volume.
func filtersOf(args []string) map[string][]string {
	filters := map[string][]string{}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--filter" {
			k, v, _ := strings.Cut(args[i+1], "=")
			filters[k] = append(filters[k], v)
		}
	}
	return filters
}

// hasLabels reports whether labels match every label filter, KEY or
// KEY=VALUE, as Docker ANDs them.
func hasLabels(labels docker.Labels, filters []string) bool {
	for _, f := range filters {
		k, v, withValue := strings.Cut(f, "=")
		got, ok := labels[k]
		if !ok || withValue && got != v {
			return false
		}
	}
	return true
}

// anyOf reports whether have holds one of want, as Docker ORs the values
// of the other filters; no filter matches everything.
func anyOf(have, want []string) bool {
	return len(want) == 0 || slices.ContainsFunc(want, func(w string) bool { return slices.Contains(have, w) })
}

// ps answers docker ps with the containers that match its filters.
func (s *realmState) ps(args []string) mock.Result {
	filters := filtersOf(args)
	for k := range filters {
		if k != "label" && k != "network" && k != "volume" {
			s.t.Errorf("realmState: unsupported ps filter %s", k)
		}
	}
	var entries []testutil.PsEntry
	for _, name := range slices.Sorted(maps.Keys(s.containers)) {
		c := s.containers[name]
		if hasLabels(c.labels, filters["label"]) && anyOf(c.networks, filters["network"]) && anyOf(c.volumes, filters["volume"]) {
			entries = append(entries, testutil.PsEntry{
				ID: "id-" + name, Names: name, State: string(c.state), Image: "img:1",
				Labels: joinLabels(c.labels), Networks: strings.Join(c.networks, ","),
			})
		}
	}
	return mock.Result{Stdout: testutil.NDJSON(entries...)}
}

// joinLabels renders labels as docker ps and docker volume ls do.
func joinLabels(labels docker.Labels) string {
	var pairs []string
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		pairs = append(pairs, k+"="+labels[k])
	}
	return strings.Join(pairs, ",")
}

// labelFlags returns the labels of the --label flags in args.
func labelFlags(args []string) docker.Labels {
	labels := docker.Labels{}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--label" {
			k, v, _ := strings.Cut(args[i+1], "=")
			labels[k] = v
		}
	}
	return labels
}

// network answers the docker network calls on networks of the state, and
// its containers' docker network connect and disconnect.
func (s *realmState) network(args []string) (mock.Result, bool) {
	last := args[len(args)-1]
	switch args[1] {
	case "create":
		s.networks[last] = labelFlags(args)
		s.made = append(s.made, "network "+last)
		return mock.Result{Stdout: "net-" + last + "\n"}, true
	case "inspect":
		labels, ok := s.networks[args[2]]
		if ok && len(args) == 5 && args[4] == "{{json .Labels}}" {
			out, _ := json.Marshal(labels)
			return mock.Result{Stdout: string(out) + "\n"}, true
		}
	case "rm":
		if _, ok := s.networks[last]; ok {
			for name, c := range s.containers {
				if slices.Contains(c.networks, last) {
					return mock.Result{Stderr: "Error response from daemon: error while removing network: network " + last +
						" has active endpoints (name:\"" + name + "\")\n", Err: testutil.ExitCode1(s.t)}, true
				}
			}
			delete(s.networks, last)
			return mock.Result{}, true
		}
	case "connect", "disconnect":
		network := args[len(args)-2]
		if _, ok := s.networks[network]; !ok {
			return mock.Result{}, false
		}
		// The SSH relay is a container of the mesh, which the state
		// does not hold until it joins a network of the state.
		c := s.containers[last]
		if c == nil {
			c = &stateContainer{labels: docker.Labels{}, state: docker.StateRunning}
			s.containers[last] = c
		}
		if args[1] == "connect" {
			c.networks = append(c.networks, network)
		} else {
			c.networks = slices.DeleteFunc(c.networks, func(n string) bool { return n == network })
		}
		return mock.Result{}, true
	}
	return mock.Result{}, false
}

// volume answers docker volume ls, and the docker volume calls on volumes
// of the state.
func (s *realmState) volume(args []string) (mock.Result, bool) {
	last := args[len(args)-1]
	switch args[1] {
	case "create":
		s.volumes[last] = labelFlags(args)
		s.made = append(s.made, "volume "+last)
		return mock.Result{Stdout: last + "\n"}, true
	case "ls":
		filters := filtersOf(args)
		var out strings.Builder
		for _, name := range slices.Sorted(maps.Keys(s.volumes)) {
			named := len(filters["name"]) == 0 || slices.ContainsFunc(filters["name"], func(f string) bool { return strings.Contains(name, f) })
			if named && hasLabels(s.volumes[name], filters["label"]) {
				line, _ := json.Marshal(map[string]string{"Name": name, "Driver": "local", "Labels": joinLabels(s.volumes[name])})
				out.Write(line)
				out.WriteString("\n")
			}
		}
		return mock.Result{Stdout: out.String()}, true
	case "rm":
		if _, ok := s.volumes[last]; ok {
			for name, c := range s.containers {
				if slices.Contains(c.volumes, last) {
					return mock.Result{Stderr: "Error response from daemon: remove " + last + ": volume is in use - [" + name + "]\n", Err: testutil.ExitCode1(s.t)}, true
				}
			}
			delete(s.volumes, last)
			return mock.Result{}, true
		}
	}
	return mock.Result{}, false
}

// of returns the resources of cluster in realm that the state holds: its
// containers by their labels, the containers attached to its network, its
// network and its volumes by their names.
func (s *realmState) of(realm, cluster string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	network := string(NetworkName(realm, cluster))
	var left []string
	for _, name := range slices.Sorted(maps.Keys(s.containers)) {
		c := s.containers[name]
		if c.labels[LabelRealm] == realm && c.labels[LabelCluster] == cluster || slices.Contains(c.networks, network) {
			left = append(left, "container "+name)
		}
	}
	if _, ok := s.networks[network]; ok {
		left = append(left, "network "+network)
	}
	for _, name := range slices.Sorted(maps.Keys(s.volumes)) {
		if strings.HasPrefix(name, realm+"-"+cluster+"-") {
			left = append(left, "volume "+name)
		}
	}
	return left
}
