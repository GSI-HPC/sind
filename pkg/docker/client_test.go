// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSortedLabelFlags_Nil(t *testing.T) {
	assert.Nil(t, sortedLabelFlags(nil))
}

func TestSortedLabelFlags_Empty(t *testing.T) {
	assert.Nil(t, sortedLabelFlags(map[string]string{}))
}

func TestSortedLabelFlags_Sorted(t *testing.T) {
	labels := map[string]string{
		"z.label": "last",
		"a.label": "first",
	}
	result := sortedLabelFlags(labels)
	assert.Equal(t, []string{
		"--label", "a.label=first",
		"--label", "z.label=last",
	}, result)
}
