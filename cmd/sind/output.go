// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const (
	outputHuman = "human"
	outputJSON  = "json"
)

// outputFlag returns the value of the --output flag, looking first at the
// command's own flags and then at inherited flags from parents.
func outputFlag(cmd *cobra.Command) string {
	f := cmd.Flags().Lookup("output")
	if f == nil {
		f = cmd.InheritedFlags().Lookup("output")
	}
	if f == nil {
		return outputHuman
	}
	return f.Value.String()
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
		return fmt.Errorf("invalid --output value %q: must be %q or %q", outputFlag(cmd), outputHuman, outputJSON)
	}
}

// writeJSON encodes v as JSON to w.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
