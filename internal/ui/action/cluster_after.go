// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"strings"

	"github.com/GSI-HPC/go-nodeset"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// Step is what sind create cluster runs once the nodes of a new cluster
// are up, for the plan: where, and the command lines.
type Step struct {
	Where string
	Lines []string
}

// afterCreate returns what sind create cluster runs and writes for cfg,
// valid and after ApplyDefaults, besides the containers: the users and
// groups on the nodes that get them, the Slurm accounts on the
// controller, and slurm.conf with sind-nodes.conf, which a cluster that sind
// does not manage does not get. pkg/cluster and pkg/slurm make them, as
// they do for the create.
func afterCreate(cfg *config.Cluster) (steps []Step, slurmConf string) {
	if script := cluster.UsersScript(cfg); script != "" {
		var on []string
		for _, rc := range cluster.NodeRunConfigs(cfg, "", "", "", "") {
			if rc.AddUsers {
				on = append(on, rc.ShortName)
			}
		}
		steps = append(steps, Step{Where: "On " + foldNames(on) + ": the users and groups", Lines: strings.Split(strings.TrimSuffix(script, "\n"), "\n")})
	}
	if cfg.HasManagedDB() {
		var lines []string
		for _, cmd := range slurm.AccountCommands(cfg.Accounts, cfg.Users) {
			lines = append(lines, Quote(append([]string{"sacctmgr", "-i"}, cmd...)))
		}
		if len(lines) > 0 {
			steps = append(steps, Step{Where: "On the controller, once slurmdbd knows the cluster: the Slurm accounts", Lines: lines})
		}
	}
	if !cfg.Managed() {
		return steps, ""
	}
	conf, nodes, err := slurmConfFiles(cfg)
	if err != nil {
		// A valid config has memory that sind converts.
		return steps, ""
	}
	return steps, conf + "\n# " + slurm.NodesConfPath + "\n" + nodes
}

// slurmConfFiles is cluster.SlurmConfFiles; tests replace it.
var slurmConfFiles = cluster.SlurmConfFiles

// foldNames writes the short names of nodes as one node set, such as
// controller,submitter,worker-[0-3], or the names one by one.
func foldNames(names []string) string {
	set, err := nodeset.Parse(strings.Join(names, ","))
	if err != nil {
		return strings.Join(names, ", ")
	}
	return set.String()
}
