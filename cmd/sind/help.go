// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/spf13/cobra"
)

// addHelpTopics makes sind help TOPIC print the words, how-tos and actions
// of sind ui from package help, the texts that sind ui shows, and sind help
// topics list them. A word that names a command is left to the command's
// own help. It runs after cobratree.Strict, whose help command it extends.
func addHelpTopics(root *cobra.Command) {
	h := help.Default()
	helpCmd, _, _ := root.Find([]string{"help"})

	// topic returns the text for args when they name a topic that is no
	// command.
	topic := func(args []string) (string, bool) {
		if len(args) != 1 {
			return "", false
		}
		if found, rest, _ := root.Find(args); found != root && len(rest) == 0 {
			return "", false
		}
		if args[0] == "topics" {
			return h.Index(), true
		}
		return h.Text(args[0])
	}

	args, run := helpCmd.Args, helpCmd.Run
	helpCmd.Args = func(cmd *cobra.Command, a []string) error {
		if _, ok := topic(a); ok {
			return nil
		}
		return args(cmd, a)
	}
	helpCmd.Run = func(cmd *cobra.Command, a []string) {
		if text, ok := topic(a); ok {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), text)
			return
		}
		run(cmd, a)
	}
	helpCmd.Short = "Help about any command or topic"
	helpCmd.Long = `Help provides help for any command in the application, and for the words,
how-tos and actions of sind ui: sind help realm. Simply type sind help
[path to command] or sind help TOPIC for the full details. sind help topics
lists the topics.`
}
