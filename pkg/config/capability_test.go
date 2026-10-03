// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSecurityOpts(t *testing.T) {
	require.NoError(t, CheckSecurityOpts("securityOpt", nil))
	require.NoError(t, CheckSecurityOpts("securityOpt", []string{
		"apparmor=unconfined",
		"seccomp=unconfined",
		"seccomp=/etc/docker/profile.json",
		"label=type:spc_t",
		"label:disable", // Docker's older NAME:VALUE form
		"no-new-privileges",
		"no-new-privileges=true",
		"writable-cgroups=false",
		"systempaths=unconfined",
	}))

	for _, opt := range []string{"unconfined", "disable", "privileged=true", "apparmor=", "apparmor:", "=unconfined", "selinux=disable"} {
		err := CheckSecurityOpts("--security-opt", []string{"apparmor=unconfined", opt})
		require.Error(t, err, opt)
		assert.Equal(t, `unknown security option "`+opt+`" in --security-opt: want label=, apparmor=, seccomp=, no-new-privileges, writable-cgroups= or systempaths=`, err.Error())
	}
}

func TestValidate_SecurityOpt(t *testing.T) {
	cfg := &Cluster{Kind: "Cluster", Name: "dev", Nodes: []Node{
		{Role: RoleController},
		{Role: RoleWorker, SecurityOpt: []string{"apparmor=unconfined"}},
	}}
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	cfg.Nodes[1].SecurityOpt = []string{"apparmor-unconfined"}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown security option "apparmor-unconfined" in securityOpt`)
}
