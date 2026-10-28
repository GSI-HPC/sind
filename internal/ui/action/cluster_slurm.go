// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

// SlurmFile is a Slurm config file that a new cluster may add lines to:
// the field "slurm-ID" holds them.
type SlurmFile struct {
	ID, File, Example string
	section           func(s *config.Slurm) *config.Section
}

// SlurmFiles are the Slurm config files of the New cluster dialog, in
// order.
var SlurmFiles = []SlurmFile{
	{"main", "slurm.conf", "SelectType=select/cons_tres", func(s *config.Slurm) *config.Section { return &s.Main }},
	{"cgroup", "cgroup.conf", "ConstrainRAMSpace=yes", func(s *config.Slurm) *config.Section { return &s.Cgroup }},
	{"gres", "gres.conf", "Name=gpu File=/dev/null Count=1", func(s *config.Slurm) *config.Section { return &s.Gres }},
	{"topology", "topology.conf", "SwitchName=s0 Nodes=worker-[0-3]", func(s *config.Slurm) *config.Section { return &s.Topology }},
	{"plugstack", "plugstack.conf", "optional /usr/lib64/slurm/spank_demo.so", func(s *config.Slurm) *config.Section { return &s.Plugstack }},
	{"slurmdbd", "slurmdbd.conf", "PurgeJobAfter=12month", func(s *config.Slurm) *config.Section { return &s.Slurmdbd }},
}

// SlurmFields are the fields of the Slurm config files.
func SlurmFields() []string {
	fields := make([]string, len(SlurmFiles))
	for i, s := range SlurmFiles {
		fields[i] = "slurm-" + s.ID
	}
	return fields
}

// clusterSlurm reads the lines of the Slurm config files, the identity
// mode and CVMFS into the config. sind checks the lines; the dialog only
// makes the line ends Unix ones, as a browser sends CR LF.
func clusterSlurm(f Form, cfg *config.Cluster, ps *problems) {
	for _, s := range SlurmFiles {
		text := strings.ReplaceAll(f.Get("slurm-"+s.ID), "\r\n", "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		*s.section(&cfg.Slurm) = config.Section{Content: text}
	}
	switch mode := config.IdentityMode(f.Get("identity")); mode {
	case "", config.IdentityLocal:
	case config.IdentityNSSSlurm, config.IdentityClientIDs:
		cfg.Identity.Mode = mode
		// The dialog hides the box with another mode.
		cfg.Identity.ControllerUsers = mode == config.IdentityClientIDs && checkbox(f, "ctlusers", "Users on the controllers", ps)
	default:
		ps.add("identity", "Identity", true, fmt.Errorf("pick %s, %s or %s", config.IdentityLocal, config.IdentityNSSSlurm, config.IdentityClientIDs))
	}
	cfg.Storage.CVMFS = checkbox(f, "cvmfs", "CVMFS", ps)
}
