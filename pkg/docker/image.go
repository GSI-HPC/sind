// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// ImageLabels returns the labels of a local image, and whether the image is
// there: false, with no error, when it has not been pulled or built.
func (c *Client) ImageLabels(ctx context.Context, image string) (Labels, bool, error) {
	return c.labels(ctx, "image", "inspect", image, "--format", "{{json .Config.Labels}}")
}

// Image is a local image: its ID and labels.
type Image struct {
	ID     string `json:"id"`
	Labels Labels `json:"labels"`
}

// InspectImage returns the ID and labels of a local image, and whether the
// image is there: false, with no error, when it has not been pulled or
// built.
func (c *Client) InspectImage(ctx context.Context, image string) (Image, bool, error) {
	stdout, _, err := c.run(ctx, "image", "inspect", "--format", `{"id":{{json .Id}},"labels":{{json .Config.Labels}}}`, image)
	if err != nil {
		return Image{}, false, IgnoreNotFound(err)
	}
	var img Image
	if err := json.Unmarshal([]byte(stdout), &img); err != nil {
		return Image{}, false, fmt.Errorf("parsing the inspect of image %s: %w", image, err)
	}
	return img, true, nil
}

// ImageExists reports whether the daemon has an image, without pulling it.
func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	return c.exists(ctx, "image", "inspect", "--format", "{{.Id}}", image)
}

// PullImage pulls an image from its registry, also when a copy of it is
// present. What docker pull writes is not kept; a progress span that shows
// lines, as the targets of the pull images step do, shows the newest of
// its status lines.
//
// The innermost progress span in ctx, which is the image's target and has
// to be running, reports the pull as work (see layerCount). docker pull
// then writes its standard output to a terminal (cmdexec.WithTerminal),
// where it tells the bytes of each layer it downloads, and the work is in
// bytes; where no terminal can be opened, it is in layers. A pull that
// finds the image there already, and writes no layer, ends with one item
// of one done. A pull that fails leaves the work where it was.
func (c *Client) PullImage(ctx context.Context, image string) error {
	target := progress.SpanFrom(ctx)
	if target == nil {
		return c.do(ctx, "pull", image)
	}
	layers := &layerCount{span: target}
	err := c.do(cmdexec.WithLineParser(cmdexec.WithTerminal(ctx), layers.line), "pull", image)
	if err == nil {
		layers.finish()
	}
	return err
}

// RunEphemeral runs a command in a temporary container and returns its stdout.
// The container is removed after the command completes (docker run --rm).
func (c *Client) RunEphemeral(ctx context.Context, image string, command ...string) (string, error) {
	return c.RunEphemeralWith(ctx, nil, image, command...)
}

// RunEphemeralWith is RunEphemeral with extra docker run flags, such as
// mounts, placed before the image.
func (c *Client) RunEphemeralWith(ctx context.Context, flags []string, image string, command ...string) (string, error) {
	args := []string{"run", "--rm"}
	args = append(args, flags...)
	args = append(args, image)
	args = append(args, command...)
	return c.output(ctx, args...)
}
