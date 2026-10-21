// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// accountingSQL returns the SQL that creates the accounting database and the
// database user slurmdbd connects as over the local socket, and grants the
// user the database. It is safe to run repeatedly. slurmdbd creates the
// schema itself on first start.
//
// Without a storagePass the user authenticates with unix_socket: only a
// process of the OS user slurm, which slurmdbd runs as, logs in as it, so
// the cluster users sind creates on the db node cannot reach the accounting
// database. With the StoragePass the slurmdbd section sets, as a site's
// slurmdbd.conf would, the user has that password instead. It is given as
// its mysql_native_password hash, which keeps the password out of the
// docker exec command line and needs no quoting.
func accountingSQL(storagePass string) string {
	auth := "IDENTIFIED VIA unix_socket"
	if storagePass != "" {
		auth = "IDENTIFIED BY PASSWORD '" + nativePasswordHash(storagePass) + "'"
	}
	user := "'" + slurm.StorageUser + "'@'localhost'"
	return "CREATE DATABASE IF NOT EXISTS " + slurm.AccountingDB + "; " +
		"CREATE USER IF NOT EXISTS " + user + " " + auth + "; " +
		"GRANT ALL ON " + slurm.AccountingDB + ".* TO " + user + ";"
}

// nativePasswordHash returns the mysql_native_password hash of a password,
// as MariaDB's PASSWORD() computes it: "*" and the uppercase hex digits of
// SHA1(SHA1(password)), the hash that authentication method is defined with.
func nativePasswordHash(password string) string {
	inner := sha1.Sum([]byte(password))
	outer := sha1.Sum(inner[:])
	return "*" + strings.ToUpper(hex.EncodeToString(outer[:]))
}

// enableDBNode starts the accounting services on a db node: mariadb, then
// the accounting database and user, then slurmdbd. systemctl enable --now
// returns once the mariadb unit is active, so the SQL runs against a live
// server. storagePass is the StoragePass of the cluster's slurmdbd section,
// empty for none (see accountingSQL).
func enableDBNode(ctx context.Context, client *docker.Client, name docker.ContainerName, shortName, storagePass string) error {
	if err := enableService(ctx, client, name, shortName, probe.ServiceMariadb); err != nil {
		return err
	}
	if _, err := client.Exec(ctx, name, "mysql", "-e", accountingSQL(storagePass)); err != nil {
		return fmt.Errorf("initializing accounting database on %s: %w", shortName, err)
	}
	return enableService(ctx, client, name, shortName, probe.ServiceSlurmdbd)
}
