// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// ImageFacts is what sind ui knows of an image: whether Docker has it,
// and its Slurm release, from the image's label or from a check that ran
// slurmctld -V in it.
type ImageFacts struct {
	Local   bool
	Version string
	// Checked says that Version comes from a check, not from the label.
	Checked bool
	// Err says why sind ui could not look at the image.
	Err string
}

// PlanImage is an image of a new cluster, as the plan shows it.
type PlanImage struct {
	Image string
	// Of names the nodes that run it, such as "controller, workers".
	Of string
	ImageFacts
	// Moves says that Create pulls the image first and that its tag can
	// move to a newer image, so that its Slurm release can change.
	Moves bool
}

// CanCheck says that a check can find the Slurm release of the image,
// which sind ui does not know.
func (i PlanImage) CanCheck() bool { return i.Version == "" && i.Err == "" }

// nodeImage is the image of the node n of cfg.
func nodeImage(cfg config.Cluster, n config.Node) string {
	return cmp.Or(n.Image, cfg.Defaults.Image, config.DefaultImage)
}

// nodeManaged reports whether sind runs the Slurm daemons of n.
func nodeManaged(cfg config.Cluster, n config.Node) bool {
	return cfg.Managed() && (n.Managed == nil || *n.Managed)
}

// clusterImages lists the images of the nodes of cfg and what sind ui knows
// of them, and checks their Slurm releases against what monitoring and the
// api node need, as sind will when it runs: an image that has a Slurm too
// old for monitoring is a problem of field, unless Create pulls a newer
// one first. Without facts, it only says what sind checks.
func clusterImages(cfg config.Cluster, pull bool, facts func(string) ImageFacts, field string, cmd *Command, ps *problems) {
	var images []PlanImage
	add := func(image, of string) {
		i := slices.IndexFunc(images, func(p PlanImage) bool { return p.Image == image })
		if i < 0 {
			moves, _ := PullByDefault(image)
			images = append(images, PlanImage{Image: image, Moves: pull && moves})
			i = len(images) - 1
		}
		if !slices.Contains(strings.Split(images[i].Of, ", "), of) {
			images[i].Of = strings.TrimPrefix(images[i].Of+", "+of, ", ")
		}
	}
	for _, n := range cfg.Nodes {
		of := string(n.Role)
		if n.Role == config.RoleWorker {
			of = "workers"
		}
		add(nodeImage(cfg, n), of)
	}
	if facts != nil {
		for i := range images {
			images[i].ImageFacts = facts(images[i].Image)
		}
		cmd.Images = images
	}
	version := func(image string) PlanImage {
		i := slices.IndexFunc(images, func(p PlanImage) bool { return p.Image == image })
		return images[i]
	}

	var controller PlanImage
	for _, n := range cfg.Nodes {
		if n.Role == config.RoleController {
			controller = version(nodeImage(cfg, n))
		}
	}
	if cfg.Monitoring && cfg.Managed() {
		monitoringImages(cfg, controller, version, field, cmd, ps)
	}
	for _, n := range cfg.Nodes {
		img := version(nodeImage(cfg, n))
		switch {
		case !nodeManaged(cfg, n):
		case n.Role == config.RoleAPI && img.Version == "":
			cmd.Notes = append(cmd.Notes, fmt.Sprintf("The api node needs slurmrestd in the image. sind-node images have it from Slurm %s. sind checks the image when it runs.",
				slurm.MonitoringMinVersion))
		case n.Role == config.RoleAPI && !slurm.VersionAtLeast(img.Version, slurm.MonitoringMinVersion):
			cmd.Warns = append(cmd.Warns, fmt.Sprintf("The api node's image %s has Slurm %s. sind-node images have slurmrestd from Slurm %s; an image of your own needs it installed.",
				img.Image, img.Version, slurm.MonitoringMinVersion))
		case n.Role == config.RoleWorker && img.Version != "" && controller.Version != "" && !slurm.VersionAtLeast(controller.Version, img.Version):
			cmd.Warns = append(cmd.Warns, fmt.Sprintf("The workers' image %s has Slurm %s, newer than the %s of the controller's: slurmd must not be newer than slurmctld.",
				img.Image, img.Version, controller.Version))
		}
	}
}

// monitoringImages checks the images that monitoring needs a Slurm of
// slurm.MonitoringMinVersion or later in: the controller's and those of
// the managed workers.
func monitoringImages(cfg config.Cluster, controller PlanImage, version func(string) PlanImage, field string, cmd *Command, ps *problems) {
	need := []PlanImage{controller}
	for _, n := range cfg.Nodes {
		if img := version(nodeImage(cfg, n)); n.Role == config.RoleWorker && nodeManaged(cfg, n) && !slices.Contains(need, img) {
			need = append(need, img)
		}
	}
	unknown := false
	for _, img := range need {
		switch {
		case img.Version == "":
			unknown = true
		case slurm.VersionAtLeast(img.Version, slurm.MonitoringMinVersion):
		case img.Moves:
			cmd.Warns = append(cmd.Warns, fmt.Sprintf("The copy of %s here has Slurm %s, older than %s, which monitoring needs. Create pulls the image first, so this can change.",
				img.Image, img.Version, slurm.MonitoringMinVersion))
		default:
			ps.add(field, "Monitoring", false, fmt.Errorf("%s has Slurm %s, and monitoring needs %s or later; pick a newer image, or clear Monitoring",
				img.Image, img.Version, slurm.MonitoringMinVersion))
		}
	}
	if unknown {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("Monitoring needs Slurm %s or later in the controller and worker images. sind checks the images when it runs, and stops with an error if one is older.",
			slurm.MonitoringMinVersion))
	}
}
