// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// New cluster opens with from=<run id> to try a create cluster again that
// failed or was stopped, when sind ui has no task with its form: the
// nodes, the image and the wait come from the run's event log, and the
// dialog says what the log does not keep.

// actRetry is what the event log of a create cluster tells the dialog
// that tries it again.
type actRetry struct {
	ID string
	// Source says where the run came from, in the words of the notice,
	// "a terminal", and Lack what sind ui lacks of it; Word is how it
	// ended, failed or stopped.
	Source, Lack, Word string
	// HasNodes says that the log names the nodes of the run: its step
	// nodes had targets. Roles are then the checkbox roles that the run
	// had: backup, db, api, submitter; Workers counts its workers.
	HasNodes bool
	Roles    map[string]bool
	Workers  int
	// Image is the one image that the run pulled; empty when the log does
	// not name one.
	Image string
	// Wait is the wait that nodes ran out of, OldWait its word, and
	// NewWait the longer wait that the dialog sets; Why says which nodes
	// ran out of it.
	Wait    time.Duration
	OldWait string
	NewWait []string
	Why     string
	// Realm is the realm of the run, when one realm has its cluster.
	Realm string
}

// actRetryOf reads the run of the id, a create cluster that ended without
// ok; nil for another run.
func actRetryOf(sn store.Snapshot, id string) *actRetry {
	if id == "" {
		return nil
	}
	var run store.Run
	found := false
	for _, r := range sn.Runs {
		if r.ID == id {
			run, found = r, true
			break
		}
	}
	if !found || run.Root.Name != "create cluster" || !run.Done() || run.Status == store.StatusOK {
		return nil
	}
	r := &actRetry{ID: id, Roles: map[string]bool{}, Realm: runEntryOf(sn, run).realm}
	r.Lack, r.Word = "sind ui does not have its config.", "failed"
	switch runSource(sn, run) {
	case runFromUI:
		r.Source, r.Lack = "sind ui", "sind ui no longer keeps its config."
	case runFromProgram:
		r.Source = "another program"
	default:
		r.Source = "a terminal"
	}
	if run.Status == store.StatusCanceled {
		r.Word = "stopped"
	}
	var images []string
	for _, step := range activity.VisibleChildren(run.Root) {
		if step.Kind != activity.KindStep {
			continue
		}
		activity.Walk(step, func(t store.Span) {
			switch {
			case t.Kind != activity.KindTarget:
			case step.Name == "pull images":
				images = append(images, cmp.Or(t.Node, t.Name))
			case step.Name != "nodes":
			case t.Role == string(config.RoleWorker):
				r.HasNodes = true
				r.Workers++
			case t.Node == cluster.ControllerBackupShortName:
				r.HasNodes, r.Roles["backup"] = true, true
			default:
				r.HasNodes = true
				if t.Role != string(config.RoleController) {
					r.Roles[t.Role] = true
				}
			}
		})
	}
	if len(images) == 1 {
		r.Image = images[0]
	}
	if tos := runTimeouts(run); len(tos) > 0 {
		r.Wait = runLongestWait(tos)
		r.NewWait, _ = runRetryWait(run)
		r.Why = runFoldNodes(runTimeoutNodes(tos)) + " ran out of the " + runWaitWord(r.Wait) + " wait"
		if r.Wait > 0 {
			r.OldWait = runWaitWord(r.Wait)
		}
	}
	return r
}

// defaults fills the fields of New cluster that the run knows: its
// roles and its workers, when the log names them, its image and the
// longer wait.
func (r *actRetry) defaults(d *actDialog) {
	for _, role := range actRoles {
		if r.HasNodes && !d.Form.Has(role.Name) {
			v := ""
			if r.Roles[role.Name] {
				v = "on"
			}
			d.Form.Set(role.Name, v)
		}
	}
	if !d.Form.Has("wg") && r.Workers > 0 {
		d.Form.Set("wg", "w0")
		d.Form.Set("w0-count", strconv.Itoa(r.Workers))
	}
	if r.Image != "" {
		d.def("image", r.Image)
	}
	if !d.Form.Has("pull") {
		// The pull is a choice of its own, which a config from YAML keeps.
		v := ""
		if pull, _ := action.PullByDefault(d.Form.Get("image")); pull {
			v = "on"
		}
		d.Form.Set("pull", v)
	}
	if len(r.NewWait) > 0 && !d.Form.Has("wait") {
		for i := 0; i+1 < len(r.NewWait); i += 2 {
			d.Form.Set(r.NewWait[i], r.NewWait[i+1])
		}
	}
}

// NewWaitWord is the wait that the dialog sets in place of the run's.
func (r *actRetry) NewWaitWord() string {
	if len(r.NewWait) == 0 {
		return ""
	}
	return r.NewWait[len(r.NewWait)-1]
}

// Nodes sums up the nodes of the run: "a controller, a db node and 2
// workers".
func (r *actRetry) Nodes() string {
	parts := []string{"a controller"}
	if r.Roles["backup"] {
		parts = append(parts, "a backup controller")
	}
	for _, role := range actRoles {
		if r.Roles[role.Name] && role.Name != "backup" {
			parts = append(parts, "a "+role.Label+" node")
		}
	}
	if r.Workers > 0 {
		parts = append(parts, pgPlural(r.Workers, "worker", "workers"))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// FromRun reports whether the value of the field, image or nodes, comes
// from the run that the New cluster page tries again.
func (d *actDialog) FromRun(field string) bool {
	r := d.Retry
	return r != nil && (field != "image" || r.Image != "") && (field != "nodes" || r.HasNodes)
}
