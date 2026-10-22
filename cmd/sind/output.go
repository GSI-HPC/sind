// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/spf13/cobra"
)

const (
	outputHuman = "human"
	outputJSON  = "json"
)

// outputFlag returns the value of the --output flag, the command's own or
// one it inherits.
func outputFlag(cmd *cobra.Command) string {
	if f := cmd.Flag("output"); f != nil {
		return f.Value.String()
	}
	return outputHuman
}

// isJSONOutput returns true when the --output flag is set to "json".
func isJSONOutput(cmd *cobra.Command) bool {
	return outputFlag(cmd) == outputJSON
}

// validateOutputFlag rejects --output values outside the known set.
func validateOutputFlag(cmd *cobra.Command) error {
	switch outputFlag(cmd) {
	case outputHuman, outputJSON:
		return nil
	default:
		return invalidFlagValue("output", outputFlag(cmd), outputHuman, outputJSON)
	}
}
