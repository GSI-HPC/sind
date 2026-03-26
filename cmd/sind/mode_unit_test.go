// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package main

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
)

var testID = "unit"

func executeWithDocker(_ ...string) (string, string, error) {
	panic("executeWithDocker called in unit mode")
}

func realClient(t *testing.T) *docker.Client {
	t.Helper()
	t.Skip("integration test")
	return nil
}
