// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The grammar of an image reference, as Docker takes it: an optional
// registry with a port, a lowercase repository path, a tag and a digest.
const (
	refPath   = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`
	refDomain = `(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))*`
	refHost   = `(?:` + refDomain + `|\[[a-fA-F0-9:]+\])(?::[0-9]+)?`
	refTag    = `[\w][\w.-]{0,127}`
	refDigest = `[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*:[0-9a-fA-F]{32,}`
)

var (
	imageReference = regexp.MustCompile(`^(?:` + refHost + `/)?` + refPath + `(?:/` + refPath + `)*(?::` + refTag + `)?(?:@` + refDigest + `)?$`)
	imageTag       = regexp.MustCompile(`^` + refTag + `$`)
	imageDigest    = regexp.MustCompile(`^` + refDigest + `$`)
)

// maxImageName is the longest repository name, registry included, that
// Docker takes.
const maxImageName = 255

// exampleImage is the image reference that the errors give as an example.
const exampleImage = "ghcr.io/gsi-hpc/sind-node:latest"

// ImageRef reports whether s is an image reference that Docker takes.
func ImageRef(s string) bool { return imageError(s) == nil }

// imageError says what is wrong with the image reference s, for people,
// or returns nil if Docker takes it.
func imageError(s string) error {
	name, digest, hasDigest := strings.Cut(s, "@")
	tagAt := strings.LastIndex(name, ":")
	if tagAt < strings.LastIndex(name, "/") {
		tagAt = -1
	}
	repo := name
	if tagAt >= 0 {
		repo = name[:tagAt]
	}
	// Only the repository is lowercase; a tag and a digest keep their case.
	lower := strings.ToLower(repo) + s[len(repo):]
	switch {
	case imageReference.MatchString(s) && len(repo) <= maxImageName:
		return nil
	case strings.ContainsFunc(s, unicode.IsSpace):
		return errors.New("use no spaces, such as " + exampleImage)
	case s != lower && imageReference.MatchString(lower):
		return fmt.Errorf("use lowercase letters, such as %s", lower)
	case hasDigest && !imageDigest.MatchString(digest):
		return errors.New("write the digest after @ as sha256: and its 64 hex digits")
	case tagAt >= 0 && !imageTag.MatchString(name[tagAt+1:]):
		return errors.New("write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05")
	case len(repo) > maxImageName:
		return fmt.Errorf("use at most %d characters before the tag; this name has %d", maxImageName, len(repo))
	}
	return errors.New("write an image reference such as " + exampleImage)
}

// yamlImageProblems records the image references of a config that Docker
// would refuse, as problems of the config.
func yamlImageProblems(cfg *config.Cluster, ps *problems) {
	images := []string{cfg.Defaults.Image}
	for _, n := range cfg.Nodes {
		images = append(images, n.Image)
	}
	var seen []string
	for _, image := range images {
		if image == "" || slices.Contains(seen, image) {
			continue
		}
		seen = append(seen, image)
		if err := imageError(image); err != nil {
			ps.add("", "Config", false, fmt.Errorf("image %s: %w", image, err))
		}
	}
}

// qosLimits are the account limits that name QOS.
var qosLimits = []string{"DefaultQOS", "QOS", "QosLevel"}

// newClusterQOS is the only QOS of a new cluster: Slurm makes it, sind
// makes no other.
const newClusterQOS = "normal"

// qosError says why the account limit key=value cannot work in a new
// cluster: it names a QOS other than normal.
func qosError(key, value string) error {
	if !slices.ContainsFunc(qosLimits, func(k string) bool { return strings.EqualFold(k, key) }) {
		return nil
	}
	for q := range strings.SplitSeq(value, ",") {
		if q = strings.TrimLeft(strings.TrimSpace(q), "+-="); q != "" && !strings.EqualFold(q, newClusterQOS) {
			return fmt.Errorf("%s=%s names the QOS %s, but a new cluster has only the QOS %s, and sind creates no QOS; remove the limit, or create the QOS after the cluster", key, value, q, newClusterQOS)
		}
	}
	return nil
}

// yamlQOSProblems records the account limits of a config that name a QOS
// other than normal, as problems of the config.
func yamlQOSProblems(cfg *config.Cluster, ps *problems) {
	for _, a := range cfg.Accounts {
		for _, key := range a.Limits.Keys() {
			if err := qosError(key, a.Limits[key]); err != nil {
				ps.add("", "Config", false, fmt.Errorf("account %s: %w", a.Name, err))
			}
		}
	}
}

// slurmWarnings are what sind takes in the Slurm config of a managed
// cluster but likely breaks it: the main section setting a parameter that
// sind writes itself (slurm.FixedParameters), which Slurm takes in place
// of sind's, and lines that are no Key=Value pair. The config refuses
// SlurmctldHost and StateSaveLocation itself with a backup controller.
func slurmWarnings(cfg config.Cluster) []string {
	if !cfg.Managed() {
		return nil
	}
	var warns, fixed []string
	for _, key := range slurm.FixedParameters {
		refused := cfg.HasBackupController() && (key == "SlurmctldHost" || key == "StateSaveLocation")
		if !refused && cfg.Slurm.Main.SetsParameter(key) {
			fixed = append(fixed, key)
		}
	}
	if len(fixed) > 0 {
		warns = append(warns, fmt.Sprintf("The lines of slurm.conf set %s, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start.", strings.Join(fixed, ", ")))
	}
	for _, s := range SlurmFiles {
		if s.ID == "plugstack" {
			// Its lines are SPANK plugins, no Key=Value pairs.
			continue
		}
		if line := pairlessLine(*s.section(&cfg.Slurm)); line != "" {
			warns = append(warns, fmt.Sprintf("The lines of %s have %q, which is no Key=Value pair. Slurm may refuse it and not start.", s.File, line))
		}
	}
	return warns
}

// pairlessLine returns the first line of s that is neither empty, a
// comment, an include nor a Key=Value pair.
func pairlessLine(s config.Section) string {
	for line := range s.Lines() {
		text, _, _ := strings.Cut(line, "#")
		text = strings.TrimSpace(text)
		if text == "" || strings.Contains(text, "=") {
			continue
		}
		if first, _, _ := strings.Cut(text, " "); strings.EqualFold(first, "include") {
			continue
		}
		return text
	}
	return ""
}
