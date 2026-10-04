// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
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

// sacctmgrTimeout bounds the docker exec that runs the account commands:
// sacctmgr loops forever on some input, e.g. a Where limit key.
var sacctmgrTimeout = 2 * time.Minute

// errSacctmgrTimeout is the cause of the account commands' context when
// sacctmgrTimeout ends it.
var errSacctmgrTimeout = errors.New("sacctmgr timed out")

// createSlurmAccounts creates the cluster's Slurm accounts and the users'
// associations, admin levels and coordinators with sacctmgr -i, as root on
// the controller (see slurm.AccountCommands). It first waits until slurmdbd
// lists the cluster: before slurmctld has registered it, sacctmgr refuses
// to add users. The commands then run in one docker exec, which
// sacctmgrTimeout bounds.
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
	cmds := slurm.AccountCommands(cfg.Accounts, cfg.Users)
	if len(cmds) == 0 {
		return nil
	}
	log := sindlog.From(ctx)
	for _, cmd := range cmds {
		log.DebugContext(ctx, "sacctmgr", "args", strings.Join(cmd, " "))
	}
	sctx, cancel := context.WithTimeoutCause(ctx, sacctmgrTimeout, errSacctmgrTimeout)
	defer cancel()
	if _, err := client.Exec(sctx, controller, "sh", "-ec", accountsScript(cmds)); err != nil {
		if errors.Is(context.Cause(sctx), errSacctmgrTimeout) {
			return fmt.Errorf("creating Slurm accounts: sacctmgr did not finish within %s", sacctmgrTimeout)
		}
		return fmt.Errorf("creating Slurm accounts: %w", err)
	}
	return nil
}

// accountsRun is the shell function the account script runs each command
// with: sacctmgr -i, and on failure the command and what sacctmgr wrote,
// on stderr, and the end of the script.
const accountsRun = `run() {
	out=$(sacctmgr -i "$@" 2>&1) || { printf 'sacctmgr %s: %s\n' "$*" "$out" >&2; exit 1; }
}
`

// accountsScript returns the shell script that runs the sacctmgr commands
// in order and stops at the first that fails. The arguments come from the
// config, limit values free-form, so each is single-quoted.
func accountsScript(cmds [][]string) string {
	var b strings.Builder
	b.WriteString(accountsRun)
	for _, cmd := range cmds {
		b.WriteString("run")
		for _, arg := range cmd {
			b.WriteString(" " + shellQuote(arg))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
