// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
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

// slurmConfPair matches a key=value pair at the start of a slurmdbd.conf
// line as Slurm's parser does (keyvalue_pattern in
// src/common/parse_config.c): the value is quoted, or runs up to the next
// whitespace.
var slurmConfPair = regexp.MustCompile(`^\s*([[:alnum:]_.]+)\s*[-*+/]?=\s*(?:"([^"]*)"|(\S+))(?:\s|$)`)

// slurmdbdStoragePass returns the StoragePass a slurmdbd section sets, empty
// for none. As slurmdbd reads slurmdbd.conf, the last one counts, in the
// string form or in the fragments in the order slurmdbd.conf includes them,
// keys match regardless of case, a line may hold several pairs, a comment
// starts at a # that is not escaped, and a backslash escapes the next
// character. Lines continued with a trailing backslash are not joined.
func slurmdbdStoragePass(s config.Section) string {
	texts := []string{s.Content}
	for _, key := range s.FragmentNames() {
		texts = append(texts, s.Fragments[key])
	}
	var pass string
	for _, text := range texts {
		for line := range strings.Lines(text) {
			line = unescapeSlurmConf(stripSlurmConfComment(line))
			for {
				m := slurmConfPair.FindStringSubmatchIndex(line)
				if m == nil {
					break
				}
				var value string
				var end int
				if m[4] >= 0 { // quoted: continue past the closing quote
					value, end = line[m[4]:m[5]], m[5]+1
				} else {
					value, end = line[m[6]:m[7]], m[7]
				}
				if strings.EqualFold(line[m[2]:m[3]], "StoragePass") {
					pass = value
				}
				line = line[end:]
			}
		}
	}
	return pass
}

// stripSlurmConfComment cuts a slurmdbd.conf line at its comment: a # that
// an even number of backslashes precedes.
func stripSlurmConfComment(line string) string {
	backslashes := 0
	for i := range len(line) {
		switch {
		case line[i] == '\\':
			backslashes++
			continue
		case line[i] == '#' && backslashes%2 == 0:
			return line[:i]
		}
		backslashes = 0
	}
	return line
}

// unescapeSlurmConf removes the backslash before each escaped character of
// a slurmdbd.conf line, as Slurm's parser does: \# is #, \\ is \.
func unescapeSlurmConf(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			if i == len(line) {
				break
			}
		}
		b.WriteByte(line[i])
	}
	return b.String()
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
