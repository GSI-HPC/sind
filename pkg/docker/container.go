// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ContainerExists returns true if the given container exists (running or stopped).
func (c *Client) ContainerExists(ctx context.Context, name ContainerName) (bool, error) {
	return c.exists(ctx, "container", "inspect", string(name))
}

// CreateContainer creates a container without starting it (docker create).
// Use StartContainer to start it afterwards. Returns the container ID.
func (c *Client) CreateContainer(ctx context.Context, args ...string) (ContainerID, error) {
	createArgs := append([]string{"create"}, args...)
	stdout, _, err := c.run(ctx, createArgs...)
	if err != nil {
		return "", err
	}
	return ContainerID(strings.TrimSpace(stdout)), nil
}

// RunContainer creates and starts a container in detached mode (docker run -d).
// The args are passed directly and should include the image name as the last
// element. Returns the container ID.
func (c *Client) RunContainer(ctx context.Context, args ...string) (ContainerID, error) {
	runArgs := append([]string{"run", "-d"}, args...)
	stdout, _, err := c.run(ctx, runArgs...)
	if err != nil {
		return "", err
	}
	return ContainerID(strings.TrimSpace(stdout)), nil
}

// StartContainer starts a stopped container.
func (c *Client) StartContainer(ctx context.Context, name ContainerName) error {
	_, _, err := c.run(ctx, "start", string(name))
	return err
}

// StopContainer gracefully stops a running container.
func (c *Client) StopContainer(ctx context.Context, name ContainerName) error {
	_, _, err := c.run(ctx, "stop", string(name))
	return err
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, name ContainerName) error {
	_, _, err := c.run(ctx, "rm", string(name))
	return err
}

// SignalContainer sends a signal to a running container (docker kill -s).
func (c *Client) SignalContainer(ctx context.Context, name ContainerName, signal string) error {
	_, _, err := c.run(ctx, "kill", "-s", signal, string(name))
	return err
}

// ContainerInfo holds inspected container details.
type ContainerInfo struct {
	ID     ContainerID
	Name   ContainerName
	Status string
	Labels map[string]string
	IPs    map[NetworkName]string
}

// inspectResult maps the subset of docker inspect JSON we care about.
type inspectResult struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// InspectContainer returns detailed information about a container.
func (c *Client) InspectContainer(ctx context.Context, name ContainerName) (*ContainerInfo, error) {
	stdout, _, err := c.run(ctx, "inspect", string(name))
	if err != nil {
		return nil, err
	}
	var results []inspectResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		return nil, fmt.Errorf("parsing inspect output: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("inspect returned no results for %q", name)
	}
	r := results[0]
	ips := make(map[NetworkName]string, len(r.NetworkSettings.Networks))
	for net, info := range r.NetworkSettings.Networks {
		ips[NetworkName(net)] = info.IPAddress
	}
	return &ContainerInfo{
		ID:     ContainerID(r.ID),
		Name:   ContainerName(strings.TrimPrefix(r.Name, "/")),
		Status: r.State.Status,
		Labels: r.Config.Labels,
		IPs:    ips,
	}, nil
}

// ContainerListEntry holds summary information from docker ps.
type ContainerListEntry struct {
	ID     ContainerID
	Name   ContainerName
	State  string
	Image  string
	Labels map[string]string
}

// psEntry maps the docker ps --format json output.
type psEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Image  string `json:"Image"`
	Labels string `json:"Labels"`
}

// ListContainers returns containers matching the given filters.
// Each filter is passed as a --filter flag to docker ps (e.g. "label=sind.cluster").
func (c *Client) ListContainers(ctx context.Context, filters ...string) ([]ContainerListEntry, error) {
	args := []string{"ps", "-a", "--no-trunc", "--format", "json"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var entries []ContainerListEntry
	for _, line := range strings.Split(stdout, "\n") {
		var p psEntry
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, fmt.Errorf("parsing ps output: %w", err)
		}
		entries = append(entries, ContainerListEntry{
			ID:     ContainerID(p.ID),
			Name:   ContainerName(p.Names),
			State:  p.State,
			Image:  p.Image,
			Labels: parseLabels(p.Labels),
		})
	}
	return entries, nil
}

// parseLabels parses the comma-separated key=value label string from docker ps JSON output.
func parseLabels(s string) map[string]string {
	if s == "" {
		return nil
	}
	labels := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(pair, "=")
		labels[k] = v
	}
	return labels
}

// Exec runs a command inside a container and returns its stdout.
func (c *Client) Exec(ctx context.Context, container ContainerName, command ...string) (string, error) {
	args := append([]string{"exec", string(container)}, command...)
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return stdout, nil
}

// ExecWithStdin runs a command inside a container, piping stdin to it.
func (c *Client) ExecWithStdin(ctx context.Context, container ContainerName, stdin io.Reader, command ...string) error {
	args := append([]string{"exec", "-i", string(container)}, command...)
	_, _, err := c.runWithStdin(ctx, stdin, args...)
	return err
}

// ReadFile reads a file from a running container via docker exec.
func (c *Client) ReadFile(ctx context.Context, container ContainerName, path string) (string, error) {
	return c.Exec(ctx, container, "cat", path)
}

// WriteFile overwrites a file in a running container via docker exec.
func (c *Client) WriteFile(ctx context.Context, container ContainerName, path, content string) error {
	return c.ExecWithStdin(ctx, container, strings.NewReader(content),
		"sh", "-c", "cat > "+path)
}

// AppendFile appends content to a file in a running container via docker exec.
func (c *Client) AppendFile(ctx context.Context, container ContainerName, path, content string) error {
	return c.ExecWithStdin(ctx, container, strings.NewReader(content),
		"sh", "-c", "cat >> "+path)
}

// CopyToContainer writes files into a container directory via docker cp.
// Files are provided as a map of filename to content. The container may be
// running or stopped. Keys are sorted for deterministic tar output.
func (c *Client) CopyToContainer(ctx context.Context, container ContainerName, destDir string, files map[string][]byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		content := files[name]
		// WriteHeader and Write cannot fail on a bytes.Buffer.
		tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0644})
		tw.Write(content)
	}
	tw.Close()

	_, _, err := c.runWithStdin(ctx, &buf, "cp", "-", string(container)+":"+destDir)
	return err
}

// CopyFromContainer reads a single file from a container via docker cp.
// The container may be running or stopped.
func (c *Client) CopyFromContainer(ctx context.Context, container ContainerName, srcPath string) ([]byte, error) {
	stdout, _, err := c.run(ctx, "cp", string(container)+":"+srcPath, "-")
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(strings.NewReader(stdout))
	for {
		_, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("file not found in tar output for %s:%s", container, srcPath)
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar output: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("reading file content: %w", err)
		}
		return data, nil
	}
}
