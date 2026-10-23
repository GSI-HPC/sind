// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"slices"
	"testing"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

// TestHelpWordsOfRolesAndServices checks that every role and service that
// the pages mark with data-word="{{.Role}}" or the name of a service is a
// word of help, so that its hint card and its entry exist.
func TestHelpWordsOfRolesAndServices(t *testing.T) {
	h := help.Default()
	names := slices.Clone(pgServiceOrder)
	for _, r := range []config.Role{config.RoleController, config.RoleDB, config.RoleAPI, config.RoleSubmitter, config.RoleWorker} {
		names = append(names, string(r))
	}
	names = append(names, cluster.ControllerBackupShortName)
	for _, n := range names {
		_, ok := h.Word(n)
		assert.True(t, ok, "%s has no word in internal/help/words.yaml", n)
	}
}
