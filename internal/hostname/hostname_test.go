// SPDX-License-Identifier: LGPL-3.0-or-later

package hostname_test

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/hostname"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckLabel_Valid(t *testing.T) {
	for _, label := range []string{
		"sind", "default", "dev", "ci-42", "42", "a", "Dev", "CI-Run-7",
		strings.Repeat("a", 63),
	} {
		t.Run(label, func(t *testing.T) {
			assert.NoError(t, hostname.CheckLabel(label))
		})
	}
}

func TestCheckLabel_Invalid(t *testing.T) {
	tests := []struct {
		label   string
		wantErr string
	}{
		{"", "it is empty"},
		{strings.Repeat("a", 64), "it is longer than 63 characters"},
		{"-rf", "it begins with a hyphen"},
		{"dev-", "it ends with a hyphen"},
		{"../x", `'.' is not a letter, a digit or a hyphen`},
		{"a/b", `'/' is not a letter, a digit or a hyphen`},
		{"my_cluster", `'_' is not a letter, a digit or a hyphen`},
		{"dev.test", `'.' is not a letter, a digit or a hyphen`},
		{"with space", `' ' is not a letter, a digit or a hyphen`},
		{"nl\n", `'\n' is not a letter, a digit or a hyphen`},
		{"grüße", `'ü' is not a letter, a digit or a hyphen`},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			err := hostname.CheckLabel(tt.label)
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}
