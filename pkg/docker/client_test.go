// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsNotFound_ExitCode1(t *testing.T) {
	err := &exec.ExitError{ProcessState: exitCode1(t)}
	assert.True(t, IsNotFound(err))
}

func TestIsNotFound_WrappedExitCode1(t *testing.T) {
	inner := &exec.ExitError{ProcessState: exitCode1(t)}
	wrapped := fmt.Errorf("inspect: %w", inner)
	assert.True(t, IsNotFound(wrapped))
}

func TestIsNotFound_Nil(t *testing.T) {
	assert.False(t, IsNotFound(nil))
}

func TestIsNotFound_OtherError(t *testing.T) {
	assert.False(t, IsNotFound(fmt.Errorf("connection refused")))
}

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
