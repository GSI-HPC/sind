// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxBridgeEndpoints is the number of containers a Docker bridge network
// holds at most: a Linux bridge has 1,024 ports (BR_MAX_PORTS), and the
// kernel reserves port 0. Docker fails to connect another container with
// "exchange full" (EXFULL).
const MaxBridgeEndpoints = 1023

// NetworkExists returns true if the given network exists.
func (c *Client) NetworkExists(ctx context.Context, name NetworkName) (bool, error) {
	return c.exists(ctx, "network", "inspect", string(name))
}

// NetworkLabels returns the labels of the given network, and whether it
// exists.
func (c *Client) NetworkLabels(ctx context.Context, name NetworkName) (Labels, bool, error) {
	return c.labels(ctx, "network", "inspect", string(name), "--format", "{{json .Labels}}")
}

// CreateNetwork creates a Docker network and returns its ID. Docker takes
// its subnet from the daemon's default address pools. Labels are applied as
// --label flags when non-nil.
func (c *Client) CreateNetwork(ctx context.Context, name NetworkName, labels Labels) (NetworkID, error) {
	return c.createNetwork(ctx, name, labels, nil)
}

// NetworkIPAM is a network's IPv4 subnet as the user configures it, rather
// than one Docker takes from its default address pools.
type NetworkIPAM struct {
	Subnet  string // CIDR, e.g. 172.18.0.0/16
	Gateway string // address in Subnet; empty lets Docker choose
	// IPRange is the CIDR within Subnet that Docker takes the addresses of
	// containers from that ask for none; empty for all of Subnet. A
	// container's --ip may lie outside it.
	IPRange string
}

// CreateNetworkWithSubnet creates a Docker network with the subnet that ipam
// configures and returns its ID. Docker accepts a container's --ip only on
// such a network. Labels are applied as --label flags when non-nil.
func (c *Client) CreateNetworkWithSubnet(ctx context.Context, name NetworkName, labels Labels, ipam NetworkIPAM) (NetworkID, error) {
	args := []string{"--subnet", ipam.Subnet}
	if ipam.Gateway != "" {
		args = append(args, "--gateway", ipam.Gateway)
	}
	if ipam.IPRange != "" {
		args = append(args, "--ip-range", ipam.IPRange)
	}
	return c.createNetwork(ctx, name, labels, args)
}

// ErrAddressPoolsExhausted reports that the Docker daemon has no subnet
// left in its default address pools for a new network (CreateNetwork).
var ErrAddressPoolsExhausted = errors.New("the Docker daemon's default address pools are used up")

// addressPoolsExhausted are the daemon's messages for default address pools
// without a free subnet, in current and in older Docker versions.
var addressPoolsExhausted = []string{
	"all predefined address pools have been fully subnetted",
	"could not find an available, non-overlapping IPv4 address pool",
}

// createNetwork runs docker network create with the given IPAM flags. When
// the default address pools are used up, the error wraps
// ErrAddressPoolsExhausted and says how to get more.
func (c *Client) createNetwork(ctx context.Context, name NetworkName, labels Labels, ipamArgs []string) (NetworkID, error) {
	args := []string{"network", "create"}
	args = append(args, ipamArgs...)
	args = append(args, SortedLabelFlags(labels)...)
	args = append(args, string(name))
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		for _, msg := range addressPoolsExhausted {
			if strings.Contains(err.Error(), msg) {
				return "", fmt.Errorf("%w: every network takes a subnet from them, and a stock daemon has about 30; "+
					"give the daemon more or smaller pools with default-address-pools in /etc/docker/daemon.json and restart it, "+
					"or remove networks no longer in use (%w)", ErrAddressPoolsExhausted, err)
			}
		}
		return "", err
	}
	return NetworkID(strings.TrimSpace(stdout)), nil
}

// RemoveNetwork removes a Docker network.
func (c *Client) RemoveNetwork(ctx context.Context, name NetworkName) error {
	_, _, err := c.run(ctx, "network", "rm", string(name))
	return err
}

// CreateConfigOnlyNetwork creates a configuration-only network (docker
// network create --config-only) and returns its ID. Such a network has no
// driver: Docker takes no subnet from its address pools for it, creates no
// bridge device, and attaches no container to it. Like every network create,
// it fails when another network has the name (see IsAlreadyExists).
func (c *Client) CreateConfigOnlyNetwork(ctx context.Context, name NetworkName, labels Labels) (NetworkID, error) {
	args := []string{"network", "create", "--config-only"}
	args = append(args, SortedLabelFlags(labels)...)
	args = append(args, string(name))
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return NetworkID(strings.TrimSpace(stdout)), nil
}

// RemoveNetworkByID removes the network with the given ID. Unlike a name,
// an ID never comes to stand for another network, so a caller that
// inspected a network removes that one and no other.
func (c *Client) RemoveNetworkByID(ctx context.Context, id NetworkID) error {
	_, _, err := c.run(ctx, "network", "rm", string(id))
	return err
}

// NetworkMeta is a network's ID, name, creation time and labels.
type NetworkMeta struct {
	ID         NetworkID
	Name       NetworkName
	Created    time.Time // by the daemon's clock
	ConfigOnly bool
	Labels     Labels
}

// InspectNetworkMeta returns the ID, creation time and labels of a network.
// The error for a missing network satisfies IsNotFound.
func (c *Client) InspectNetworkMeta(ctx context.Context, name NetworkName) (*NetworkMeta, error) {
	stdout, _, err := c.run(ctx, "network", "inspect", string(name), "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var r struct {
		ID         string    `json:"Id"`
		Name       string    `json:"Name"`
		Created    time.Time `json:"Created"`
		ConfigOnly bool      `json:"ConfigOnly"`
		Labels     Labels    `json:"Labels"`
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		return nil, fmt.Errorf("parsing network inspect output: %w", err)
	}
	return &NetworkMeta{
		ID:         NetworkID(r.ID),
		Name:       NetworkName(r.Name),
		Created:    r.Created,
		ConfigOnly: r.ConfigOnly,
		Labels:     r.Labels,
	}, nil
}

// ConnectNetwork connects a container to a network.
func (c *Client) ConnectNetwork(ctx context.Context, network NetworkName, container ContainerName) error {
	_, _, err := c.run(ctx, "network", "connect", string(network), string(container))
	return err
}

// DisconnectNetwork disconnects a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, network NetworkName, container ContainerName) error {
	_, _, err := c.run(ctx, "network", "disconnect", string(network), string(container))
	return err
}

// NetworkInfo holds detailed information about a Docker network. Subnet,
// Gateway and IPRange are those of its first IPAM configuration; IPRange is
// empty unless the user configured one.
type NetworkInfo struct {
	ID      string
	Name    NetworkName
	Driver  string
	Subnet  string
	Gateway string
	IPRange string
	Labels  Labels
}

// networkInspectResult maps the subset of docker network inspect JSON we need.
type networkInspectResult struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels"`
	IPAM   struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
			IPRange string `json:"IPRange"`
		} `json:"Config"`
	} `json:"IPAM"`
}

// InspectNetwork returns detailed information about a network.
func (c *Client) InspectNetwork(ctx context.Context, name NetworkName) (*NetworkInfo, error) {
	infos, err := c.InspectNetworks(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("network inspect returned no results for %q", name)
	}
	return infos[0], nil
}

// InspectNetworks returns details for the given networks in a single docker
// network inspect invocation. Returns nil when names is empty.
//
// When some of the networks cannot be inspected, e.g. one was removed after
// it was listed, docker exits non-zero but still prints the others.
// InspectNetworks then returns those together with the error, so callers
// that can do without the missing ones may use them.
func (c *Client) InspectNetworks(ctx context.Context, names ...NetworkName) ([]*NetworkInfo, error) {
	if len(names) == 0 {
		return nil, nil
	}
	args := make([]string, 0, 2+len(names))
	args = append(args, "network", "inspect")
	for _, n := range names {
		args = append(args, string(n))
	}
	stdout, _, runErr := c.run(ctx, args...)
	var results []networkInspectResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("parsing network inspect output: %w", err)
	}
	infos := make([]*NetworkInfo, 0, len(results))
	for _, r := range results {
		info := &NetworkInfo{ID: r.ID, Name: NetworkName(r.Name), Driver: r.Driver, Labels: r.Labels}
		if len(r.IPAM.Config) > 0 {
			info.Subnet = r.IPAM.Config[0].Subnet
			info.Gateway = r.IPAM.Config[0].Gateway
			info.IPRange = r.IPAM.Config[0].IPRange
		}
		infos = append(infos, info)
	}
	return infos, runErr
}

// NetworkListEntry holds summary information from docker network ls.
type NetworkListEntry struct {
	Name   NetworkName
	Driver string
	Labels Labels
}

// networkLsEntry maps the docker network ls --format json output.
type networkLsEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Labels string `json:"Labels"`
}

// ListNetworks returns networks matching the given filters.
// Each filter is passed as a --filter flag (e.g. "name=sind").
func (c *Client) ListNetworks(ctx context.Context, filters ...string) ([]NetworkListEntry, error) {
	args := []string{"network", "ls", "--format", "json"}
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
	var entries []NetworkListEntry
	for _, line := range strings.Split(stdout, "\n") {
		var n networkLsEntry
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			return nil, fmt.Errorf("parsing network ls output: %w", err)
		}
		entries = append(entries, NetworkListEntry{
			Name:   NetworkName(n.Name),
			Driver: n.Driver,
			Labels: parseLabels(n.Labels),
		})
	}
	return entries, nil
}
