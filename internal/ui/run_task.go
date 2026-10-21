// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// registerRunTask adds the page of a task whose run is not in the event log
// yet, and the routes of the dock: GET /tasks/{id} and POST
// /tasks/{id}/dismiss. POST /tasks/{id}/cancel belongs to the actions.
func registerRunTask(r *Registry) {
	r.AddFragment(Fragment{
		Name:     "run-wait",
		Topics:   []store.Topic{store.TopicTasks},
		Prefixes: []string{"run/"},
		Params:   []string{"id"},
		Load:     func(v *View) error { v.Data = runWaitOf(v); return nil },
	})
	r.AddRoute(Route{Pattern: "GET /tasks/{id}", Handle: runTaskPage})
	r.AddRoute(Route{Pattern: "POST /tasks/{id}/dismiss", Handle: runTaskDismiss})
}

// runWait is what the waiting page says about a task.
type runWait struct {
	// Gone is set when the task was dismissed while the page is open.
	Gone bool
	// Redirect is the run page to go to, once the log holds the run.
	Redirect string
	TaskID   string
	Title    string
	Class    string
	// PillClass colours the state of the task.
	PillClass string
	Elapsed   string
	Since     int64
	Cancel    bool
	Canceling bool
	Err       string
	Output    string
}

// runWaitOf builds the waiting page's fragment for the task in the Param id.
func runWaitOf(v *View) *runWait {
	t, ok := v.Snap.Task(v.Params.Get("id"))
	if !ok {
		return &runWait{Gone: true}
	}
	w := &runWait{TaskID: t.ID, Title: t.Display, Err: t.Err, Output: t.Output}
	if w.Title == "" {
		w.Title = t.Action
	}
	if run, ok := v.Snap.RunOfTrace(t.Trace); ok {
		w.Redirect = "/runs/" + run.ID
	}
	card := dockTaskCard(t, store.Run{}, false, v.Now)
	w.Class, w.Elapsed, w.Since = card.Class, card.Elapsed, card.Since
	w.PillClass = runPillClass(card.Class)
	w.Cancel, w.Canceling = card.Cancel, card.Canceling
	return w
}

// runTaskPage answers GET /tasks/{id}: a redirect to the run page when the
// log holds the run of the task, else the waiting page, which goes there by
// itself as soon as the run shows up.
func runTaskPage(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id := v.Params.Get("id")
	t, ok := v.Snap.Task(id)
	if !ok {
		s.Fail(w, r, fmt.Errorf("task %s: %w", id, ErrNotFound))
		return
	}
	if run, ok := v.Snap.RunOfTrace(t.Trace); ok {
		url := "/runs/" + run.ID
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", url)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	v.Page, v.Title, v.Nav, v.Content = "run-task", "Starting "+t.Display, "activity", "page-run-task"
	v.Params.Set("task", id)
	v.Events = s.eventsURL([]string{"run-wait"}, v)
	s.Write(w, r, http.StatusOK, "layout", v)
}

// runTaskDismiss answers POST /tasks/{id}/dismiss: it removes the card of a
// task that has ended. A task that is gone already counts as dismissed.
func runTaskDismiss(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	id := v.Params.Get("id")
	if t, ok := v.Snap.Task(id); ok {
		if !taskEnded(t) {
			s.Fail(w, r, fmt.Errorf("the command still runs; stop it first: %w", ErrBadRequest))
			return
		}
		s.Store().RemoveTask(id)
	}
	w.WriteHeader(http.StatusNoContent)
}
