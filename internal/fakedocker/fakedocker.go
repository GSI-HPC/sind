// SPDX-License-Identifier: LGPL-3.0-or-later

// Package fakedocker holds a stand-in for dockerd that the unit tests of
// several packages share, as _test.go files cannot be imported. Daemon
// keeps state: what a command creates, a later one finds.
package fakedocker

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// Created is the creation time of every network of a Daemon.
var Created = time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC)

// Network is a network of a Daemon.
type Network struct {
	ID     string
	Labels docker.Labels
}

// Daemon answers docker network create, inspect and rm as dockerd and the
// docker CLI do, for the networks it keeps by name: a create refuses a
// name that is taken, and an rm takes the network's ID. These are the
// commands of the realm lock's daemon part. Its OnCall is a
// mock.Executor.OnCall, and it locks itself, so that the clients of
// several executors can share it.
type Daemon struct {
	exit1 error
	mu    sync.Mutex
	hook  func(args []string) *mock.Result
	nets  map[string]*Network
	ids   int
}

// New returns a Daemon without networks.
func New(t *testing.T) *Daemon {
	return &Daemon{exit1: testutil.ExitCode1(t), nets: map[string]*Network{}}
}

// Client returns a docker client of the daemon, with a mock.Executor of
// its own.
func (d *Daemon) Client() *docker.Client {
	return docker.NewClient(&mock.Executor{OnCall: d.OnCall})
}

// SetHook makes hook see each call first; a result it returns answers
// the call instead of the daemon. It runs outside the daemon's lock, so
// it may call the daemon's methods, but under the lock of the
// mock.Executor, so it must not run docker commands.
func (d *Daemon) SetHook(hook func(args []string) *mock.Result) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hook = hook
}

// Fail returns the result of a docker command that exits 1 with stderr.
func (d *Daemon) Fail(stderr string) *mock.Result {
	return &mock.Result{Stderr: stderr, Err: d.exit1}
}

// Add creates a network, as another client would, and returns its ID.
func (d *Daemon) Add(name string, labels docker.Labels) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.add(name, labels)
}

func (d *Daemon) add(name string, labels docker.Labels) string {
	d.ids++
	id := fmt.Sprintf("%064x", d.ids)
	d.nets[name] = &Network{ID: id, Labels: labels}
	return id
}

// Remove removes the network name, as another client would.
func (d *Daemon) Remove(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.nets, name)
}

// Network returns the network name, or nil.
func (d *Daemon) Network(name string) *Network {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nets[name]
}

// Networks returns the names of the networks, sorted.
func (d *Daemon) Networks() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Sorted(maps.Keys(d.nets))
}

// HasID reports whether a network has the ID id.
func (d *Daemon) HasID(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.byID(id) != ""
}

func (d *Daemon) byID(id string) string {
	for name, n := range d.nets {
		if n.ID == id {
			return name
		}
	}
	return ""
}

// OnCall answers a docker command, as a mock.Executor.OnCall.
func (d *Daemon) OnCall(args []string, _ string) mock.Result {
	d.mu.Lock()
	hook := d.hook
	d.mu.Unlock()
	if hook != nil {
		if r := hook(args); r != nil {
			return *r
		}
	}
	if len(args) < 3 || args[0] != "network" {
		return mock.Result{Err: fmt.Errorf("fake daemon: unexpected call %v", args)}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch args[1] {
	case "create":
		name := args[len(args)-1]
		if d.nets[name] != nil {
			return *d.Fail("Error response from daemon: network with name " + name + " already exists\n")
		}
		labels := docker.Labels{}
		for i := 2; i < len(args)-1; i++ {
			if args[i] == "--label" {
				k, v, _ := strings.Cut(args[i+1], "=")
				labels[k] = v
				i++
			}
		}
		return mock.Result{Stdout: d.add(name, labels) + "\n"}
	case "inspect":
		n := d.nets[args[2]]
		if n == nil {
			return *d.Fail(testutil.NoSuchNetwork(args[2]))
		}
		out, _ := json.Marshal(map[string]any{
			"Name": args[2], "Id": n.ID, "Created": Created, "ConfigOnly": true, "Labels": n.Labels,
		})
		return mock.Result{Stdout: string(out) + "\n"}
	case "rm":
		name := d.byID(args[2])
		if name == "" {
			return *d.Fail(testutil.NoSuchNetwork(args[2]))
		}
		delete(d.nets, name)
		return mock.Result{Stdout: args[2] + "\n"}
	}
	return mock.Result{Err: fmt.Errorf("fake daemon: unexpected call %v", args)}
}
