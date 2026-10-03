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

// clusterNotFound returns the error for a cluster that realm does not hold.
// It names the other realms that hold a cluster of that name: a realm set
// in a cluster's config file applies to `sind create cluster` alone, so the
// commands after it look in another realm unless they are given the same
// one. Failing to list them only drops the hint.
func clusterNotFound(ctx context.Context, client *docker.Client, realm, clusterName string) error {
	err := errorWith(ErrClusterNotFound, "cluster %q not found in realm %q", clusterName, realm)
	others, lerr := OtherRealms(ctx, client, realm, clusterName)
	if lerr != nil || len(others) == 0 {
		return err
	}
	quoted := make([]string, len(others))
	for i, r := range others {
		quoted[i] = fmt.Sprintf("%q", r)
	}
	noun := "realm"
	if len(others) > 1 {
		noun = "realms"
	}
	return fmt.Errorf("%w (it exists in %s %s)", err, noun, strings.Join(quoted, ", "))
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
