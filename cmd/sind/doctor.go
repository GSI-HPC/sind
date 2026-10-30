// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/go-clikit/cliout"
	clidoctor "github.com/GSI-HPC/go-clikit/doctor"
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

	cmd.Flags().StringP("output", "o", "", outputUsage)
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
		if err := cliout.WriteJSON(cmd.OutOrStdout(), checks); err != nil {
			return err
		}
	} else if err := clidoctor.Write(cmd.OutOrStdout(), checks); err != nil {
		return err
	}
	// The names are the ones the output shows, so the error line points at
	// the lines marked ✗.
	var failed []string
	for _, c := range checks {
		if c.Status == clidoctor.StatusFailed {
			failed = append(failed, c.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("checks failed: %s", strings.Join(failed, ", "))
	}
	return nil
}
