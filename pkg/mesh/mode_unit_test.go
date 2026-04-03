// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package mesh

import "github.com/GSI-HPC/sind/pkg/cmdexec"

// testRealm is DefaultRealm for unit tests; integration tests use a random value.
var testRealm = DefaultRealm

// lifecycleRealm returns testRealm in unit mode. In integration mode it
// returns a per-test unique realm so lifecycle tests can run in parallel.
func lifecycleRealm(*cmdexec.Recorder) string { return testRealm }
