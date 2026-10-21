// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strconv"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/doctor"
)

// moreDoctorKey is the Env key of the func that runs the host checks again.
const moreDoctorKey = "collect.doctor"

func init() {
	RegisterFragment(Fragment{
		Name:   "more-doctor",
		Topics: []store.Topic{store.TopicDoctor, store.TopicDocker},
		Load:   moreDoctorLoad,
	})
	RegisterPage(Page{
		Name: "more-doctor", Pattern: "GET /doctor", Title: "Doctor", Nav: "doctor",
		Fragments: []string{"more-doctor"},
	})
	RegisterRoute(Route{Pattern: "POST /doctor/check", Handle: moreDoctorCheck})
}

// moreDoctorData is what the Doctor page shows.
type moreDoctorData struct {
	// HasReport is false before the first check ended.
	HasReport bool
	Running   bool
	// Ago says when the last check ended.
	Ago    string
	Checks []moreCheck
	// Counts of the checks by status.
	Warnings, Failures int
	// DockerErr is why Docker is unreachable.
	DockerErr string
	// CanRecheck is set when something runs the checks on request.
	CanRecheck bool
}

// moreCheck is one check as the page draws it.
type moreCheck struct {
	doctor.Check
	// Class colours the mark and the pill; Mark is the glyph.
	Class, Mark string
}

// moreDoctorFunc returns the func that runs the host checks, nil when no
// service provides it.
func moreDoctorFunc() func() {
	env := moreEnv.Load()
	if env == nil {
		return nil
	}
	fn, _ := env.Get(moreDoctorKey).(func())
	return fn
}

// moreCheckMark returns the class and glyph of a check status.
func moreCheckMark(status string) (class, mark string) {
	switch status {
	case doctor.StatusOK:
		return "ok", "✓"
	case doctor.StatusWarning:
		return "warn", "!"
	case doctor.StatusFailed:
		return "bad", "✗"
	}
	return "", "?"
}

// moreDoctorCount is the count behind the rail entry: the checks that did
// not pass, red when one failed.
func moreDoctorCount(sn store.Snapshot) NavCount {
	if sn.Doctor == nil {
		return NavCount{}
	}
	n, class := 0, ""
	for _, c := range sn.Doctor.Checks {
		if c.Status == doctor.StatusOK {
			continue
		}
		n++
		if c.Status == doctor.StatusFailed {
			class = "bad"
		}
	}
	if n == 0 {
		return NavCount{}
	}
	return NavCount{Text: strconv.Itoa(n), Class: class}
}

// moreDoctorLoad fills the data of the Doctor page.
func moreDoctorLoad(v *View) error {
	d := moreDoctorData{DockerErr: moreDockerDown(v.Snap.Docker), CanRecheck: moreDoctorFunc() != nil}
	if r := v.Snap.Doctor; r != nil {
		d.HasReport, d.Running = !r.Time.IsZero(), r.Running
		if d.HasReport {
			d.Ago = moreAgo(v.Now, r.Time)
		}
		for _, c := range r.Checks {
			mc := moreCheck{Check: doctor.Check(c)}
			mc.Class, mc.Mark = moreCheckMark(c.Status)
			switch c.Status {
			case doctor.StatusWarning:
				d.Warnings++
			case doctor.StatusFailed:
				d.Failures++
			}
			d.Checks = append(d.Checks, mc)
		}
	}
	v.Data = d
	return nil
}

// moreDoctorCheck runs the host checks again and answers with the fragment
// that shows them; a browser without script is sent back to the page.
func moreDoctorCheck(s *Server, w http.ResponseWriter, r *http.Request) {
	fn, _ := s.Env().Get(moreDoctorKey).(func())
	if fn == nil {
		s.Fail(w, r, ErrNotFound)
		return
	}
	fn()
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/doctor", http.StatusSeeOther)
		return
	}
	s.Fragment(w, r, "more-doctor")
}
