// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Shots is the shot list: shots.yaml next to this tool.
type Shots struct {
	// Sizes are the browser windows by name.
	Sizes map[string]Size `yaml:"sizes"`
	Shots []Shot          `yaml:"shots"`
}

// Size is the size of a browser window in CSS pixels.
type Size struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

// Shot is one page, tab or dialog, taken in each of its themes and sizes.
type Shot struct {
	// Name names the images: NAME-light.png, NAME-dark.png, and
	// NAME-phone-light.png for the size phone.
	Name string `yaml:"name"`
	// Alt is the text that stands for the image in the docs.
	Alt string `yaml:"alt"`
	// Path is the page to open, with its query.
	Path string `yaml:"path"`
	// Steps run on the page, in order, before the shot.
	Steps []Step `yaml:"steps"`
	// Crop is the selector of the element to take instead of the window.
	Crop string `yaml:"crop"`
	// Hide is the selector of the elements to hide before the shot, such as
	// the task dock, which would cover the bottom of a page.
	Hide string `yaml:"hide"`
	// Fit shortens the window to the page when the page is shorter.
	Fit bool `yaml:"fit"`
	// Page names the page of the UI that the shot shows;
	// the screenshot tool ignores it.
	Page string `yaml:"page"`
	// Sizes name the windows to take it in; desktop when empty.
	Sizes []string `yaml:"sizes"`
}

// Step is one thing to do on a page; exactly one field is set.
type Step struct {
	// Click clicks the first element that matches the selector.
	Click string `yaml:"click"`
	// Dialog opens a dialog as its button does: the action ID, with the
	// query of the link when it has one, as in power?cluster=dev&op=reboot.
	Dialog string `yaml:"dialog"`
	// Wait waits until an element matches the selector.
	Wait string `yaml:"wait"`
	// Type types the text into the element named by Into.
	Type string `yaml:"type"`
	Into string `yaml:"into"`
}

// Themes are the colour schemes every shot is taken in.
var Themes = []string{"light", "dark"}

// loadShots reads the shot list and checks it.
func loadShots(path string) (Shots, error) {
	var l Shots
	data, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil {
		return l, fmt.Errorf("%s: %w", path, err)
	}
	if err := l.check(); err != nil {
		return l, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// check reports what is wrong with the list.
func (l Shots) check() error {
	if len(l.Shots) == 0 {
		return fmt.Errorf("no shots")
	}
	seen := map[string]bool{}
	for _, s := range l.Shots {
		switch {
		case s.Name == "" || s.Path == "" || s.Alt == "":
			return fmt.Errorf("shot %q needs a name, a path and an alt", s.Name)
		case seen[s.Name]:
			return fmt.Errorf("shot %q is listed twice", s.Name)
		}
		seen[s.Name] = true
		for _, sz := range s.WindowSizes() {
			if _, ok := l.Sizes[sz]; !ok {
				return fmt.Errorf("shot %q: no size %q", s.Name, sz)
			}
		}
		for i, st := range s.Steps {
			if st.n() != 1 {
				return fmt.Errorf("shot %q, step %d: exactly one of click, dialog, wait and type", s.Name, i+1)
			}
			if (st.Type != "") != (st.Into != "") {
				return fmt.Errorf("shot %q, step %d: type and into go together", s.Name, i+1)
			}
		}
	}
	return nil
}

// n counts the fields that are set.
func (st Step) n() int {
	n := 0
	for _, f := range []string{st.Click, st.Dialog, st.Wait, st.Type} {
		if f != "" {
			n++
		}
	}
	return n
}

// WindowSizes returns the names of the windows the shot is taken in.
func (s Shot) WindowSizes() []string {
	if len(s.Sizes) == 0 {
		return []string{"desktop"}
	}
	return s.Sizes
}

// Files returns the names of the images of the shot.
func (s Shot) Files() []string {
	var out []string
	for _, sz := range s.WindowSizes() {
		for _, th := range Themes {
			out = append(out, s.File(sz, th))
		}
	}
	return out
}

// File returns the name of the image of the shot in a window and theme.
func (s Shot) File(size, theme string) string {
	if size == "desktop" {
		return s.Name + "-" + theme + ".png"
	}
	return s.Name + "-" + size + "-" + theme + ".png"
}
