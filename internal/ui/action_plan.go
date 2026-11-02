// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/config"
)

// The plan of a new cluster at the right of its page, and the budget of
// the host that Add workers shows too: what the nodes take of the host,
// the warnings of the Doctor that they reach, the command one argument to
// a line, and the slurm.conf with the user's own lines marked.

// actBar is a bar of what the nodes take of the host: those that run now
// and the new ones, of 1000, and the host or a limit as a mark on it.
type actBar struct {
	Label string
	// NowW and AddW are the widths of the nodes that run now and of the
	// new ones, MarkX where the mark is, -1 for none.
	NowW, AddW, MarkX int
	// Class is high or over, which tints the new part, or "".
	Class string
	// Value is what the bar says in short, Say what it says in full.
	Value, Say string
}

// actBarOf is the bar of now and add of limit, whose scale is the limit,
// or the total once it comes to more; the limit is a mark once it does.
func actBarOf(label string, now, add, limit int) actBar {
	scale := max(limit, now+add, 1)
	b := actBar{Label: label, NowW: now * 1000 / scale, AddW: add * 1000 / scale, MarkX: -1}
	if now+add > limit {
		b.MarkX, b.Class = limit*1000/scale, "over"
	}
	return b
}

// HostBars are the bars of what the nodes of the command take of the
// host: its memory, its CPUs, and the nodes against each limit of the
// Doctor that holds from a number of nodes on; none while sind ui does not
// know the host.
func (p actPreview) HostBars() []actBar {
	b := p.Cmd.Budget
	if b == nil {
		return nil
	}
	mem := actBarOf("Memory", b.NowMB, b.AddMB, b.HostMB)
	if b.High() {
		mem.Class = "high"
	}
	mem.Value = "+" + action.MemoryLabel(b.AddMB) + " of " + action.MemoryLabel(b.HostMB)
	mem.Say = fmt.Sprintf("Memory: the nodes that run now take %s, the new ones %s, of the %s of the host.",
		action.MemoryLabel(b.NowMB), action.MemoryLabel(b.AddMB), action.MemoryLabel(b.HostMB))
	out := []actBar{mem}
	if b.HostCPUs > 0 {
		// Nodes share the CPUs: more than the host has is no problem.
		cpus := actBarOf("CPUs", b.NowCPUs, b.AddCPUs, b.HostCPUs)
		cpus.Class = ""
		cpus.Value = fmt.Sprintf("+%d of %d", b.AddCPUs, b.HostCPUs)
		cpus.Say = fmt.Sprintf("CPUs: the nodes that run now ask for %d, the new ones %d, of the %d of the host. Nodes share the CPUs.", b.NowCPUs, b.AddCPUs, b.HostCPUs)
		out = append(out, cpus)
	}
	for _, l := range b.Limits {
		// The bar of a limit is half as long again as the limit, so that
		// its mark stays in view.
		safe := l.Nodes - 1
		scale := max(safe+safe/2, b.Nodes(), 1)
		bar := actBar{Label: l.Check, NowW: b.NowNodes * 1000 / scale, AddW: b.AddNodes * 1000 / scale, MarkX: safe * 1000 / scale}
		if b.Nodes() > safe {
			bar.Class = "over"
		}
		bar.Value = fmt.Sprintf("%d nodes · max %d", b.Nodes(), safe)
		bar.Say = fmt.Sprintf("%s: the host then runs %s; the Doctor says that %d and more can fail to start.", l.Check, pgPlural(b.Nodes(), "node", "nodes"), l.Nodes)
		out = append(out, bar)
	}
	return out
}

// actHostWarn is a warning of the Doctor that the nodes on the host reach
// with the command, as the plan shows it: in a sentence, with the detail of
// the check and its fix.
type actHostWarn struct {
	// Check is the check of the Doctor.
	Check       string
	Title, Text string
	// Short says it in a line, for the footer of a dialog.
	Short  string
	Detail string
	Steps  []moreFixStep
}

// actHostTitles head the warnings of the checks of the Doctor whose
// warning holds from a number of nodes on.
var actHostTitles = map[string]string{"inotify": "The host can run out of inotify instances"}

// HostWarns are the warnings of the Doctor that the nodes on the host
// reach with the command.
func (p actPreview) HostWarns() []actHostWarn {
	b := p.Cmd.Budget
	if b == nil {
		return nil
	}
	with := "the new workers"
	if p.ID == "create-cluster" {
		with = "the new cluster"
		if name := strings.TrimSpace(p.Form.Get("name")); name != "" && p.ProblemOf("name") == "" {
			with = name
		}
	}
	var out []actHostWarn
	for _, w := range b.Crossed() {
		out = append(out, actHostWarn{
			Check: w.Check,
			Title: cmp.Or(actHostTitles[w.Check], "The Doctor warns of "+w.Check),
			Text: fmt.Sprintf("With %s, this host runs %s. The Doctor says that %d and more can fail to start.",
				with, pgPlural(b.Nodes(), "node", "nodes"), w.Nodes),
			Short:  fmt.Sprintf("With %s, the %s limit of the host can run out.", pgPlural(b.Nodes(), "node", "nodes"), w.Check),
			Detail: w.Detail,
			Steps:  moreFixSteps(w.Remediation),
		})
	}
	return out
}

// actRunLine is a line of the command that the plan shows: an argument,
// or the words before the first, whose last piece ends with a backslash
// but on the last line. Pieces are its text, which a line breaks only
// between words and after a slash of a long path.
type actRunLine struct {
	Pieces []pgPiece
	// More says that a line follows, Sub that the line continues the
	// command.
	More, Sub bool
}

// RunLines are the lines of the command of the plan: sind with its global
// flags, such as --realm, then its words, and each argument with its value
// on a line of its own, as a shell script writes a long command.
func (p actPreview) RunLines() []actRunLine {
	argv := p.Cmd.Argv
	i := 1
	for i+1 < len(argv) && argv[i] == "--realm" {
		i += 2
	}
	groups := [][]string{argv[:i]}
	j := i
	for j < len(argv) && !strings.HasPrefix(argv[j], "-") {
		j++
	}
	if i > 1 {
		// The words go on a line of their own, after the global flags.
		groups = append(groups, argv[i:j])
	} else {
		groups[0] = argv[:j]
	}
	for i = j; i < len(argv); i = j {
		j = i + 1
		if j < len(argv) && !strings.HasPrefix(argv[j], "-") {
			j++
		}
		groups = append(groups, argv[i:j])
	}
	out := make([]actRunLine, len(groups))
	for k, g := range groups {
		words := make([]string, len(g))
		for i, a := range g {
			// A path below home that needs no quotes reads as ~/, as
			// the shell expands it.
			words[i] = string(p.Tilde(action.Quote([]string{a})))
		}
		l := actRunLine{Pieces: pgText(strings.Join(words, " ")).Pieces(), More: k < len(groups)-1, Sub: k > 0}
		if n := len(l.Pieces); l.More && n > 0 {
			// The backslash goes with the last piece, so that a line
			// never wraps before it.
			l.Pieces[n-1].Text += ` \`
		}
		out[k] = l
	}
	return out
}

// Tilde writes a path below the home directory of the user with ~/, as
// the shell takes it.
func (p actPreview) Tilde(path string) pgText {
	if rest, ok := strings.CutPrefix(path, p.Home+"/"); ok && p.Home != "" {
		return pgText("~/" + rest)
	}
	return pgText(path)
}

// actSlashSegs cuts s after each slash.
func actSlashSegs(s string) []string {
	var out []string
	for s != "" {
		i := strings.Index(s, "/")
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// actConfLine is a line of the slurm.conf of the plan; Own marks one that
// the user adds in Slurm config.
type actConfLine struct {
	Text string
	Own  bool
}

// ConfLines are the lines of the slurm.conf of the plan, with the user's
// own lines marked.
func (p actPreview) ConfLines() []actConfLine {
	own := map[string]bool{}
	for _, l := range strings.Split(p.Form.Get("slurm-main"), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			own[l] = true
		}
	}
	lines := strings.Split(strings.TrimSuffix(p.Cmd.SlurmConf, "\n"), "\n")
	out := make([]actConfLine, len(lines))
	for i, l := range lines {
		out[i] = actConfLine{Text: l, Own: own[strings.TrimSpace(l)]}
	}
	return out
}

// ImageShort is the last part of the name of an image.
func (p actPreview) ImageShort(image string) string { return actImageShort(image) }

// actPageOK is the label of the button of the page of a new cluster:
// Create and the name, once the name is right. A long name ends in … on
// the button, which keeps its place beside Cancel (act-okl).
func actPageOK(f action.Form) string {
	if name := strings.TrimSpace(f.Get("name")); name != "" && config.CheckName("cluster", name) == nil {
		return "Create " + name
	}
	return "Create"
}
