// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import "fmt"

// JWTAuthType is the authentication plugin of the tokens slurmrestd takes,
// which sind adds to AuthAltTypes for a cluster with a managed api node.
const JWTAuthType = "auth/jwt"

// validateAPI checks the slurm sections against the JWT setup of a managed
// api node: an AuthAltTypes that the main or slurmdbd section sets must
// list auth/jwt, which slurmrestd needs, as sind writes no value of its own
// then. Slurm also takes the plugin without its auth/ prefix. An
// AuthAltParameters there is taken as written, e.g. to point at a JWKS file.
func (c *Cluster) validateAPI() error {
	if !c.HasManagedAPI() {
		return nil
	}
	for _, s := range []struct {
		name    string
		section Section
	}{
		{"main", c.Slurm.Main},
		{"slurmdbd", c.Slurm.Slurmdbd},
	} {
		value, ok := s.section.Parameter("AuthAltTypes")
		if ok && !ListsValue(value, JWTAuthType) && !ListsValue(value, "jwt") {
			return fmt.Errorf("slurm %s sets AuthAltTypes=%s: the api node needs %s in it", s.name, value, JWTAuthType)
		}
	}
	return nil
}
