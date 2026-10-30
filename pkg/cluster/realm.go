// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// RealmFlagError is the error ResolveRealm returns for an invalid --realm
// value, so that a command line can report it as a usage error.
type RealmFlagError struct {
	Err error
}

// Error returns the cause, prefixed with the flag.
func (e *RealmFlagError) Error() string { return "--realm: " + e.Err.Error() }

// Unwrap returns the cause.
func (e *RealmFlagError) Unwrap() error { return e.Err }

// ResolveRealm determines the realm with the following precedence:
//
//	--realm flag > SIND_REALM env var > config file > mesh.DefaultRealm
//
// flag is the value of --realm, nil when it was not given. configRealm is
// the realm of a config file, "" when there is none: only `create cluster`
// reads one; every other command passes "". Ranking SIND_REALM above the
// config keeps them in step: a realm set in the environment is the realm
// every later command looks in.
//
// The realm that wins must be a valid name (config.CheckName); a config
// file's realm has already been checked by config.Validate. An invalid flag
// fails with a *RealmFlagError, an invalid SIND_REALM with an error that
// names the variable.
func ResolveRealm(flag *string, configRealm string) (string, error) {
	if flag != nil {
		if err := config.CheckName("realm", *flag); err != nil {
			return "", &RealmFlagError{Err: err}
		}
		return *flag, nil
	}
	if env, ok := envvar.Lookup(envvar.Realm); ok {
		if err := config.CheckName("realm", env); err != nil {
			return "", fmt.Errorf("%s: %w", envvar.Realm, err)
		}
		return env, nil
	}
	if configRealm != "" {
		return configRealm, nil
	}
	return mesh.DefaultRealm, nil
}
