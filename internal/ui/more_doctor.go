// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

func init() {
	defaultRegistry.AddFragment(Fragment{
		Name:   "more-doctor",
		Topics: []store.Topic{store.TopicDoctor, store.TopicDocker},
		Load:   moreDoctorLoad,
	})
	defaultRegistry.AddPage(Page{
		Name: "more-doctor", Pattern: "GET /doctor", Title: "Doctor", Nav: "doctor",
		Fragments: []string{"more-doctor"},
	})
	defaultRegistry.AddRoute(Route{Pattern: "POST /doctor/check", Handle: moreDoctorCheck})
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
	// Verdict says whether sind can create clusters on this host.
	Verdict moreVerdict
}

// moreVerdict is the answer at the top of the Doctor page: Title in bold,
// then Text; Class colours the callout.
type moreVerdict struct {
	Class, Title, Text string
}

// moreCheck is one check as the page draws it.
type moreCheck struct {
	doctor.Check
	// Class colours the mark and the pill; Mark is the glyph.
	Class, Mark string
	// Steps are the remediation, split into sentences and commands.
	Steps []moreFixStep
}

// moreFixStep is a step of a fix: the sentences that say what to do, and
// the command that does it. Either may be empty.
type moreFixStep struct {
	Text []string
	Cmd  string
}

// Multi reports whether the command has more than one line, which the
// page shows without a prompt.
func (s moreFixStep) Multi() bool { return strings.Contains(s.Cmd, "\n") }

// moreFixSteps splits a remediation into its steps, so that Copy takes a
// command without the sentence before it. A remediation is paragraphs
// between blank lines: sentences, and the commands they introduce. A
// paragraph is a command when it starts with a lower-case letter and does
// not end like a sentence, with ":" or "."; the sentences before a command
// are its step's Text.
func moreFixSteps(remediation string) []moreFixStep {
	var steps []moreFixStep
	var text []string
	for _, p := range strings.Split(strings.ReplaceAll(remediation, "\r\n", "\n"), "\n\n") {
		p = moreDedent(p)
		switch {
		case p == "":
		case moreIsCommand(p):
			steps = append(steps, moreFixStep{Text: text, Cmd: p})
			text = nil
		default:
			text = append(text, p)
		}
	}
	if len(text) > 0 {
		steps = append(steps, moreFixStep{Text: text})
	}
	return steps
}

// moreIsCommand reports whether a paragraph of a remediation, which is not
// empty, is a command.
func moreIsCommand(p string) bool {
	return unicode.IsLower([]rune(p)[0]) && !strings.HasSuffix(p, ":") && !strings.HasSuffix(p, ".")
}

// moreDedent removes the indent that the lines of p share, the spaces at
// their ends, and the blank lines around them.
func moreDedent(p string) string {
	lines := strings.Split(p, "\n")
	indent := -1
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
		if lines[i] == "" {
			continue
		}
		if n := len(lines[i]) - len(strings.TrimLeft(lines[i], " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	for i, l := range lines {
		if l != "" {
			lines[i] = l[indent:]
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// moreDoctorVerdict says whether sind can create clusters on this host:
// a failed check stops create and power on, a warning does not.
func moreDoctorVerdict(d moreDoctorData) moreVerdict {
	const (
		ready    = "Ready: sind can create clusters on this host"
		notReady = "Not ready: sind cannot create clusters on this host"
	)
	switch {
	case !d.HasReport && d.Running:
		return moreVerdict{Class: "run", Title: "Checking this host", Text: "The answer shows when the checks end."}
	case !d.HasReport && d.CanRecheck:
		return moreVerdict{Title: "Not checked yet", Text: "Press Check again to learn whether sind can create clusters on this host."}
	case !d.HasReport:
		return moreVerdict{Title: "Not checked yet", Text: "Run sind doctor in a terminal to learn whether sind can create clusters on this host."}
	case d.Failures == 1:
		return moreVerdict{Class: "bad", Title: notReady, Text: "Fix the failed check first. Create and power on stop until it passes."}
	case d.Failures > 1:
		return moreVerdict{Class: "bad", Title: notReady,
			Text: "Fix the " + strconv.Itoa(d.Failures) + " failed checks first. Create and power on stop until they pass."}
	case d.Warnings == 1:
		return moreVerdict{Class: "ok", Title: ready, Text: "The warning does not stop sind, but read its fix."}
	case d.Warnings > 1:
		return moreVerdict{Class: "ok", Title: ready, Text: "The " + strconv.Itoa(d.Warnings) + " warnings do not stop sind, but read their fixes."}
	}
	return moreVerdict{Class: "ok", Title: ready, Text: "Every check passed."}
}

// moreCheckMark returns the class and glyph of a check status.
func moreCheckMark(status doctor.Status) (class, mark string) {
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

// moreDoctorCount is the badge behind the rail entry: the checks that
// failed, as "1 failed", else the warnings, as "2 warnings", or a dot when
// every check passed. Before the first check it shows none.
func moreDoctorCount(sn store.Snapshot, _ time.Time) NavCount {
	if sn.Doctor == nil {
		return NavCount{}
	}
	failed, warned := 0, 0
	for _, c := range sn.Doctor.Checks {
		switch c.Status {
		case doctor.StatusOK:
		case doctor.StatusFailed:
			failed++
		default:
			warned++
		}
	}
	var parts []string
	if failed > 0 {
		parts = append(parts, pgPlural(failed, "host check failed", "host checks failed"))
	}
	if warned > 0 {
		parts = append(parts, pgPlural(warned, "warning", "warnings"))
	}
	label := strings.Join(parts, ", ")
	switch {
	case failed > 0:
		return NavCount{Text: strconv.Itoa(failed) + " failed", Class: "bad", Label: label}
	case warned > 0:
		return NavCount{Text: pgPlural(warned, "warning", "warnings"), Class: "warn", Label: label}
	}
	return NavCount{Class: "ok", Label: "every host check passed"}
}

// moreDoctorLoad fills the data of the Doctor page.
func moreDoctorLoad(v *View) error {
	d := moreDoctorData{DockerErr: moreDockerDown(v.Snap.Docker), CanRecheck: moreEnvGet[func()](ColDoctorKey) != nil}
	if r := v.Snap.Doctor; r != nil {
		d.HasReport, d.Running = !r.Time.IsZero(), r.Running
		if d.HasReport {
			d.Ago = moreAgo(v.Now, r.Time)
		}
		for _, c := range r.Checks {
			mc := moreCheck{Check: c, Steps: moreFixSteps(c.Remediation)}
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
	d.Verdict = moreDoctorVerdict(d)
	v.Data = d
	return nil
}

// moreDoctorCheck runs the host checks again and answers with the fragment
// that shows them; a browser without script is sent back to the page.
func moreDoctorCheck(s *Server, w http.ResponseWriter, r *http.Request) {
	fn, _ := s.env.Get(ColDoctorKey).(func())
	if fn == nil {
		s.Fail(w, r, ErrNotFound)
		return
	}
	fn()
	if !page.IsHTMX(r) {
		http.Redirect(w, r, "/doctor", http.StatusSeeOther)
		return
	}
	s.Fragment(w, r, "more-doctor")
}
