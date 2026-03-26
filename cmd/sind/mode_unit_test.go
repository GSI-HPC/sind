// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
)

var testID = "unit"

func executeWithDocker(_ ...string) (string, string, error) {
	panic("executeWithDocker called in unit mode")
}

func executeWithDockerCtx(_ context.Context, _ ...string) (string, string, error) {
	panic("executeWithDockerCtx called in unit mode")
}

func realClient(t *testing.T) *docker.Client {
	t.Helper()
	t.Skip("integration test")
	return nil
}

func skipIfNoNsdelegate(t *testing.T) {
	t.Helper()
	t.Skip("integration test")
}

func skipIfNoImage(t *testing.T, _ *docker.Client) string {
	t.Helper()
	t.Skip("integration test")
	return ""
}
