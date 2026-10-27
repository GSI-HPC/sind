// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// actImageTTL is how long sind ui keeps what it saw of an image, so that a
// preview, which comes with each key, does not run docker each time.
const actImageTTL = 3 * time.Second

// actImages knows the Slurm releases of the images of new clusters: from
// their label, or from a check that the user asked for, which runs
// slurmctld -V in the image, as sind does, and which sind ui keeps by the
// image's ID.
type actImages struct {
	// inspect looks at a local image; discover runs the check, which pulls
	// an image that is not here.
	inspect  func(ctx context.Context, image string) (docker.Image, bool, error)
	discover func(ctx context.Context, image string) (string, error)
	now      func() time.Time

	mu      sync.Mutex
	seen    map[string]actSeen[action.ImageFacts]
	checked map[string]string
}

// actSeen is what sind ui saw of an image or a container, and when.
type actSeen[T any] struct {
	facts T
	at    time.Time
}

// newActImages returns the images of client.
func newActImages(inspect func(context.Context, string) (docker.Image, bool, error), discover func(context.Context, string) (string, error)) *actImages {
	return &actImages{inspect: inspect, discover: discover, now: time.Now, seen: map[string]actSeen[action.ImageFacts]{}, checked: map[string]string{}}
}

// facts tells what sind ui knows of image.
func (a *actImages) facts(ctx context.Context, image string) action.ImageFacts {
	a.mu.Lock()
	s, ok := a.seen[image]
	a.mu.Unlock()
	if ok && a.now().Sub(s.at) < actImageTTL {
		return s.facts
	}
	var f action.ImageFacts
	img, local, err := a.inspect(ctx, image)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case err != nil:
		f.Err = err.Error()
	case local:
		f.Local, f.Version = true, img.Labels[cluster.LabelSlurmVersion]
		if v, ok := a.checked[img.ID]; ok && f.Version == "" {
			f.Version, f.Checked = v, true
		}
	}
	a.seen[image] = actSeen[action.ImageFacts]{f, a.now()}
	return f
}

// check runs slurmctld -V in image and keeps the Slurm release that it
// prints.
func (a *actImages) check(ctx context.Context, image string) error {
	version, err := a.discover(ctx, image)
	if err != nil {
		return err
	}
	img, local, err := a.inspect(ctx, image)
	if err == nil && !local {
		err = errors.New("docker has no such image after the check")
	}
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checked[img.ID] = version
	delete(a.seen, image)
	return nil
}

// ImageLine says what sind ui knows of an image of the plan.
func (p actPreview) ImageLine(i action.PlanImage) string {
	switch {
	case i.Err != "":
		return "sind ui cannot look at it: " + i.Err
	case !i.Local:
		return "Not on this host; Create pulls it. Check image pulls it now and reads its Slurm release."
	case i.Version == "":
		return "No label with its Slurm release, as an image of your own may have. Check image starts it once to read the release."
	}
	line := "Slurm " + i.Version
	if i.Checked {
		line += ", as a check found"
	}
	if i.Moves {
		line += ". Create pulls the image first, so this can change"
	}
	return line + "."
}

// CheckVals are the values of the Check image button of image.
func (p actPreview) CheckVals(image string) string {
	vals, _ := json.Marshal(map[string]string{"check": image}) // strings only
	return string(vals)
}

// actCheckTimeout is how long a check of an image may take, its pull
// included.
const actCheckTimeout = 10 * time.Minute

// actCheckRoute checks the image of the form's field "check", then answers
// as a preview does.
func actCheckRoute(s *Server, w http.ResponseWriter, r *http.Request) {
	a, st, v, ok := actPost(s, w, r)
	if !ok {
		return
	}
	image := strings.TrimSpace(r.PostForm.Get("check"))
	switch {
	case a.ID != "create-cluster" || st.images == nil:
		s.Fail(w, r, fmt.Errorf("action %s checks no image: %w", a.ID, ErrNotFound))
		return
	case !action.ImageRef(image):
		s.Fail(w, r, fmt.Errorf("check: write an image reference: %w", ErrBadRequest))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), actCheckTimeout)
	defer cancel()
	checkErr := st.images.check(ctx, image)
	p := actEvaluate(v, a, r.PostForm, actContext(r, v, st))
	if checkErr != nil {
		p.Warn = fmt.Sprintf("Checking %s did not work: %v", image, checkErr)
	}
	s.Write(w, r, http.StatusOK, "act-preview", v.withData(p))
}
