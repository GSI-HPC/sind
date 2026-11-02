// SPDX-License-Identifier: LGPL-3.0-or-later

// Package testutil provides shared test helpers used across sind packages.
package testutil

import (
	"archive/tar"
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/stretchr/testify/require"
)

// PsEntry mirrors the docker ps --format json output structure.
type PsEntry struct {
	ID       string `json:"ID"`
	Names    string `json:"Names"`
	State    string `json:"State"`
	Status   string `json:"Status,omitempty"`
	Image    string `json:"Image"`
	Labels   string `json:"Labels,omitempty"`
	Networks string `json:"Networks,omitempty"`
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

// TarModes returns the mode of each file in a tar archive, as docker cp
// receives it on stdin.
func TarModes(t *testing.T, archive string) map[string]int64 {
	t.Helper()
	modes := map[string]int64{}
	tr := tar.NewReader(strings.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return modes
		}
		require.NoError(t, err)
		modes[hdr.Name] = hdr.Mode
	}
}

// WithoutCalls draws the spans of events as progresstest.Capture.Tree
// does, leaving out the calls and what is under them: the docker commands,
// which the tests of the docker package cover, and of which a wait that
// polls runs as many as its rounds take.
func WithoutCalls(events []progress.Event) string {
	var c progresstest.Capture
	calls := map[progress.SpanID]bool{}
	for _, e := range events {
		if e.Kind == progress.KindCall || calls[e.Parent] {
			calls[e.Span] = true
			continue
		}
		c.Handle(e)
	}
	return c.Tree()
}

// Image returns the sind-node image the tests run: SIND_TEST_IMAGE, or
// ghcr.io/gsi-hpc/sind-node:latest when it is unset.
func Image() string {
	return cmp.Or(os.Getenv("SIND_TEST_IMAGE"), "ghcr.io/gsi-hpc/sind-node:latest")
}

// ArgValue returns the value following the first occurrence of flag in args.
func ArgValue(args []string, flag string) (string, bool) {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1], true
	}
	return "", false
}

// ArgValues returns all values following each occurrence of flag in args.
func ArgValues(args []string, flag string) []string {
	var values []string
	for i := 1; i < len(args); i++ {
		if args[i-1] == flag {
			values = append(values, args[i])
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

// NoSuchContainer returns what docker writes to stderr, with exit code 1,
// for a container that does not exist. Paired with ExitCode1 in a mock
// result, it gives the error the real executor returns, which
// docker.IsNotFound recognises.
func NoSuchContainer(name string) string {
	return "Error response from daemon: No such container: " + name + "\n"
}

// NoSuchNetwork is NoSuchContainer for a network.
func NoSuchNetwork(name string) string {
	return "Error response from daemon: network " + name + " not found\n"
}

// NoSuchVolume is NoSuchContainer for a volume.
func NoSuchVolume(name string) string {
	return "Error response from daemon: get " + name + ": no such volume\n"
}
