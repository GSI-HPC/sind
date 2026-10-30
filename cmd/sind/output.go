// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/spf13/cobra"
)

const (
	outputHuman = "human"
	outputJSON  = "json"
)

// outputUsage is the help of the --output flag of get and doctor, whose
// default the flag leaves empty, as SIND_OUTPUT may take its place.
const outputUsage = "output format (human|json) (default: " + envvar.Output + ", else " + outputHuman + ")"

// outputFlag returns the output format of cmd: its --output, the command's
// own or one it inherits, else SIND_OUTPUT, else human.
func outputFlag(cmd *cobra.Command) string {
	f := cmd.Flag("output")
	if f == nil {
		return outputHuman
	}
	if f.Changed {
		return f.Value.String()
	}
	if env, ok := envvar.Lookup(envvar.Output); ok {
		return env
	}
	return outputHuman
}

// isJSONOutput returns true when the output format is json.
func isJSONOutput(cmd *cobra.Command) bool {
	return outputFlag(cmd) == outputJSON
}

// validateOutputFlag rejects an output format outside the known set: an
// --output as a usage error, a SIND_OUTPUT as an error that names it.
func validateOutputFlag(cmd *cobra.Command) error {
	switch out := outputFlag(cmd); {
	case out == outputHuman, out == outputJSON:
		return nil
	case cmd.Flag("output").Changed:
		return invalidFlagValue("output", out, outputHuman, outputJSON)
	default:
		return invalidEnvValue(envvar.Output, out, oneOf([]string{outputHuman, outputJSON}))
	}
}
