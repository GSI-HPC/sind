// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// initAccountingSQL creates the accounting database and the passwordless
// database user slurmdbd connects as over the local socket, and grants the
// user the database. It is safe to run repeatedly. slurmdbd creates the
// schema itself on first start.
const initAccountingSQL = "CREATE DATABASE IF NOT EXISTS " + slurm.AccountingDB + "; " +
	"CREATE USER IF NOT EXISTS '" + slurm.StorageUser + "'@'localhost'; " +
	"GRANT ALL ON " + slurm.AccountingDB + ".* TO '" + slurm.StorageUser + "'@'localhost';"

// enableDBNode starts the accounting services on a db node: mariadb, then
// the accounting database and user, then slurmdbd. systemctl enable --now
// returns once the mariadb unit is active, so the SQL runs against a live
// server.
func enableDBNode(ctx context.Context, client *docker.Client, name docker.ContainerName, shortName string) error {
	if err := enableService(ctx, client, name, shortName, probe.ServiceMariadb); err != nil {
		return err
	}
	if _, err := client.Exec(ctx, name, "mysql", "-e", initAccountingSQL); err != nil {
		return fmt.Errorf("initializing accounting database on %s: %w", shortName, err)
	}
	return enableService(ctx, client, name, shortName, probe.ServiceSlurmdbd)
}
