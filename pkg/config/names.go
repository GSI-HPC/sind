// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/internal/hostname"
)

// CheckName reports whether name may be used as a cluster or realm name;
// kind ("cluster" or "realm") names it in the error.
//
// Both names become part of Docker resource names (<realm>-<cluster>-net),
// of DNS names (<node>.<cluster>.<realm>.sind) and of paths
// ($XDG_STATE_HOME/sind/<realm>), so each has to be one DNS label:
// lowercase ASCII letters, digits and hyphens, 1 to 63 characters, not
// beginning or ending with a hyphen. A "." would add a DNS label and split
// node arguments such as worker-0.dev in the wrong place, and "/" or ".."
// would leave the state directory. Uppercase letters are refused too: DNS
// ignores case, so Dev and dev would be two sets of containers behind the
// same DNS names.
func CheckName(kind, name string) error {
	err := hostname.CheckLabel(name)
	if err == nil && strings.ToLower(name) != name {
		err = fmt.Errorf("it has uppercase letters")
	}
	if err != nil {
		return fmt.Errorf("invalid %s name %q: %w", kind, name, err)
	}
	return nil
}
