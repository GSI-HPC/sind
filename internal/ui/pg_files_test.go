// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestPgFilesOf(t *testing.T) {
	f := pgFilesOf(cluster.MountPoint{Type: config.StorageHostPath, Source: "~/sind/x y", Path: "/data"}, "c")
	assert.Equal(t, "cp job.sh ~/'sind/x y/'", f.Copy, "the shell still expands ~/")
	f = pgFilesOf(cluster.MountPoint{Type: config.StorageHostPath}, "c")
	assert.Equal(t, pgFiles{Kind: "On this host", Node: "/data", Text: "Say which directory of this host the nodes see at /data."}, f)
	f = pgFilesOf(cluster.MountPoint{Type: config.StorageVolume, Source: "v", Path: "/data/"}, "c")
	assert.Equal(t, pgFiles{Kind: "Docker volume", Host: "v", Node: "/data/", Text: "Docker has no volume v, so /data/ is empty in the nodes."}, f)
	f = pgFilesOf(cluster.MountPoint{Type: config.StorageVolume, Source: "v", Path: "/data/", OK: true}, "c")
	assert.Equal(t, "docker cp job.sh c:/data/", f.Copy)

	assert.Equal(t, "s", pgFilesNode([]pgNode{{Role: config.RoleWorker, Container: "w"}, {Role: config.RoleController, Container: "c"}, {Role: config.RoleSubmitter, Container: "s"}}))
	assert.Equal(t, "c", pgFilesNode([]pgNode{{Role: config.RoleController, Container: "c"}, {Role: config.RoleController, Container: "c2"}}))
	assert.Empty(t, pgFilesNode(nil))
}

// The Connect tab says where the files of the jobs are, once the status
// has the data mount.
func TestPgClusterConnectTabFiles(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev?tab=connect", 200)
	assert.Contains(t, body, `<h2>Your files</h2>`)
	assert.Contains(t, body, `Where your files are appears once sind ui has probed the cluster.`)

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=connect", 200)
	assert.Contains(t, body, `<span class="pg-fside"><small>On this host</small><b class="mono">/scratch/dev</b></span><span class="pg-farrow" aria-hidden="true">→</span><span class="pg-fside"><small>In every node</small><b class="mono">/data</b></span>`)
	assert.Contains(t, body, `<code>cp job.sh /scratch/dev/</code>`)

	ts.st.SetStatus("sind", "dev", &cluster.Status{Name: "dev", Mounts: []cluster.MountPoint{{Path: "/work", Source: "sind-dev-data", Type: config.StorageVolume, OK: true, Data: true}}})
	body = pgBody(t, ts, "/clusters/sind/dev?tab=connect", 200)
	assert.Contains(t, body, `<code>docker cp job.sh sind-dev-submitter:/work/</code>`, "through the submitter, where jobs start")
}
