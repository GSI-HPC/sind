// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/pkg/config"
)

// ResolveCluster determines the cluster of a command whose command line
// names none, with the following precedence:
//
//	SIND_CLUSTER env var > config file > config.DefaultClusterName
//
// A cluster that the command line names, as an argument or as the cluster
// of a node argument, comes before all of them. configName is the name of a
// config file, "" when there is none: only `create cluster` reads one. As
// SIND_REALM does, SIND_CLUSTER wins over the config, so that the cluster
// set in the environment is the cluster every later command acts on.
//
// SIND_CLUSTER must be a valid name (config.CheckName); an invalid one
// fails with an error that names the variable.
func ResolveCluster(configName string) (string, error) {
	if env, ok := envvar.Lookup(envvar.Cluster); ok {
		if err := config.CheckName("cluster", env); err != nil {
			return "", fmt.Errorf("%s: %w", envvar.Cluster, err)
		}
		return env, nil
	}
	if configName != "" {
		return configName, nil
	}
	return config.DefaultClusterName, nil
}
