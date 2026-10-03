// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
)

// IdentityMode is where a cluster's nodes look up the users, and how Slurm
// authenticates them.
type IdentityMode string

// Identity modes.
const (
	// IdentityLocal gives every node the Linux accounts, with munge
	// authentication. It is the default.
	IdentityLocal IdentityMode = "local"
	// IdentityNSSSlurm keeps the accounts off managed workers, which
	// resolve the job's user and groups with nss_slurm, from the job
	// credential (LaunchParameters=enable_nss_slurm), with munge
	// authentication.
	IdentityNSSSlurm IdentityMode = "nssSlurm"
	// IdentityClientIDs gives only the login node the accounts:
	// auth/slurm with AuthInfo=use_client_ids carries each user's identity
	// in their tokens to slurmctld and slurmdbd, and nss_slurm serves it on
	// managed workers. munge is off.
	IdentityClientIDs IdentityMode = "clientIds"
)

// Identity selects the identity mode (identity) of a cluster.
type Identity struct {
	Mode IdentityMode `json:"mode,omitempty"`
	// ControllerUsers also creates the users and groups on the controllers
	// with IdentityClientIDs, which a partition with AllowGroups needs:
	// slurmctld checks AllowGroups with local lookups.
	ControllerUsers bool `json:"controllerUsers,omitempty"`
}

// UnmarshalJSON supports two YAML forms:
//   - bare string: "nssSlurm"  (the mode)
//   - full object: "mode: clientIds\n  controllerUsers: true"
func (i *Identity) UnmarshalJSON(data []byte) error {
	var mode string
	if err := json.Unmarshal(data, &mode); err == nil {
		i.Mode = IdentityMode(mode)
		return nil
	}

	type identityAlias Identity // prevents infinite recursion
	var alias identityAlias
	if err := decodeStrict(data, &alias); err != nil {
		return fmt.Errorf("identity must be a mode or an object with mode and controllerUsers: %w", err)
	}
	*i = Identity(alias)
	return nil
}

// UsesNSSSlurm reports whether managed workers resolve users with nss_slurm.
func (i Identity) UsesNSSSlurm() bool {
	return i.Mode == IdentityNSSSlurm || i.Mode == IdentityClientIDs
}

// validate checks the mode, empty for local, and that controllerUsers goes
// with clientIds. Both modes other than local change the Slurm
// configuration, which sind writes only for a managed cluster.
func (i Identity) validate(managed bool) error {
	switch i.Mode {
	case "", IdentityLocal:
		return i.validateControllerUsers()
	case IdentityNSSSlurm, IdentityClientIDs:
	default:
		return fmt.Errorf("identity must be %q, %q or %q, got %q", IdentityLocal, IdentityNSSSlurm, IdentityClientIDs, i.Mode)
	}
	if !managed {
		return fmt.Errorf("identity %s requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster", i.Mode)
	}
	return i.validateControllerUsers()
}

// validateControllerUsers checks that controllerUsers goes with clientIds.
func (i Identity) validateControllerUsers() error {
	if i.ControllerUsers && i.Mode != IdentityClientIDs {
		return fmt.Errorf("identity controllerUsers is only valid with mode %q", IdentityClientIDs)
	}
	return nil
}
