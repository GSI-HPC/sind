// SPDX-License-Identifier: LGPL-3.0-or-later

// Package mcpconfig edits the MCP server list in an editor's configuration
// file (Claude Desktop, VS Code, Cursor) in place. The file is parsed as
// JSON with comments and trailing commas (JSONC, as VS Code writes it), and
// only the server entry being changed is rewritten: other settings, other
// servers, comments and formatting stay as they were.
package mcpconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/afero"
	"github.com/tailscale/hujson"

	"github.com/GSI-HPC/sind/internal/atomicfile"
)

// defaultIndent indents a file that has no members to copy from.
const defaultIndent = "  "

// maxSymlinks bounds the symbolic links Load follows, as the kernel does.
const maxSymlinks = 40

// File is an editor configuration file loaded for editing.
type File struct {
	fs   afero.Fs
	path string // the file itself, after symbolic links
	key  string // the member that holds the servers: mcpServers or servers

	exists  bool
	mode    fs.FileMode
	orig    []byte
	root    hujson.Value
	newline string // the file's line break: \r\n if it has one, else \n
}

// Server is an entry of a configuration's server list.
type Server struct {
	Name    string            `json:"-"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Load reads the configuration file at path, whose servers are the members
// of the object key. A file that does not exist, or holds only whitespace,
// loads as an empty configuration. A symbolic link is followed, so that
// Save writes the file it points to rather than replacing the link.
func Load(fsys afero.Fs, path, key string) (*File, error) {
	path, err := resolve(fsys, path)
	if err != nil {
		return nil, err
	}
	f := &File{fs: fsys, path: path, key: key, mode: 0o600, newline: "\n"}
	data, err := afero.ReadFile(fsys, path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	default:
		info, err := fsys.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		f.exists, f.mode, f.orig = true, info.Mode().Perm(), bytes.Clone(data)
	}
	if bytes.Contains(data, []byte("\r\n")) {
		f.newline = "\r\n"
	}
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}\n")
	}
	f.root, err = hujson.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if _, ok := f.root.Value.(*hujson.Object); !ok {
		return nil, fmt.Errorf("parsing %s: not a JSON object", path)
	}
	// An editor reads the last of two members of the same name, while
	// Set and Remove would change the first.
	if duplicate(f.object()) == key {
		return nil, fmt.Errorf("parsing %s: %q is defined more than once", path, key)
	}
	if m := member(f.object(), key); m != nil {
		obj, ok := m.Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("parsing %s: %q is not an object", path, key)
		}
		if name := duplicate(obj); name != "" {
			return nil, fmt.Errorf("parsing %s: server %q is defined more than once in %q", path, name, key)
		}
	}
	return f, nil
}

// duplicate returns the first member name that obj has more than once, or
// "" if it has none.
func duplicate(obj *hujson.Object) string {
	seen := make(map[string]bool, len(obj.Members))
	for _, m := range obj.Members {
		name := m.Name.Value.(hujson.Literal).String()
		if seen[name] {
			return name
		}
		seen[name] = true
	}
	return ""
}

// resolve follows the symbolic links of path, if fsys has any, to the file
// they point to, which need not exist.
func resolve(fsys afero.Fs, path string) (string, error) {
	lstater, ok := fsys.(afero.Lstater)
	reader, _ := fsys.(afero.LinkReader)
	if !ok || reader == nil {
		return path, nil
	}
	for range maxSymlinks {
		info, _, err := lstater.LstatIfPossible(path)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && info.Mode()&fs.ModeSymlink == 0) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", path, err)
		}
		target, err := reader.ReadlinkIfPossible(path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", path, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", fmt.Errorf("reading %s: too many levels of symbolic links", path)
}

// Path returns the path of the file, after symbolic links.
func (f *File) Path() string {
	return f.path
}

// object returns the configuration's top-level object.
func (f *File) object() *hujson.Object {
	return f.root.Value.(*hujson.Object)
}

// servers returns the object that holds the servers, or nil.
func (f *File) servers() *hujson.Object {
	if m := member(f.object(), f.key); m != nil {
		return m.Value.Value.(*hujson.Object)
	}
	return nil
}

// Servers returns the configured servers, in the order of the file. An
// entry that is not an object, or whose fields have other types, is listed
// by its name alone.
func (f *File) Servers() []Server {
	obj := f.servers()
	if obj == nil {
		return nil
	}
	servers := make([]Server, 0, len(obj.Members))
	for _, m := range obj.Members {
		s := Server{}
		v := m.Value.Clone()
		v.Standardize()
		if json.Unmarshal(v.Pack(), &s) != nil {
			s = Server{}
		}
		s.Name = m.Name.Value.(hujson.Literal).String()
		servers = append(servers, s)
	}
	return servers
}

// Server returns the entry name, as Servers lists it, and whether there
// is one.
func (f *File) Server(name string) (Server, bool) {
	for _, s := range f.Servers() {
		if s.Name == name {
			return s, true
		}
	}
	return Server{}, false
}

// Set adds the server s under its name, or replaces the entry that has
// that name. A replaced entry keeps the members that Server has no field
// for, such as "disabled" or "autoApprove", in their place, and the
// comments around it stay.
func (f *File) Set(s Server) {
	obj := f.servers()
	var m *hujson.ObjectMember
	if obj != nil {
		m = member(obj, s.Name)
	}
	indent, lines := f.layout(obj, 2)
	var old *hujson.Object
	if m != nil {
		indent, lines = indentOf(m.Name.BeforeExtra), multiline(m.Name.BeforeExtra)
		old, _ = m.Value.Value.(*hujson.Object)
	}
	value := f.render(s, old, indent, lines)
	if m != nil {
		m.Value.Value = value.Value
		return
	}
	if obj == nil {
		indent, lines := f.layout(f.object(), 1)
		f.insert(f.object(), "", 1, f.key, hujson.Value{Value: &hujson.Object{}}, indent, lines)
		obj = f.servers()
	}
	f.insert(obj, "/"+pointerToken(f.key), 2, s.Name, value, indent, lines)
}

// serverMembers are the members of an entry that the fields of Server
// hold: Set writes them as s has them, and leaves out those s leaves empty.
var serverMembers = []string{"type", "command", "args", "url", "env"}

// Remove removes the server entry name and reports whether there was one.
func (f *File) Remove(name string) bool {
	obj := f.servers()
	if obj == nil || member(obj, name) == nil {
		return false
	}
	closing := indentOf(obj.AfterExtra)
	ptr := "/" + pointerToken(f.key) + "/" + pointerToken(name)
	f.patch("remove", ptr, nil)
	// The line break and indentation before the entry are left before the
	// closing brace when the entry was the last one.
	after := bytes.TrimRight(obj.AfterExtra, " \t")
	switch {
	case len(obj.Members) == 0 && len(bytes.TrimSpace(after)) == 0:
		obj.AfterExtra = nil
	case bytes.HasSuffix(after, []byte("\n")):
		obj.AfterExtra = slices.Concat(after, []byte(closing))
	}
	return true
}

// layout returns how a new member of obj, whose members are at depth (1
// for the top-level object), is laid out: like the members obj has, on
// lines of their own, indented as they are, or, when they share a line, on
// the same line. A nil obj is a new one.
func (f *File) layout(obj *hujson.Object, depth int) (indent string, lines bool) {
	if obj == nil || len(obj.Members) == 0 {
		return strings.Repeat(f.indentUnit(), depth), multilineFile(f.object())
	}
	return indentOf(obj.Members[len(obj.Members)-1].Name.BeforeExtra), multiline(obj.Members[0].Name.BeforeExtra)
}

// insert adds the member name with value to obj, which ptr points to and
// whose members are at depth, at the end, laid out as layout says.
func (f *File) insert(obj *hujson.Object, ptr string, depth int, name string, value hujson.Value, indent string, lines bool) {
	n := len(obj.Members)
	trailingComma := n > 0 && obj.Members[n-1].Value.AfterExtra != nil
	sameLineValue := n > 0 && len(obj.Members[n-1].Value.BeforeExtra) == 0
	f.patch("add", ptr+"/"+pointerToken(name), value.Value)
	m := &obj.Members[n]
	m.Value.BeforeExtra = hujson.Extra(" ")
	if sameLineValue {
		m.Value.BeforeExtra = nil
	}
	if trailingComma {
		m.Value.AfterExtra = hujson.Extra{}
	}
	if !lines {
		return
	}
	// The extras are slices of the parsed file: an append in place could
	// overwrite what follows them.
	before := bytes.TrimRight(m.Name.BeforeExtra, " \t")
	if !bytes.HasSuffix(before, []byte("\n")) {
		before = slices.Concat(before, []byte(f.newline))
	}
	m.Name.BeforeExtra = slices.Concat(before, []byte(indent))
	if !bytes.ContainsRune(obj.AfterExtra, '\n') {
		obj.AfterExtra = slices.Concat(bytes.TrimRight(obj.AfterExtra, " \t"), []byte(f.newline+strings.Repeat(f.indentUnit(), depth-1)))
	}
	// The patch leaves a \n before the closing brace, even where the file
	// had \r\n.
	if f.newline != "\n" {
		lf := bytes.ReplaceAll(obj.AfterExtra, []byte("\r\n"), []byte("\n"))
		obj.AfterExtra = bytes.ReplaceAll(lf, []byte("\n"), []byte(f.newline))
	}
}

// patch applies a JSON Patch operation, which keeps the comments around
// the value it adds or removes. Set and Remove point it at members they
// found or added, so it cannot fail.
func (f *File) patch(op, ptr string, value hujson.ValueTrimmed) {
	p := `[{"op":` + strconv.Quote(op) + `,"path":` + strconv.Quote(ptr)
	if value != nil {
		p += `,"value":` + hujson.Value{Value: value}.String()
	}
	if err := f.root.Patch([]byte(p + "}]")); err != nil {
		panic(fmt.Sprintf("mcpconfig: %s %s: %v", op, ptr, err))
	}
}

// render marshals s as JSON, with the members of old, the entry s
// replaces, that Server has no field for, in their place, and those of s
// after them: indented with the file's indentation after the prefix
// indent, and with its line breaks, if lines is set, or on one line. A Server always marshals, to
// JSON that always parses, and the members of old, standardized, are JSON.
func (f *File) render(s Server, old *hujson.Object, indent string, lines bool) hujson.Value {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	v, _ := hujson.Parse(bytes.TrimSpace(buf.Bytes()))
	if old != nil {
		v = merge(old, v.Value.(*hujson.Object))
	}
	buf.Reset()
	if lines {
		_ = json.Indent(&buf, v.Pack(), indent, f.indentUnit())
	} else {
		buf.Write(v.Pack())
	}
	v, _ = hujson.Parse(bytes.ReplaceAll(buf.Bytes(), []byte("\n"), []byte(f.newline)))
	return v
}

// merge returns the members of old that Server has no field for, in their
// place, and those of s there and after them, without comments.
func merge(old, s *hujson.Object) hujson.Value {
	merged := &hujson.Object{}
	added := map[string]bool{}
	for _, m := range old.Members {
		name := m.Name.Value.(hujson.Literal).String()
		if !slices.Contains(serverMembers, name) {
			v := m.Value.Clone()
			v.Standardize()
			merged.Members = append(merged.Members, hujson.ObjectMember{Name: hujson.Value{Value: m.Name.Value}, Value: v})
		} else if sm := member(s, name); sm != nil && !added[name] {
			merged.Members = append(merged.Members, *sm)
			added[name] = true
		}
	}
	for _, m := range s.Members {
		if !added[m.Name.Value.(hujson.Literal).String()] {
			merged.Members = append(merged.Members, m)
		}
	}
	return hujson.Value{Value: merged}
}

// indentUnit returns the indentation of the file's first top-level member,
// or defaultIndent if it has none on a line of its own.
func (f *File) indentUnit() string {
	if obj := f.object(); len(obj.Members) > 0 {
		if extra := obj.Members[0].Name.BeforeExtra; multiline(extra) && indentOf(extra) != "" {
			return indentOf(extra)
		}
	}
	return defaultIndent
}

// Save writes the configuration back unless it is unchanged. Before it
// changes a file that exists, it copies the file to a new backup next to
// it, named after the time now, and returns the backup's path. The file is
// replaced in one step, through a temporary file and a rename, and keeps
// its permissions; a new file is readable by its owner alone, as its
// entries can hold secrets in their environment.
func (f *File) Save(now time.Time) (backup string, err error) {
	data := f.root.Pack()
	if f.exists && bytes.Equal(data, f.orig) {
		return "", nil
	}
	dir := filepath.Dir(f.path)
	if f.exists {
		if backup, err = f.backup(now); err != nil {
			return "", err
		}
	} else if err := f.fs.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("writing %s: %w", f.path, err)
	}
	if err := atomicfile.Write(f.fs, f.path, data, f.mode); err != nil {
		return backup, fmt.Errorf("writing %s: %w", f.path, err)
	}
	f.exists, f.orig = true, data
	return backup, nil
}

// backup copies the file as it was loaded to <path>.<time>.bak, or
// <path>.<time>-<n>.bak if that exists: a backup is never overwritten.
func (f *File) backup(now time.Time) (string, error) {
	stamp := now.Format("20060102-150405")
	for n := 0; ; n++ {
		name := f.path + "." + stamp + ".bak"
		if n > 0 {
			name = fmt.Sprintf("%s.%s-%d.bak", f.path, stamp, n)
		}
		out, err := f.fs.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("backing up %s: %w", f.path, err)
		}
		_, err = out.Write(f.orig)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = f.fs.Remove(name)
			return "", fmt.Errorf("backing up %s: %w", f.path, err)
		}
		return name, nil
	}
}

// member returns the member of obj named name, or nil.
func member(obj *hujson.Object, name string) *hujson.ObjectMember {
	for i := range obj.Members {
		if obj.Members[i].Name.Value.(hujson.Literal).String() == name {
			return &obj.Members[i]
		}
	}
	return nil
}

// multiline reports whether the extra before a member ends a line, i.e.
// whether the member starts a line of its own.
func multiline(extra hujson.Extra) bool {
	return bytes.ContainsRune(extra, '\n')
}

// multilineFile reports whether the top-level object has its members on
// lines of their own, as an empty one is given them.
func multilineFile(obj *hujson.Object) bool {
	return len(obj.Members) == 0 || multiline(obj.Members[0].Name.BeforeExtra)
}

// indentOf returns the spaces and tabs a member is indented by: those after
// the last line break of the extra before it.
func indentOf(extra hujson.Extra) string {
	line := extra[bytes.LastIndexByte(extra, '\n')+1:]
	return string(line[len(bytes.TrimRight(line, " \t")):])
}

// pointerToken escapes a member name for a JSON Pointer (RFC 6901).
func pointerToken(name string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
}
