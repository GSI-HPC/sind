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
	v := strings.TrimPrefix(version, "v")
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

	if c != "" && !strings.Contains(version, "-g") {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sind %s (%s)\n", v, c)
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sind %s\n", v)
	}

	return nil
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
