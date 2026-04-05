// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposeLabels(t *testing.T) {
	labels := ComposeLabels("myproject", "web", 2)

	assert.Equal(t, "myproject", labels[ComposeProjectLabel])
	assert.Equal(t, "web", labels[ComposeServiceLabel])
	assert.Equal(t, "2", labels[ComposeContainerNumberLabel])
	assert.Equal(t, "False", labels[ComposeOneoffLabel])
	assert.Equal(t, "", labels[ComposeConfigHashLabel])
	assert.Equal(t, "", labels[ComposeConfigFilesLabel])
	assert.Len(t, labels, 6)
}

func TestSortedLabelFlags_Nil(t *testing.T) {
	assert.Nil(t, SortedLabelFlags(nil))
}

func TestSortedLabelFlags_Empty(t *testing.T) {
	assert.Nil(t, SortedLabelFlags(Labels{}))
}

func TestSortedLabelFlags_Sorted(t *testing.T) {
	labels := Labels{
		"z.label": "last",
		"a.label": "first",
	}
	result := SortedLabelFlags(labels)
	assert.Equal(t, []string{
		"--label", "a.label=first",
		"--label", "z.label=last",
	}, result)
}
