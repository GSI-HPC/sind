// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Endpoint is the address at which the docker CLI reaches the daemon.
type Endpoint struct {
	// Host is the daemon's address, e.g. "unix:///var/run/docker.sock",
	// "tcp://build-host:2376" or "ssh://ci@build-host".
	Host string
	// Context is the docker context that Host comes from, empty when
	// DOCKER_HOST sets it.
	Context string
}

// UnixSocket reports whether the CLI reaches the daemon through a unix
// socket, as it does a daemon on its own machine.
func (e Endpoint) UnixSocket() bool {
	return strings.HasPrefix(e.Host, "unix://")
}

// Endpoint returns the address at which the docker CLI reaches the daemon,
// chosen as the CLI chooses it: DOCKER_HOST, else the docker endpoint of
// the context that DOCKER_CONTEXT names, else of the current context of the
// CLI's configuration (`docker context use`), else the default socket.
// Without DOCKER_HOST it runs `docker context inspect`, which resolves the
// context without contacting the daemon.
func (c *Client) Endpoint(ctx context.Context) (Endpoint, error) {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return Endpoint{Host: host}, nil
	}
	stdout, _, err := c.run(ctx, "context", "inspect", "--format", "{{json .}}")
	if err != nil {
		return Endpoint{}, fmt.Errorf("inspecting the docker context: %w", err)
	}
	var out struct {
		Name      string `json:"Name"`
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		return Endpoint{}, fmt.Errorf("parsing docker context inspect: %w", err)
	}
	host := out.Endpoints["docker"].Host
	if host == "" {
		return Endpoint{}, fmt.Errorf("docker context %q has no docker endpoint", out.Name)
	}
	return Endpoint{Host: host, Context: out.Name}, nil
}
