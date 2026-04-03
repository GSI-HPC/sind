// SPDX-License-Identifier: LGPL-3.0-or-later

// Package testutil provides shared test helpers used across sind packages.
package testutil

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"iter"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// PsEntry mirrors the docker ps --format json output structure.
type PsEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Image  string `json:"Image"`
	Labels string `json:"Labels,omitempty"`
}

// NDJSON builds newline-delimited JSON from the given entries.
func NDJSON[T any](entries ...T) string {
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

// TarArchive builds a tar archive containing a single file with the given
// name and content.
func TarArchive(name, content string) string {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte(content)
	_ = tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	return buf.String()
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T { return &v }

// Pairs yields adjacent pairs from a string slice.
func Pairs(s []string) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for i := range len(s) - 1 {
			if !yield(s[i], s[i+1]) {
				return
			}
		}
	}
}

// ArgValue returns the value following the first occurrence of flag in args.
func ArgValue(args []string, flag string) (string, bool) {
	for key, val := range Pairs(args) {
		if key == flag {
			return val, true
		}
	}
	return "", false
}

// ArgValues returns all values following each occurrence of flag in args.
func ArgValues(args []string, flag string) []string {
	var values []string
	for key, val := range Pairs(args) {
		if key == flag {
			values = append(values, val)
		}
	}
	return values
}

// ExitCode1 returns an *exec.ExitError with exit code 1.
// This is used to mock Docker inspect commands that return exit code 1
// for missing resources.
func ExitCode1(t *testing.T) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}
