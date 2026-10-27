// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestShotListOfTheRepo checks the list that the docs use: it parses, and
// takes every kind of page the task asks for.
func TestShotListOfTheRepo(t *testing.T) {
	l, err := loadShots("shots.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	phone, dialog, help, midRun := 0, 0, 0, 0
	for _, s := range l.Shots {
		names = append(names, s.Name)
		if slices.Contains(s.Sizes, "phone") {
			phone++
		}
		for _, st := range s.Steps {
			switch {
			case st.Dialog != "":
				dialog++
			case strings.Contains(st.Click, "help"):
				help++
			}
		}
		if s.Path == "/runs/d3e0000000000063" {
			midRun++
		}
	}
	for _, want := range []string{"overview", "clusters", "cluster", "nodes", "node", "mesh", "monitoring", "topology", "doctor", "activity", "run-going"} {
		if !slices.Contains(names, want) {
			t.Errorf("no shot %q in %v", want, names)
		}
	}
	if phone == 0 || dialog == 0 || help == 0 || midRun == 0 {
		t.Errorf("phone %d, dialog %d, help %d, mid-run %d: every kind needs a shot", phone, dialog, help, midRun)
	}
}

func TestShotListChecks(t *testing.T) {
	sizes := map[string]Size{"desktop": {1440, 900}}
	for name, c := range map[string]struct {
		shots Shots
		want  string
	}{
		"no shots":     {Shots{Sizes: sizes}, "no shots"},
		"no path":      {Shots{Sizes: sizes, Shots: []Shot{{Name: "a", Alt: "A"}}}, "needs a name, a path and an alt"},
		"twice":        {Shots{Sizes: sizes, Shots: []Shot{{Name: "a", Alt: "A", Path: "/"}, {Name: "a", Alt: "A", Path: "/"}}}, "listed twice"},
		"unknown size": {Shots{Sizes: sizes, Shots: []Shot{{Name: "a", Alt: "A", Path: "/", Sizes: []string{"tv"}}}}, `no size "tv"`},
		"two things":   {Shots{Sizes: sizes, Shots: []Shot{{Name: "a", Alt: "A", Path: "/", Steps: []Step{{Click: "x", Wait: "y"}}}}}, "exactly one"},
		"type alone":   {Shots{Sizes: sizes, Shots: []Shot{{Name: "a", Alt: "A", Path: "/", Steps: []Step{{Type: "x"}}}}}, "go together"},
	} {
		err := c.shots.check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, c.want)
		}
	}
}

func TestLoadShots_Errors(t *testing.T) {
	if _, err := loadShots(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("a missing file loaded")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"unknown.yaml": "sizes: {}\nshots: []\nbogus: 1\n",
		"empty.yaml":   "sizes: {}\nshots: []\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadShots(path); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: got %v, want an error that names the file", name, err)
		}
	}
}

func TestShotFiles(t *testing.T) {
	s := Shot{Name: "overview", Sizes: []string{"desktop", "phone"}}
	got := strings.Join(s.Files(), " ")
	want := "overview-light.png overview-dark.png overview-phone-light.png overview-phone-dark.png"
	if got != want {
		t.Errorf("files are %q, want %q", got, want)
	}
	def := Shot{Name: "a"}
	if got := def.WindowSizes(); len(got) != 1 || got[0] != "desktop" {
		t.Errorf("the default window is %v", got)
	}
}

func TestOptions(t *testing.T) {
	var n names
	_ = n.Set("a")
	_ = n.Set("b")
	if n.String() != "a,b" {
		t.Errorf("names are %q", n.String())
	}
	if got := strings.Join(uiArgs(8080), " "); got != "ui --demo --still --port 8080" {
		t.Errorf("sind runs as %q", got)
	}
	if u, ok := urlOf("sind ui: http://127.0.0.1:1/?token=x"); !ok || u != "http://127.0.0.1:1/?token=x" {
		t.Errorf("url is %q, %v", u, ok)
	}
	if _, ok := urlOf("Press Ctrl-C to stop."); ok {
		t.Error("another line is no URL")
	}
	if _, err := time.Parse(time.RFC3339, epoch); err != nil {
		t.Errorf("the epoch %q: %v", epoch, err)
	}
}

func TestRun_Errors(t *testing.T) {
	for name, args := range map[string][]string{
		"no out":          {},
		"unexpected":      {"-out", "x", "y"},
		"bad epoch":       {"-out", "x", "-epoch", "yesterday"},
		"missing shots":   {"-out", "x", "-shots", "/nonexistent.yaml"},
		"unknown flag":    {"-bogus"},
		"no such browser": {"-out", "x", "-chromium", ""},
	} {
		t.Setenv("PATH", "")
		t.Setenv("CHROMIUM", "")
		if err := run(args); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestFindChromium(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("CHROMIUM", "")
	if _, err := findChromium(""); err == nil {
		t.Error("found a browser on an empty PATH")
	}
	if got, err := findChromium("/opt/chrome"); err != nil || got != "/opt/chrome" {
		t.Errorf("a named browser is %q, %v", got, err)
	}
	t.Setenv("CHROMIUM", "/env/chrome")
	if got, err := findChromium(""); err != nil || got != "/env/chrome" {
		t.Errorf("$CHROMIUM is %q, %v", got, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "chromium-browser")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // G306: a test double
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM", "")
	t.Setenv("PATH", dir)
	if got, err := findChromium(""); err != nil || got != path {
		t.Errorf("PATH holds %q, found %q, %v", path, got, err)
	}
}
