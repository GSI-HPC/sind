// SPDX-License-Identifier: LGPL-3.0-or-later

// Command images prints the images of the realm services that sind pins,
// one per line: the mesh's CoreDNS and the monitoring stack's four. CI and
// the overview video pull them from a mirror of Docker Hub before sind
// runs them. Run it from the repository root: go run ./tools/images.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/GSI-HPC/sind/internal/observability"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func main() {
	if err := write(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// images returns the pinned images, the mesh's first.
func images() []string {
	return append([]string{mesh.DNSImage}, observability.Images()...)
}

// write prints the images to w, one per line.
func write(w io.Writer) error {
	for _, image := range images() {
		if _, err := fmt.Fprintln(w, image); err != nil {
			return err
		}
	}
	return nil
}
