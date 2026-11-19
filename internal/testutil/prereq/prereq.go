// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package prereq

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/doctor"
	"github.com/GSI-HPC/sind/internal/testutil/itest"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/afero"
)

// infoTimeout bounds the docker info of Check, which the tests run while
// others create and delete clusters on the same daemon.
const infoTimeout = 30 * time.Second

var (
	checkOnce sync.Once
	// unmet is why the host does not meet the prerequisites, or "".
	unmet string
)

// Check ends t with itest.Unavailable unless the host meets sind's
// prerequisites: a Docker version that sind supports, and cgroup v2
// mounted with nsdelegate. Only its first call checks them, with c; the
// later ones take its answer, as neither can change within the process.
func Check(t testing.TB, c *docker.Client) {
	t.Helper()
	checkOnce.Do(func() { unmet = check(c) })
	if unmet != "" {
		itest.Unavailable(t, "%s", unmet)
	}
}

// check returns why the host does not meet the prerequisites, or "".
func check(c *docker.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), infoTimeout)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Sprintf("cannot query the Docker version: %v", err)
	}
	if err := doctor.CheckDockerVersion(info.ServerVersion); err != nil {
		return err.Error()
	}
	if _, _, nsdelegate := doctor.CgroupInfo(afero.NewOsFs()); !nsdelegate {
		return "the host's cgroup mount lacks nsdelegate"
	}
	return ""
}
