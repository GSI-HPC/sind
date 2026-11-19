// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
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

// CheckImage says what is wrong with the image reference ref, for people,
// or returns nil if Docker takes it.
func CheckImage(ref string) error {
	name, digest, hasDigest := strings.Cut(ref, "@")
	tagAt := strings.LastIndex(name, ":")
	if tagAt < strings.LastIndex(name, "/") {
		tagAt = -1
	}
	repo := name
	if tagAt >= 0 {
		repo = name[:tagAt]
	}
	// Only the repository is lowercase; a tag and a digest keep their case.
	lower := strings.ToLower(repo) + ref[len(repo):]
	switch {
	case imageReference.MatchString(ref) && len(repo) <= maxImageName:
		return nil
	case strings.ContainsFunc(ref, unicode.IsSpace):
		return errors.New("use no spaces, such as " + exampleImage)
	case ref != lower && imageReference.MatchString(lower):
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

// imageErrors returns an error for each image reference of the config,
// of the defaults and of the nodes, that Docker would refuse.
func (c *Cluster) imageErrors() []error {
	images := []string{c.Defaults.Image}
	for _, n := range c.Nodes {
		images = append(images, n.Image)
	}
	var seen []string
	var errs []error
	for _, image := range images {
		if image == "" || slices.Contains(seen, image) {
			continue
		}
		seen = append(seen, image)
		if err := CheckImage(image); err != nil {
			errs = append(errs, fmt.Errorf("image %s: %w", image, err))
		}
	}
	return errs
}
