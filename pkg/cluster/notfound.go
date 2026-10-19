// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// clusterNotFound returns the error for a cluster that realm does not hold,
// the one every command reports for a cluster that does not exist. It names
// the clusters the realm does hold, so that a slip such as creating dev and
// entering the default cluster points at the right name, and the other
// realms that hold a cluster of that name: a realm set in a cluster's config
// file applies to `sind create cluster` alone, so the commands after it look
// in another realm unless they are given the same one. Failing to list
// either only drops its hint.
func clusterNotFound(ctx context.Context, client *docker.Client, realm, clusterName string) error {
	err := errorWith(ErrClusterNotFound, "cluster %q not found in realm %q", clusterName, realm)
	var hints []string
	if clusters, lerr := GetClusters(ctx, client, realm); lerr == nil && len(clusters) > 0 {
		names := make([]string, len(clusters))
		for i, c := range clusters {
			names[i] = c.Name
		}
		hints = append(hints, "existing clusters: "+strings.Join(names, ", "))
	}
	if others, lerr := OtherRealms(ctx, client, realm, clusterName); lerr == nil && len(others) > 0 {
		quoted := make([]string, len(others))
		for i, r := range others {
			quoted[i] = fmt.Sprintf("%q", r)
		}
		noun := "realm"
		if len(others) > 1 {
			noun = "realms"
		}
		hints = append(hints, fmt.Sprintf("it exists in %s %s", noun, strings.Join(quoted, ", ")))
	}
	if len(hints) == 0 {
		return err
	}
	return fmt.Errorf("%w (%s)", err, strings.Join(hints, "; "))
}

// checkClusterExists fails with clusterNotFound for a cluster that has no
// containers, the ones its caller listed, and no network. The network tells
// "no such cluster" from "partial teardown" (e.g. a failed delete that left
// the network behind): a cluster that was never created or is fully gone is
// a not-found error rather than a plausible-looking empty report.
func checkClusterExists(ctx context.Context, client *docker.Client, realm, clusterName string, containers []docker.ContainerListEntry) error {
	if len(containers) > 0 {
		return nil
	}
	if _, err := client.InspectNetwork(ctx, NetworkName(realm, clusterName)); err != nil {
		if docker.IsNotFound(err) {
			return clusterNotFound(ctx, client, realm, clusterName)
		}
		return fmt.Errorf("checking cluster network: %w", err)
	}
	return nil
}

// OtherRealms returns, sorted, the realms other than realm that hold a node
// container of a cluster named clusterName. Labels that are not valid realm
// names are left out.
func OtherRealms(ctx context.Context, client *docker.Client, realm, clusterName string) ([]string, error) {
	entries, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	var others []string
	for _, e := range entries {
		r := e.Labels[LabelRealm]
		if r == realm || slices.Contains(others, r) || config.CheckName("realm", r) != nil {
			continue
		}
		others = append(others, r)
	}
	slices.Sort(others)
	return others, nil
}
