// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/ssh"
)

// setupStep is one step of a node's setup script (see nodeSetupScript).
type setupStep struct {
	// name marks the step on the script's stderr, so that a failure tells
	// which step failed.
	name string
	// what describes the step in the error of its failure.
	what string
	// script holds the step's commands, one per line: the setup script runs
	// with sh -e and stops at the first that fails. An && or || list stops
	// it only when its last command fails.
	script string
	// output marks the step whose stdout is the setup script's; the other
	// steps write theirs to stderr.
	output bool
}

// setupStepMarker starts the line that the setup script writes to stderr
// before each step, followed by the step's name. When the script fails, the
// last marker names the step that failed, and the lines after it are what
// the step wrote.
const setupStepMarker = "sind-setup-step: "

// sshSetupStep injects the realm's SSH public key, the setup script's $1,
// and prints the host key that sshd serves.
var sshSetupStep = setupStep{
	name:   "ssh",
	what:   "setting up SSH: injecting SSH key and scanning host key",
	script: ssh.InjectKeyScript,
	output: true,
}

// nodeSetupScript returns the shell script that runs the steps in order,
// each after its marker line, and stops at the first command that fails,
// with that command's exit status.
func nodeSetupScript(steps []setupStep) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, s := range steps {
		fmt.Fprintf(&b, "echo '%s%s' >&2\n", setupStepMarker, s.name)
		script := strings.TrimRight(s.script, "\n")
		if s.output {
			b.WriteString(script + "\n")
		} else {
			b.WriteString("{\n" + script + "\n} >&2\n")
		}
	}
	return b.String()
}

// nodeSetupSteps returns the steps of a node's setup, in the order they
// must run:
//
//   - nss_slurm (managed workers with identity nssSlurm or clientIds; see
//     nssSlurmSteps): the check that the image has the module comes before
//     nsswitch.conf names it, so that a missing module fails with the image
//     name.
//   - slurmrestd (a managed api node; see slurmrestdSteps): the check that
//     the image has it, before the users, so that an image without it fails
//     early, then the drop-in that lets it start in the container.
//   - The cluster's groups, then its users, which name their groups (where
//     the identity mode puts them; see nodeGetsUsers).
//   - The realm's SSH key and the host key (every node).
//
// setupNode runs them after the node's base readiness wait: ssh-keyscan
// asks the running sshd for its key, and useradd does not race the boot's
// own writers of /etc/passwd. nss_slurm and the users are in place before
// Create and WorkerAdd start the Slurm daemons (enableSlurm), and the users
// before createSlurmAccounts runs sacctmgr, as slurmctld resolves the uids
// of new associations only once an hour otherwise. The steps rely on what
// Create did before the node started: the munge key is on the munge volume
// (createResources), so munge starts and the readiness wait sees it.
// createHomes, which runs once on the controller after every node's setup,
// sets ownership by number and needs none of the steps.
func nodeSetupSteps(nc RunConfig) []setupStep {
	var steps []setupStep
	if nc.NSSSlurm {
		steps = append(steps, nssSlurmSteps(nc)...)
	}
	if nc.Role == config.RoleAPI && nc.Managed {
		steps = append(steps, slurmrestdSteps(nc)...)
	}
	if nc.AddUsers && !nc.Users.IsEmpty() {
		steps = append(steps, addUsersStep(nc.Users))
	}
	return append(steps, sshSetupStep)
}

// setupNode sets a node up from the inside in one docker exec, with the
// steps nodeSetupSteps gives it, and returns the node's ed25519 host key.
// The realm's SSH public key is an argument of the shell, not part of its
// script.
//
// The setup is the progress call "setup", under the node's target, with
// the docker exec under it; it ends as endSpan ends it, canceled when a
// sibling's failure or an interrupt stopped it.
func setupNode(ctx context.Context, client *docker.Client, container docker.ContainerName, nc RunConfig, sshPubKey string) (_ string, err error) {
	ctx, call := progress.Start(ctx, progress.KindCall, "setup")
	defer func() { endSpan(ctx, call, err) }()
	stdout, err := runSetupSteps(ctx, client, container, nodeSetupSteps(nc), strings.TrimSpace(sshPubKey))
	if err != nil {
		return "", err
	}
	key, err := ssh.HostKey(stdout)
	if err != nil {
		return "", fmt.Errorf("setting up SSH: %w", err)
	}
	return key, nil
}

// runSetupSteps runs the steps' script in one docker exec, with args as
// the script's positional parameters, and returns its stdout. When a step
// fails, the error says what the step did and carries the exit status of
// the command that failed and what the step wrote to stderr.
func runSetupSteps(ctx context.Context, client *docker.Client, container docker.ContainerName, steps []setupStep, args ...string) (string, error) {
	command := append([]string{"sh", "-c", nodeSetupScript(steps), "sh"}, args...)
	stdout, err := client.Exec(ctx, container, command...)
	if err == nil {
		return stdout, nil
	}
	var exitErr *cmdexec.ExitError
	if errors.As(err, &exitErr) {
		if step, out, ok := failedStep(steps, exitErr.Stderr); ok {
			return "", fmt.Errorf("%s: %w", step.what, &cmdexec.ExitError{Err: exitErr.Err, Stderr: out})
		}
	}
	// docker exec failed itself, e.g. on a container that stopped.
	return "", fmt.Errorf("running the node setup: %w", err)
}

// failedStep returns the step that the last marker line on a failed setup
// script's stderr names, and what the script wrote after that line.
func failedStep(steps []setupStep, stderr string) (setupStep, string, bool) {
	var step setupStep
	var out strings.Builder
	found := false
	for line := range strings.Lines(stderr) {
		if name, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), setupStepMarker); ok {
			if i := slices.IndexFunc(steps, func(s setupStep) bool { return s.name == name }); i >= 0 {
				step, found = steps[i], true
				out.Reset()
				continue
			}
		}
		out.WriteString(line)
	}
	return step, out.String(), found
}
