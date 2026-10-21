// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system prerequisites",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd)
		},
	}

	cmd.Flags().StringP("output", "o", "human", "output format (human|json)")
	registerOutputCompletion(cmd)

	return cmd
}

func runDoctor(cmd *cobra.Command) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	ctx := cmd.Context()
	fs := fsFrom(ctx)
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	mgr := meshMgrFrom(ctx, client, realm)
	checks := doctor.Run(ctx, doctor.Deps{Client: client, Fs: fs, Mesh: mgr})

	if isJSONOutput(cmd) {
		if err := writeJSON(cmd.OutOrStdout(), checks); err != nil {
			return err
		}
	} else {
		printChecks(cmd.OutOrStdout(), checks)
	}
	// The names are the ones the output shows, so the error line points at
	// the lines marked ✗.
	var failed []string
	for _, c := range checks {
		if c.Status == doctor.StatusFailed {
			failed = append(failed, c.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("checks failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// statusMark is the mark of a check: ✓ passed, ✗ failed, and ! for an
// advisory check that did not pass, which leaves the exit status alone.
func statusMark(status string) string {
	if status == doctor.StatusWarning {
		return "!"
	}
	return checkmark(status == doctor.StatusOK)
}

// printChecks writes one line per check, each failed or warning check
// followed by its remediation between blank lines. A detail can quote what
// docker wrote, so it is escaped.
func printChecks(w io.Writer, checks []doctor.Check) {
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "%s %s: %s\n", statusMark(c.Status), c.Name, termtext.EscapeLines(c.Detail))
		if c.Remediation != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n\n", c.Remediation)
		}
	}
}
