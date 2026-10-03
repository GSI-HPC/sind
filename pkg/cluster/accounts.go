// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/monitor"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// createSlurmAccounts creates the cluster's Slurm accounts and the users'
// associations, admin levels and coordinators with sacctmgr -i, as root on
// the controller (see slurm.AccountCommands). It first waits until slurmdbd
// lists the cluster: before slurmctld has registered it, sacctmgr refuses
// to add users.
//
// The Linux users exist on the controller by then (setupNodes), so
// slurmctld resolves the uids of the new associations right away instead
// of an hour later. sacctmgr -i does not stop for a user it cannot find.
func createSlurmAccounts(ctx context.Context, client *docker.Client, realm string, cfg *config.Cluster, interval time.Duration, watcher *monitor.Watcher) error {
	controller := ContainerName(realm, cfg.Name, string(config.RoleController))
	registered := probe.Probe{Name: "registration", Check: probe.ClusterRegistered(cfg.Name)}
	if err := waitReady(ctx, client, controller, []probe.Probe{registered}, interval, watcher); err != nil {
		return fmt.Errorf("waiting for slurmdbd to register the cluster: %w", err)
	}
	for _, cmd := range slurm.AccountCommands(cfg.Accounts, cfg.Users) {
		sindlog.From(ctx).DebugContext(ctx, "sacctmgr", "args", strings.Join(cmd, " "))
		if _, err := client.Exec(ctx, controller, append([]string{"sacctmgr", "-i"}, cmd...)...); err != nil {
			return fmt.Errorf("creating Slurm accounts: sacctmgr %s: %w", strings.Join(cmd, " "), err)
		}
	}
	return nil
}
