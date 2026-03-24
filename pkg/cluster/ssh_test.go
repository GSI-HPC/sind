// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSSH_BuildCommand(t *testing.T) {
	args := BuildSSHArgs("compute-0", "dev", true, nil, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", "sind-ssh",
		"ssh", "compute-0.dev.sind.local",
	}, args)
}

func TestSSH_BuildCommand_NonInteractive(t *testing.T) {
	args := BuildSSHArgs("compute-0", "dev", false, nil, nil)

	assert.Equal(t, []string{
		"exec", "-i", "sind-ssh",
		"ssh", "compute-0.dev.sind.local",
	}, args)
}

func TestSSH_BuildCommand_WithCommand(t *testing.T) {
	args := BuildSSHArgs("controller", "default", false, nil, []string{"hostname"})

	assert.Equal(t, []string{
		"exec", "-i", "sind-ssh",
		"ssh", "controller.default.sind.local", "hostname",
	}, args)
}

func TestSSH_BuildCommand_WithMultiWordCommand(t *testing.T) {
	args := BuildSSHArgs("compute-0", "dev", false, nil, []string{"ls", "-la", "/tmp"})

	assert.Equal(t, []string{
		"exec", "-i", "sind-ssh",
		"ssh", "compute-0.dev.sind.local", "ls", "-la", "/tmp",
	}, args)
}

func TestSSH_PassthroughOptions(t *testing.T) {
	args := BuildSSHArgs("compute-0", "dev", true, []string{"-v"}, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", "sind-ssh",
		"ssh", "-v", "compute-0.dev.sind.local",
	}, args)
}

func TestSSH_PassthroughOptions_PortForwarding(t *testing.T) {
	args := BuildSSHArgs("controller", "default", true,
		[]string{"-L", "8080:localhost:80"}, nil)

	assert.Equal(t, []string{
		"exec", "-i", "-t", "sind-ssh",
		"ssh", "-L", "8080:localhost:80", "controller.default.sind.local",
	}, args)
}

func TestSSH_PassthroughOptions_ForceTTY(t *testing.T) {
	args := BuildSSHArgs("compute-0", "dev", true,
		[]string{"-t"}, []string{"top"})

	assert.Equal(t, []string{
		"exec", "-i", "-t", "sind-ssh",
		"ssh", "-t", "compute-0.dev.sind.local", "top",
	}, args)
}

func TestSSH_PassthroughOptions_Multiple(t *testing.T) {
	args := BuildSSHArgs("compute-0", "dev", false,
		[]string{"-v", "-o", "StrictHostKeyChecking=no"},
		[]string{"uptime"})

	assert.Equal(t, []string{
		"exec", "-i", "sind-ssh",
		"ssh", "-v", "-o", "StrictHostKeyChecking=no",
		"compute-0.dev.sind.local", "uptime",
	}, args)
}
