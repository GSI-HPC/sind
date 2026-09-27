// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
)

// newMCPCommand returns the `sind mcp` command group, which serves sind's
// commands as MCP tools through ophis.
func newMCPCommand(cfg *ophis.Config) *cobra.Command {
	return ophis.Command(cfg)
}
