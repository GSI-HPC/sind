// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitError returns the error the real executor gives for a command that
// exited with code and wrote stderr.
func exitError(t *testing.T, code int, stderr string) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return cmdexec.WrapExitError(exitErr, stderr)
}

func TestIsNotFound_DockerMessages(t *testing.T) {
	for _, stderr := range []string{
		"Error response from daemon: No such container: sind-dev-controller\n",
		"Error: No such container: sind-dev-controller\n",
		"Error response from daemon: No such image: example:latest\n",
		"error: no such object: sind-dns\n",
		"Error response from daemon: get sind-dev-config: no such volume\n",
		"Error: No such volume: sind-dev-config\n",
		"Error response from daemon: network sind-dev-net not found\n",
		"Error: No such network: sind-dev-net\n",
	} {
		t.Run(stderr, func(t *testing.T) {
			err := exitError(t, 1, stderr)
			assert.True(t, IsNotFound(err))
			assert.True(t, IsNotFound(fmt.Errorf("inspect: %w", err)))
		})
	}
}

func TestIsNotFound_OtherExit1(t *testing.T) {
	// docker exits 1 for failures other than a missing resource; reading
	// them as not found would leak the resource.
	for _, stderr := range []string{
		"Error response from daemon: remove sind-dev-config: volume is in use - [abc123]\n",
		"Error response from daemon: error while removing network: network sind-dev-net has active endpoints\n",
		// The CLI itself, when it cannot reach the daemon.
		"failed to connect to the docker API at unix:///var/run/docker.sock; check if the path is correct and if the daemon is running: dial unix /var/run/docker.sock: connect: no such file or directory\n",
		"context \"nope\": context not found: open /root/.docker/contexts/meta/abc/meta.json: no such file or directory\n",
		"open /nonexistent/ca.pem: no such file or directory\n",
		"",
	} {
		t.Run(stderr, func(t *testing.T) {
			assert.False(t, IsNotFound(exitError(t, 1, stderr)))
		})
	}
}

func TestIsNotFound_OtherExitCode(t *testing.T) {
	assert.False(t, IsNotFound(exitError(t, 2, "Error: No such container: x\n")))
}

func TestIsNotFound_BareExitError(t *testing.T) {
	// Without stderr there is nothing to tell a missing resource by.
	assert.False(t, IsNotFound(&exec.ExitError{ProcessState: exitCode1(t)}))
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
