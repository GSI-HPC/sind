// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"os"

	"github.com/GSI-HPC/sind/internal/ui"
	"github.com/spf13/cobra"
)

// runUI serves sind ui; tests replace it.
var runUI = ui.Run

func newUICommand() *cobra.Command {
	var o ui.Options

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open a local web UI for sind",
		Long: `ui serves a web UI on 127.0.0.1 and opens it in your browser. It shows
your realms, clusters and nodes, and updates live.

Only you can open it: it prints a URL with a token that is new each run,
and it refuses other users' connections. A second sind ui opens the
running one instead. To use it from another machine, forward the port
with ssh -L and pass --no-open.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.RuntimeDir = os.Getenv("XDG_RUNTIME_DIR")
			o.Stderr = cmd.ErrOrStderr()
			return runUI(cmd.Context(), o)
		},
	}

	f := cmd.Flags()
	f.IntVar(&o.Port, "port", 0, "port on 127.0.0.1 to listen on (default: a free one)")
	f.BoolVar(&o.NoOpen, "no-open", false, "print the URL without opening a browser")

	return cmd
}
