// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package main

import (
	"context"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
)

var testID = "unit"

func realClient(t *testing.T) *docker.Client {
	t.Helper()
	t.Skip("integration test")
	return nil
}

func checkPrerequisites(t *testing.T, _ *docker.Client) {
	t.Helper()
	t.Skip("integration test")
}

func testImage(t *testing.T) string {
	t.Helper()
	t.Skip("integration test")
	return ""
}

func executeWithRealm(_ string, _ ...string) (string, string, error) {
	panic("executeWithRealm called in unit mode")
}

func executeWithRealmCtx(_ context.Context, _ string, _ ...string) (string, string, error) {
	panic("executeWithRealmCtx called in unit mode")
}
