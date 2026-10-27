// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// registerRunTask adds the page of a task whose run is not in the event log
// yet, and the routes of the dock: GET /tasks/{id}, POST
// /tasks/{id}/dismiss and POST /tasks/clear. POST /tasks/{id}/cancel belongs
// to the actions.
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
	r.AddRoute(Route{Pattern: "POST /tasks/clear", Handle: runTaskClear})
}

// runWait is what the waiting page says about a task.
type runWait struct {
	// Gone is set when the store dropped the task while the page is open.
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
	// Stop is the Stop button while the task runs.
	Stop      *runStop
	Canceling bool
	Err       string
	Output    string
	// NoRun says that the command writes no event log, so it never has a
	// run page.
	NoRun bool
	// Command, Back, RetryID and Edit are those of the run page (runHead),
	// for a command that ended before it wrote a run.
	Command *pgCmd
	Back    *runLink
	RetryID string
	Edit    *runLink
	CSRF    string
	// SayKey, SayState and Say are what run.js announces when the task
	// ends while the page waits. The key is the page's own, and the page
	// announces no start: the run page that follows at once does.
	SayKey   string
	SayState string
	Say      string
}

// runWaitOf builds the waiting page's fragment for the task in the Param id.
func runWaitOf(v *View) *runWait {
	t, ok := v.Snap.Task(v.Params.Get("id"))
	if !ok {
		return &runWait{Gone: true}
	}
	w := &runWait{TaskID: t.ID, Err: t.Err, Output: t.Output, NoRun: runless(t)}
	if run, ok := v.Snap.RunOfTrace(t.Trace); ok {
		w.Redirect = "/runs/" + run.ID
	}
	// The page names the task as its card and its run page do.
	card := dockTaskCard(t, store.Run{}, false, v.Now)
	w.Title, w.Class, w.Elapsed, w.Since = card.Title, card.Class, card.Elapsed, card.Since
	w.PillClass = runPillClass(card.Class)
	w.Canceling = card.Canceling
	if card.Stop != nil {
		w.Stop = newRunStop(t, nil, card.Title, v.CSRF, false)
	}
	w.SayKey, w.SayState, w.Say = "w:"+t.ID, card.SayState, card.Say
	w.CSRF, w.Back = v.CSRF, runBack(v.Snap, t)
	if t.Display != "" {
		w.Command = &pgCmd{Label: "Command", Text: t.Display}
	}
	w.RetryID, w.Edit = runRetry(t, card.Class)
	return w
}

// runless reports whether the command of the task writes no event log, so
// that it has no run page: sind exec, which Drain and Resume run.
func runless(t store.Task) bool {
	words := strings.Fields(t.Display)
	i := 1
	if len(words) > i && words[i] == "--realm" {
		i += 2
	}
	return len(words) > i && words[i] == "exec"
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
		s.Fail(w, r, runTaskMiss(v.Snap, id))
		return
	}
	if run, ok := v.Snap.RunOfTrace(t.Trace); ok {
		page.Redirect(w, r, "/runs/"+run.ID)
		return
	}
	v.Page, v.Title, v.Nav, v.Content = "run-task", "Starting "+taskTitle(t, nil), "activity", "page-run-task"
	v.Params.Set("task", id)
	v.Events = s.eventsURL([]string{"run-wait"}, v)
	s.Write(w, r, http.StatusOK, "layout", v)
}

// runTaskMiss says that sind ui holds no task id: it keeps the newest
// tasks that ended, store.MaxEndedTasks of them, until it stops. The run of the same id,
// when the log holds one, and Activity are the ways forward.
func runTaskMiss(sn store.Snapshot, id string) *Miss {
	m := &Miss{
		What: fmt.Sprintf("sind ui holds no task %s.", id),
		Hint: fmt.Sprintf("sind ui keeps the last %d commands that ended, until it stops. Activity lists the runs that the activity log holds.", store.MaxEndedTasks),
	}
	if run, ok := sn.Run(id); ok {
		m.Links = append(m.Links, MissLink{Label: "Run " + id, URL: "/runs/" + url.PathEscape(run.ID)})
	}
	m.Links = append(m.Links, MissLink{Label: "Activity", URL: "/activity"}, missOverview)
	return m
}

// runTaskDismiss answers POST /tasks/{id}/dismiss: it hides the card of a
// task that has ended. The task stays, so that its run page still shows its
// command and Try again. A task that is gone counts as dismissed.
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
		s.env.Store.HideTasks(func(t store.Task) bool { return t.ID == id })
	}
	w.WriteHeader(http.StatusNoContent)
}

// runTaskClear answers POST /tasks/clear, the Clear done of the dock: it
// hides the cards of all tasks that have ended.
func runTaskClear(s *Server, w http.ResponseWriter, _ *http.Request) {
	s.env.Store.HideTasks(taskEnded)
	w.WriteHeader(http.StatusNoContent)
}
