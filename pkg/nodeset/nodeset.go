// SPDX-License-Identifier: LGPL-3.0-or-later

// Package nodeset provides nodeset pattern expansion for Slurm-style node specifications.
// It implements a subset of the ClusterShell nodeset syntax.
//
// See: https://clustershell.readthedocs.io/en/latest/tools/nodeset.html
package nodeset

import (
	"strings"
)

// Expand expands a nodeset pattern into a list of individual node names.
// Patterns can include:
//   - Simple names: "compute-0" → ["compute-0"]
//   - Comma-separated: "a,b,c" → ["a", "b", "c"]
func Expand(pattern string) ([]string, error) {
	// Split on commas to handle multiple nodesets
	parts := strings.Split(pattern, ",")
	return parts, nil
}
