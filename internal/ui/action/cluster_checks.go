// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import "github.com/GSI-HPC/sind/pkg/config"

// ImageRef reports whether s is an image reference that Docker takes.
func ImageRef(s string) bool { return config.CheckImage(s) == nil }
