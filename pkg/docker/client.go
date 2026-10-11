// SPDX-License-Identifier: LGPL-3.0-or-later

// Package docker provides a thin abstraction over the Docker CLI.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// ContainerID is a Docker container identifier (64 hex characters).
type ContainerID string

// ContainerName is a human-readable Docker container name.
type ContainerName string

// NetworkID is a Docker network identifier.
type NetworkID string

// NetworkName is a human-readable Docker network name.
type NetworkName string

// VolumeName is a human-readable Docker volume name.
type VolumeName string

// Labels is a set of key-value metadata pairs applied to Docker resources.
type Labels map[string]string

// MaxConcurrentCalls is the number of docker commands a Client from
// NewClient runs at once; further calls wait for one to finish. Creating a
// cluster probes every node in its own goroutine, so without a bound a
// large cluster forks hundreds of docker processes that compete with the
// booting nodes for the daemon. Long-lived streams (Executor.Start, such as
// docker events) do not count. No call waits for another while it runs, so
// the bound cannot deadlock.
const MaxConcurrentCalls = 16

// Client provides operations against the Docker CLI.
type Client struct {
	Executor cmdexec.Executor
	Command  string // docker executable path (default: "docker")

	// slots bounds the docker commands that run at once (see
	// MaxConcurrentCalls). A Client not made by NewClient has none and
	// runs them unbounded.
	slots chan struct{}
}

// NewClient returns a Client that runs docker commands through the given
// executor, at most MaxConcurrentCalls at once.
func NewClient(executor cmdexec.Executor) *Client {
	return &Client{Executor: executor, Command: "docker", slots: make(chan struct{}, MaxConcurrentCalls)}
}

// acquire takes a slot for a docker command and returns the function that
// gives it back. It waits while all slots are taken, until one is free or
// ctx ends. A free slot is taken even when ctx has ended, so that a
// command run with an ended context fails as the executor reports it.
func (c *Client) acquire(ctx context.Context) (release func(), err error) {
	if c.slots == nil {
		return func() {}, nil
	}
	release = func() { <-c.slots }
	select {
	case c.slots <- struct{}{}:
		return release, nil
	default:
	}
	select {
	case c.slots <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) run(ctx context.Context, args ...string) (string, string, error) {
	sindlog.From(ctx).Log(ctx, sindlog.LevelTrace, "docker", "cmd", strings.Join(args, " "))
	return c.call(ctx, args, func(ctx context.Context) (string, string, error) {
		return c.Executor.Run(ctx, c.Command, args...)
	})
}

func (c *Client) runWithStdin(ctx context.Context, stdin io.Reader, args ...string) (string, string, error) {
	return c.call(ctx, args, func(ctx context.Context) (string, string, error) {
		return c.Executor.RunWithStdin(ctx, stdin, c.Command, args...)
	})
}

// call runs the docker command args with exec once it has a slot, and
// reports it as a hidden progress call named after it (see callName),
// which a display draws only when it takes a second or longer: queued
// while it waits for the slot, running from when it has one, and ended
// with the command's exit code when docker exited with an error. Lines of
// the command's output reach the display through the executor (see
// cmdexec.OSExecutor), as the context passed to exec carries the call:
// those of its standard error, and of its standard output only for docker
// pull, which writes its status there. Every other command writes data
// there, a container's ID, a name or the JSON of inspect, which is no line
// for a row that shows what a pull does (cmdexec.WithStdoutAsData).
func (c *Client) call(ctx context.Context, args []string, exec func(context.Context) (string, string, error)) (string, string, error) {
	name := callName(args)
	ctx, span := progress.Start(ctx, progress.KindCall, name, progress.Queued(), progress.WithFlags(progress.Hidden))
	release, err := c.acquire(ctx)
	if err != nil {
		span.End(err)
		return "", "", err
	}
	defer release()
	span.Run()
	runCtx := ctx
	if name != "docker pull" && name != "docker image pull" {
		runCtx = cmdexec.WithStdoutAsData(ctx)
	}
	stdout, stderr, err := exec(runCtx)
	var end []progress.Option
	if exitErr, ok := errors.AsType[*cmdexec.ExitError](err); ok {
		end = append(end, progress.Exit(exitErr.ExitCode()))
	}
	span.End(err, end...)
	return stdout, stderr, err
}

// callName names the progress call of the docker command args by what it
// does, never by what it does it to: docker and the subcommand, with the
// verb of a management command, as "docker network create"; no container
// name, path or script.
func callName(args []string) string {
	if len(args) == 0 {
		return "docker"
	}
	name := "docker " + args[0]
	if managementCommands[args[0]] && len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		name += " " + args[1]
	}
	return name
}

// managementCommands are the docker subcommands whose next word is the
// verb, as in docker network create.
var managementCommands = map[string]bool{
	"container": true,
	"context":   true,
	"image":     true,
	"network":   true,
	"plugin":    true,
	"system":    true,
	"volume":    true,
}

// SortedLabelFlags returns --label k=v flag pairs in sorted key order.
// Returns nil when labels is nil or empty.
func SortedLabelFlags(labels Labels) []string {
	if len(labels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(labels)*2)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	return args
}

// exists runs an inspect-style command and returns true if the resource exists.
// Docker inspect commands exit with code 1 for missing resources; other errors
// are propagated.
// labels runs an inspect command that prints a resource's labels as JSON
// and reports them, and whether the resource exists.
func (c *Client) labels(ctx context.Context, args ...string) (Labels, bool, error) {
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		if IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var labels Labels
	if out := strings.TrimSpace(stdout); out != "" {
		if err := json.Unmarshal([]byte(out), &labels); err != nil {
			return nil, false, fmt.Errorf("parsing labels: %w", err)
		}
	}
	return labels, true, nil
}

func (c *Client) exists(ctx context.Context, args ...string) (bool, error) {
	_, _, err := c.run(ctx, args...)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsNotFound reports whether err is a docker CLI "resource not found" error
// rather than a genuine failure. Callers that want the info from a single
// inspect or rm call can use this to distinguish missing resources without
// issuing a second existence probe.
//
// Docker exits 1 for many failures, so the exit code alone is not enough: a
// volume that is still in use exits 1 too, and so does the CLI when it
// cannot reach the daemon ("... connect: no such file or directory"). The
// error has to be an *cmdexec.ExitError whose stderr names a missing
// resource in one of the forms the docker CLI and daemon use:
// "No such container: x", "No such image: x", "error: no such object: x",
// "get x: no such volume", "No such network: x" or "network x not found".
func IsNotFound(err error) bool {
	exitErr, ok := errors.AsType[*cmdexec.ExitError](err)
	if !ok || exitErr.ExitCode() != 1 {
		return false
	}
	msg := strings.ToLower(exitErr.Stderr)
	for _, form := range notFoundForms {
		if strings.Contains(msg, form) {
			return true
		}
	}
	return networkNotFound.MatchString(msg)
}

// IsAlreadyExists reports whether err is docker's error for a network name
// that another network has: docker network create exits 1 with "network
// with name x already exists". Like IsNotFound, it needs the
// *cmdexec.ExitError and its stderr, so that a daemon that cannot be
// reached does not count.
func IsAlreadyExists(err error) bool {
	exitErr, ok := errors.AsType[*cmdexec.ExitError](err)
	return ok && exitErr.ExitCode() == 1 && strings.Contains(strings.ToLower(exitErr.Stderr), "already exists")
}

// notFoundForms are docker's messages for a missing resource, lowercased.
var notFoundForms = []string{
	"no such container",
	"no such image",
	"no such object",
	"no such volume",
	"no such network",
}

// networkNotFound matches the daemon's message for a missing network.
var networkNotFound = regexp.MustCompile(`\bnetwork \S+ not found\b`)
