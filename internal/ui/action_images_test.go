// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// fakeImages is a Docker of images for actImages, which counts how often
// it was asked.
type fakeImages struct {
	images   map[string]docker.Image
	inspects int
	inspErr  error
	release  string
	checkErr error
}

func (f *fakeImages) inspect(_ context.Context, image string) (docker.Image, bool, error) {
	f.inspects++
	img, ok := f.images[image]
	return img, ok, f.inspErr
}

func (f *fakeImages) discover(context.Context, string) (string, error) { return f.release, f.checkErr }

func newFakeImages() (*fakeImages, *actImages, *time.Time) {
	f := &fakeImages{images: map[string]docker.Image{
		"labelled:1": {ID: "sha256:1", Labels: docker.Labels{"sind.slurm.version": "26.05.4"}},
		"own:dev":    {ID: "sha256:2"},
	}, release: "25.11.8"}
	a := newActImages(f.inspect, f.discover)
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	return f, a, &now
}

func TestActImages_Facts(t *testing.T) {
	f, a, now := newFakeImages()
	ctx := t.Context()

	assert.Equal(t, action.ImageFacts{Local: true, Version: "26.05.4"}, a.facts(ctx, "labelled:1"))
	assert.Equal(t, action.ImageFacts{Local: true}, a.facts(ctx, "own:dev"))
	assert.Equal(t, action.ImageFacts{}, a.facts(ctx, "away:1"))
	assert.Equal(t, 3, f.inspects)

	a.facts(ctx, "labelled:1")
	assert.Equal(t, 3, f.inspects, "a preview soon after asks Docker no more")
	*now = now.Add(actImageTTL)
	a.facts(ctx, "labelled:1")
	assert.Equal(t, 4, f.inspects)

	f.inspErr = errors.New("permission denied")
	assert.Equal(t, action.ImageFacts{Err: "permission denied"}, a.facts(ctx, "new:1"))
}

func TestActImages_Check(t *testing.T) {
	f, a, _ := newFakeImages()
	ctx := t.Context()

	a.facts(ctx, "own:dev")
	require.NoError(t, a.check(ctx, "own:dev"))
	assert.Equal(t, action.ImageFacts{Local: true, Version: "25.11.8", Checked: true}, a.facts(ctx, "own:dev"), "the check counts at once")

	f.images["own:dev"] = docker.Image{ID: "sha256:3"}
	f.inspects = 0
	a.seen = map[string]actSeen[action.ImageFacts]{}
	assert.Equal(t, action.ImageFacts{Local: true}, a.facts(ctx, "own:dev"), "a new build is another image")

	f.images["own:dev"] = docker.Image{ID: "sha256:2", Labels: docker.Labels{"sind.slurm.version": "26.05.1"}}
	a.seen = map[string]actSeen[action.ImageFacts]{}
	assert.Equal(t, action.ImageFacts{Local: true, Version: "26.05.1"}, a.facts(ctx, "own:dev"), "the label wins")

	assert.ErrorContains(t, a.check(ctx, "away:1"), "docker has no such image after the check")
	f.checkErr = errors.New("running slurmctld -V: exit 1")
	assert.ErrorContains(t, a.check(ctx, "own:dev"), "slurmctld -V")
	f.checkErr, f.inspErr = nil, errors.New("permission denied")
	assert.ErrorContains(t, a.check(ctx, "own:dev"), "permission denied")
}

func TestActDockerImages(t *testing.T) {
	m := &mock.Executor{}
	m.AddResult(`{"id":"sha256:9","labels":null}`, "", nil)
	m.AddResult("slurm 26.05.4\n", "", nil)
	m.AddResult(`{"id":"sha256:9","labels":null}`, "", nil)
	m.AddResult(`{"id":"sha256:9","labels":null}`, "", nil)
	a := actDockerImages(docker.NewClient(m))

	assert.Equal(t, action.ImageFacts{Local: true}, a.facts(t.Context(), "own:dev"))
	require.NoError(t, a.check(t.Context(), "own:dev"))
	assert.Equal(t, []string{"run", "--rm", "own:dev", "slurmctld", "-V"}, m.Calls[1].Args)
	assert.Equal(t, action.ImageFacts{Local: true, Version: "26.05.4", Checked: true}, a.facts(t.Context(), "own:dev"))
}

func TestActPreview_ImageLine(t *testing.T) {
	var p actPreview
	for _, tc := range []struct {
		img  action.PlanImage
		want string
	}{
		{action.PlanImage{ImageFacts: action.ImageFacts{Err: "denied"}}, "sind ui cannot look at it: denied"},
		{action.PlanImage{}, "Not on this host; Create pulls it. Check image pulls it now and reads its Slurm release."},
		{action.PlanImage{ImageFacts: action.ImageFacts{Local: true}}, "No label with its Slurm release, as an image of your own may have. Check image starts it once to read the release."},
		{action.PlanImage{ImageFacts: action.ImageFacts{Local: true, Version: "26.05.4"}}, "Slurm 26.05.4."},
		{action.PlanImage{ImageFacts: action.ImageFacts{Local: true, Version: "26.05.4", Checked: true}, Moves: true}, "Slurm 26.05.4, as a check found. Create pulls the image first, so this can change."},
	} {
		assert.Equal(t, tc.want, p.ImageLine(tc.img))
	}
	assert.Equal(t, `{"check":"a\"b"}`, p.CheckVals(`a"b`))
}

func TestActCheck(t *testing.T) {
	f := newActFixture(t)
	fake, images, _ := newFakeImages()
	f.state.images = images

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "image", "own:dev")).Body.String()
	assert.Contains(t, body, `<span class="mono">own:dev</span><small>controller, workers</small>`)
	assert.Contains(t, body, `hx-post="/actions/create-cluster/check" hx-vals="{&#34;check&#34;:&#34;own:dev&#34;}"`)

	w := f.post("/actions/create-cluster/check", actForm("name", "hpc", "image", "own:dev", "check", "own:dev"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `<span class="hint act-ver">Slurm 25.11.8, as a check found.</span>`)
	assert.NotContains(t, w.Body.String(), "Check image</button>")

	fake.checkErr = errors.New("no slurmctld")
	w = f.post("/actions/create-cluster/check", actForm("name", "hpc", "image", "own:dev", "check", "own:dev"))
	assert.Contains(t, w.Body.String(), `<div class="callout warn">Checking own:dev did not work: no slurmctld</div>`)

	assert.Equal(t, http.StatusBadRequest, f.post("/actions/create-cluster/check", actForm("check", "-v")).Code)
	assert.Equal(t, http.StatusNotFound, f.post("/actions/power/check", actForm("check", "own:dev")).Code)
	assert.Equal(t, http.StatusNotFound, f.post("/actions/nope/check", "").Code)
	assert.Equal(t, http.StatusBadRequest, f.post("/actions/create-cluster/check", actForm("x", string(make([]byte, actMaxValue+1)))).Code)
	f.state.images = nil
	assert.Equal(t, http.StatusNotFound, f.post("/actions/create-cluster/check", actForm("check", "own:dev")).Code)
	f.srv.env.values.Delete(actKey{})
	assert.Equal(t, http.StatusInternalServerError, f.post("/actions/create-cluster/check", "").Code)
}
