// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Images of the tests and what sind ui knows of them.
const (
	imgNew    = "ghcr.io/gsi-hpc/sind-node:26.05.4"
	imgOld    = "ghcr.io/gsi-hpc/sind-node:25.11.8"
	imgLatest = "ghcr.io/gsi-hpc/sind-node:latest"
	imgOwn    = "own:dev"
	imgAway   = "ghcr.io/gsi-hpc/sind-node:27.05"
	imgBroken = "broken:1"
	imgNext   = "ghcr.io/gsi-hpc/sind-node:26.11.1"
)

var testFacts = map[string]ImageFacts{
	imgNew:    {Local: true, Version: "26.05.4"},
	imgOld:    {Local: true, Version: "25.11.8"},
	imgLatest: {Local: true, Version: "25.11.8"},
	imgOwn:    {Local: true, Version: "26.05.1", Checked: true},
	imgBroken: {Err: "permission denied"},
	imgNext:   {Local: true, Version: "26.11.1"},
}

// buildWithImages builds the form with what testFacts knows of the images.
func buildWithImages(t *testing.T, f Form) Command {
	t.Helper()
	a, _ := Lookup("create-cluster")
	c := testContext()
	c.Image = func(image string) ImageFacts { return testFacts[image] }
	cmd, err := a.Build(f, c)
	require.NoError(t, err)
	return cmd
}

func TestBuildCreateCluster_Images(t *testing.T) {
	cmd := buildWithImages(t, valid("image", imgNew, "db", "on", "submitter", "on", "db-image", imgOwn, "w0-image", imgAway, "api", "on", "api-image", imgOwn))
	assert.Equal(t, []PlanImage{
		{Image: imgNew, Of: "controller, submitter", ImageFacts: testFacts[imgNew]},
		{Image: imgOwn, Of: "db, api", ImageFacts: testFacts[imgOwn]},
		{Image: imgAway, Of: "workers"},
	}, cmd.Images)
	assert.False(t, cmd.Images[0].CanCheck())
	assert.True(t, cmd.Images[2].CanCheck(), "an image that is not here")
	assert.Empty(t, cmd.Warns)
	assert.Empty(t, cmd.Problems)

	cmd, err := build(t, "create-cluster", valid())
	require.NoError(t, err)
	assert.Empty(t, cmd.Images, "without facts")
}

func TestBuildCreateCluster_MonitoringNeedsTheRelease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       Form
		problem string
		warn    string
		note    bool
	}{
		{"new", valid("monitoring", "on", "image", imgNew), "", "", false},
		{"old", valid("monitoring", "on", "image", imgOld),
			imgOld + " has Slurm 25.11.8, and monitoring needs 26.05 or later; pick a newer image, or clear Monitoring", "", false},
		{"old but pulled", valid("monitoring", "on", "image", imgLatest, "pull", "on"),
			"", "The copy of " + imgLatest + " here has Slurm 25.11.8, older than 26.05, which monitoring needs. Create pulls the image first, so this can change.", false},
		{"old, fixed tag pulled", valid("monitoring", "on", "image", imgOld, "pull", "on"), imgOld + " has Slurm 25.11.8", "", false},
		{"old worker", valid("monitoring", "on", "image", imgNew, "w0-image", imgOld), imgOld + " has Slurm 25.11.8", "", false},
		{"old unmanaged worker", valid("monitoring", "on", "image", imgNew, "w0-image", imgOld, "w0-unmanaged", "on"), "", "", false},
		{"old db", valid("monitoring", "on", "image", imgNew, "db", "on", "db-image", imgOld), "", "", false},
		{"unknown", valid("monitoring", "on", "image", imgAway), "", "", true},
		{"error", valid("monitoring", "on", "image", imgBroken), "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := buildWithImages(t, tc.f)
			if tc.problem == "" {
				assert.Empty(t, cmd.Problems)
			} else {
				require.Len(t, cmd.Problems, 1, "%+v", cmd.Problems)
				assert.Equal(t, "monitoring", cmd.Problems[0].Field)
				assert.Contains(t, cmd.Problems[0].Text, tc.problem)
			}
			if tc.warn == "" {
				assert.Empty(t, cmd.Warns)
			} else {
				assert.Equal(t, []string{tc.warn}, cmd.Warns)
			}
			hasNote := false
			for _, n := range cmd.Notes {
				hasNote = hasNote || n == "Monitoring needs Slurm 26.05 or later in the controller and worker images. sind checks the images when it runs, and stops with an error if one is older."
			}
			assert.Equal(t, tc.note, hasNote)
		})
	}

	cmd := buildWithImages(t, yamlForm("kind: Cluster\nname: lab\nmonitoring: true\ndefaults:\n  image: "+imgOld+"\n"))
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, "yaml", cmd.Problems[0].Field, "a problem of the YAML")
}

func TestBuildCreateCluster_APIAndWorkerReleases(t *testing.T) {
	cmd := buildWithImages(t, valid("image", imgNew, "api", "on", "api-image", imgOld))
	assert.Equal(t, []string{"The api node's image " + imgOld + " has Slurm 25.11.8. sind-node images have slurmrestd from Slurm 26.05; an image of your own needs it installed."}, cmd.Warns)
	assert.Empty(t, cmd.Problems, "sind checks for slurmrestd, not the release")

	cmd = buildWithImages(t, valid("image", imgNew, "api", "on", "api-image", imgAway))
	assert.Contains(t, cmd.Notes, "The api node needs slurmrestd in the image. sind-node images have it from Slurm 26.05. sind checks the image when it runs.")

	cmd = buildWithImages(t, valid("image", imgNew, "api", "on", "api-unmanaged", "on", "api-image", imgOld))
	assert.Empty(t, cmd.Warns, "an unmanaged api node runs no slurmrestd of sind's")

	cmd = buildWithImages(t, valid("image", imgNew, "w0-image", imgNext))
	assert.Equal(t, []string{"The workers' image " + imgNext + " has Slurm 26.11.1, newer than the 26.05.4 of the controller's: slurmd must not be newer than slurmctld."}, cmd.Warns)

	cmd = buildWithImages(t, valid("image", imgNext, "w0-image", imgNew))
	assert.Empty(t, cmd.Warns, "older workers are fine")
}

func TestImageRef(t *testing.T) {
	assert.True(t, ImageRef(imgNew))
	assert.True(t, ImageRef("own:dev@sha256:abc"))
	assert.False(t, ImageRef("-v"))
	assert.False(t, ImageRef("a b"))
}
