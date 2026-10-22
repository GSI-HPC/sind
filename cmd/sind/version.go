// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/spf13/cobra"
)

type versionInfo struct {
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"`
	GoVer    string `json:"goVersion"`
	Platform string `json:"platform"`
}

func newVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVersion(cmd)
		},
	}

	cmd.Flags().Bool("json", false, "output as JSON")

	return cmd
}

func runVersion(cmd *cobra.Command) error {
	full := resolveVersion()
	v := strings.TrimPrefix(full, "v")
	c := resolveCommit()

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		info := versionInfo{
			Version:  v,
			Commit:   c,
			GoVer:    runtime.Version(),
			Platform: runtime.GOOS + "/" + runtime.GOARCH,
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		return enc.Encode(info)
	}

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), versionLine())
	return nil
}

// versionLine is the line `sind version` and `sind --version` print.
func versionLine() string {
	full := resolveVersion()
	v := strings.TrimPrefix(full, "v")
	if c := resolveCommit(); c != "" && !strings.Contains(full, "-g") {
		return fmt.Sprintf("sind %s (%s)", v, c)
	}
	return "sind " + v
}

// resolveVersion returns the version set at build time, or, for a binary
// built without one, the module version of a binary built by
// "go install github.com/GSI-HPC/sind/cmd/sind@v0.9.0", whose version the
// checksum database vouches for, and "dev" for anything else.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	return cmp.Or(cobratree.ReadBuildInfo().Version, "dev")
}

// resolveCommit returns the commit set at build time, or, for a binary
// built without one, the short revision of the checkout it was built
// from, with "-dirty" for one that held changes not committed.
func resolveCommit() string {
	if commit != "" {
		return commit
	}
	return cobratree.ReadBuildInfo().Short(7)
}
