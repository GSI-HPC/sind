// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

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
		Args:  cobra.NoArgs,
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

	if c != "" && !strings.Contains(full, "-g") {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sind %s (%s)\n", v, c)
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sind %s\n", v)
	}

	return nil
}

// resolveVersion returns the version set at build time, or, for a binary
// built without one, the module version the toolchain recorded.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	bi, _ := debug.ReadBuildInfo()
	return versionFromBuildInfo(bi)
}

// versionFromBuildInfo returns the module version of a binary built by
// "go install github.com/GSI-HPC/sind/cmd/sind@v0.9.0", whose version the
// checksum database vouches for, and "dev" for anything else; bi may be nil.
//
// A build from a checkout carries VCS stamps. The toolchain derives a
// module version for it too, from a tag the commit carries or as a
// pseudo-version, but a local tag is not a release, so that is ignored.
//
// Adapted from GSI-HPC/clusterctl internal/version.
func versionFromBuildInfo(bi *debug.BuildInfo) string {
	if bi == nil || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return "dev"
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return "dev"
		}
	}
	return bi.Main.Version
}

func resolveCommit() string {
	if commit != "" {
		return commit
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	var rev string
	var dirty bool

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	if rev == "" {
		return ""
	}

	if len(rev) > 7 {
		rev = rev[:7]
	}

	if dirty {
		rev += "-dirty"
	}

	return rev
}
