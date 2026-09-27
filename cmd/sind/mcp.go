// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
)

// mcpStreamHost is the address `sind mcp stream` listens on unless --host
// says otherwise. The stream has no authentication, and its tools create
// and delete containers, so it is not offered to the network by default.
const mcpStreamHost = "127.0.0.1"

// newMCPCommand returns the `sind mcp` command group, which serves sind's
// commands as MCP tools through ophis.
func newMCPCommand(cfg *ophis.Config) *cobra.Command {
	cmd := ophis.Command(cfg)
	for _, sub := range cmd.Commands() {
		if sub.Name() != "stream" {
			continue
		}
		// ophis listens on all interfaces by default.
		host := sub.Flags().Lookup("host")
		host.DefValue = mcpStreamHost
		_ = host.Value.Set(mcpStreamHost)
		host.Usage = "host to listen on (use 0.0.0.0 for all interfaces)"
	}
	return cmd
}
