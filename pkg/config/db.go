// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
)

// slurmdbdManagedKeys are slurmdbd.conf parameters that the accounting
// setup on a managed db node depends on: slurmdbd runs there as the slurm
// user and reaches the database sind creates, slurm_acct_db, as the MariaDB
// user slurm over the local socket. Slurm takes the last value, so the
// slurmdbd section would override sind's.
var slurmdbdManagedKeys = []string{"DbdHost", "SlurmUser", "StorageType", "StorageHost", "StorageLoc", "StorageUser"}

// validateSlurmdbd checks the slurmdbd section against the accounting setup
// of a managed db node: it must not set the parameters sind's database setup
// depends on (slurmdbdManagedKeys), and an AuthType it sets must be the
// plugin the cluster authenticates with, auth/slurm with identity clientIds
// and auth/munge otherwise, with or without the auth/ prefix as Slurm takes
// it. With clientIds, an AuthInfo it sets must list use_client_ids.
func (c *Cluster) validateSlurmdbd() error {
	if !c.HasManagedDB() {
		return nil
	}
	s := c.Slurm.Slurmdbd
	for _, key := range slurmdbdManagedKeys {
		if s.SetsParameter(key) {
			return fmt.Errorf("slurm slurmdbd must not set %s: sind sets up the accounting database on the db node and points slurmdbd at it", key)
		}
	}
	auth, info := "auth/munge", ""
	if c.Identity.Mode == IdentityClientIDs {
		auth, info = "auth/slurm", "use_client_ids"
	}
	if value, ok := s.Parameter("AuthType"); ok && strings.TrimPrefix(value, "auth/") != strings.TrimPrefix(auth, "auth/") {
		return fmt.Errorf("slurm slurmdbd sets AuthType=%s: the cluster authenticates with %s", value, auth)
	}
	if value, ok := s.Parameter("AuthInfo"); ok && info != "" && !ListsValue(value, info) {
		return fmt.Errorf("slurm slurmdbd sets AuthInfo=%s: identity %s needs %s in it", value, c.Identity.Mode, info)
	}
	return nil
}
