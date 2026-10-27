// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"os"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/ui"
	"github.com/GSI-HPC/sind/internal/ui/collect"
	"github.com/spf13/cobra"
)

// runUI serves sind ui; tests replace it.
var runUI = ui.Run

func newUICommand() *cobra.Command {
	var o ui.Options
	var open, noOpen bool

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open a local web UI for sind",
		Long: `ui serves a web UI on 127.0.0.1 and prints its URL; --open also opens it
in your browser. It shows your realms, clusters and nodes, and updates live.

Only you can open it: it prints a URL with a token that is new each run,
and it refuses other users' connections. A second sind ui prints the URL
of the running one instead, and opens it with --open. To use it from
another machine, forward the port with ssh -L.

The Monitoring page reads the monitoring stacks every
--monitoring-interval, and only while a page of sind ui is open. Its
picker changes the interval of the running UI.

--demo serves made-up clusters and plays the commands of the dialogs instead
of running them: it needs no Docker, starts no process and writes nothing.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := collect.CheckInterval(o.MonitoringInterval); err != nil {
				return cobratree.InvalidValue("monitoring-interval", o.MonitoringInterval.String(), "a duration from 5s to 1h")
			}
			o.NoOpen = !open
			o.RuntimeDir = os.Getenv("XDG_RUNTIME_DIR")
			o.Getenv = os.Getenv
			o.Stderr = cmd.ErrOrStderr()
			return runUI(cmd.Context(), o)
		},
	}

	f := cmd.Flags()
	f.IntVar(&o.Port, "port", 0, "port on 127.0.0.1 to listen on (default: a free one)")
	f.BoolVar(&open, "open", false, "also open the URL in a browser")
	// --no-open was the way to keep the browser closed before that became
	// the default; it is still accepted, and does nothing.
	f.BoolVar(&noOpen, "no-open", false, "print the URL without opening a browser (the default)")
	_ = f.MarkHidden("no-open")
	f.DurationVar(&o.MonitoringInterval, "monitoring-interval", collect.MonitoringEvery, "how often to read the monitoring stacks while a page is open (5s to 1h)")
	f.BoolVar(&o.Demo, "demo", false, "serve made-up clusters and play commands instead of running them; no Docker is used")
	f.BoolVar(&o.Still, "still", false, "with --demo: freeze the clock and play nothing, for screenshots")
	_ = f.MarkHidden("still")

	return cmd
}
