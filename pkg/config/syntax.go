// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// checkSyntax walks the YAML document against the Cluster type and reports
// the first mistake with its line and key path, e.g. `line 7:
// nodes[1].cpu: unknown key (valid: ...)`, and returns the document for
// Parse to decode. The decoder reports neither, and names Go types.
func checkSyntax(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, syntaxError(data, err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		return nil, errors.New("empty configuration")
	}
	return &doc, walk(reflect.TypeFor[Cluster](), doc.Content[0], "")
}

// parserProblems are the problems the YAML parser, rather than its
// scanner, reports. The parser counts lines from 0 and names the line where
// the enclosing block or flow collection begins, or failing that, the line
// of the token it could not take.
var parserProblems = []string{
	"did not find expected ',' or ']'",
	"did not find expected ',' or '}'",
	"did not find expected '-' indicator",
	"did not find expected <document start>",
	"did not find expected key",
	"did not find expected node content",
	"found duplicate %TAG directive",
	"found duplicate %YAML directive",
	"found incompatible YAML document",
	"found undefined tag handle",
}

// positionless are the starts of the YAML parser's messages that name no
// place in the input: an alias without its anchor, and errors decoding the
// input's characters. Its other messages leave out only line 1.
var positionless = []string{
	"unknown anchor", "input error", "invalid ", "incomplete ", "unexpected low surrogate",
	"expected low surrogate", "control characters",
}

// wholeNumber matches a whole number in decimal form.
var wholeNumber = regexp.MustCompile(`^[-+]?[0-9]+$`)

// yamlLine matches the line number the YAML parser puts in front of its
// error messages.
var yamlLine = regexp.MustCompile(`(?s)^line (\d+): (.*)$`)

// syntaxError returns the YAML parser's error err for data, with the line of
// the mistake counted from 1.
func syntaxError(data []byte, err error) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	line := 0
	if m := yamlLine.FindStringSubmatch(msg); m != nil {
		line, _ = strconv.Atoi(m[1])
		msg = m[2]
	}
	switch {
	case line == 0 && slices.ContainsFunc(positionless, func(p string) bool { return strings.HasPrefix(msg, p) }):
		return errors.New(msg)
	case slices.Contains(parserProblems, msg):
		line++
	case line == 0:
		line = 1
	case strings.HasPrefix(msg, "found a tab character"):
		// The scanner names the line where the scalar with the tab
		// begins.
		line = tabLine(data, line)
	}
	// The parser can name the line after the last, where the input ends.
	return fmt.Errorf("line %d: %s", min(line, lineCount(data)), msg)
}

// lineCount returns the number of lines of data, the last one with or
// without its line break.
func lineCount(data []byte) int {
	return bytes.Count(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) + 1
}

// tabLine returns the number of the first line after line (counted from 1)
// with a tab in its indentation, or line if there is none.
func tabLine(data []byte, line int) int {
	lines := strings.Split(string(data), "\n")
	for i, l := range lines[min(line, len(lines)):] {
		if strings.Contains(l[:len(l)-len(strings.TrimLeft(l, " \t"))], "\t") {
			return line + i + 1
		}
	}
	return line
}

// unmarshalerType is the interface of the types that accept more than
// one YAML form (a name or an object).
var unmarshalerType = reflect.TypeFor[yaml.Unmarshaler]()

// field is a key of a struct, as the YAML file spells it.
type field struct {
	key string
	typ reflect.Type
}

// fieldsOf returns the keys of struct type t in declaration order, or nil if
// t has none with a yaml tag.
func fieldsOf(t reflect.Type) []field {
	var fields []field
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		fields = append(fields, field{name, f.Type})
	}
	return fields
}

// describe names the YAML kind of n for an error message.
func describe(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "an object"
	case yaml.SequenceNode:
		return "a list"
	}
	switch n.Tag {
	case "!!int":
		return "a number"
	case "!!float":
		return "a decimal number"
	case "!!bool":
		return "a boolean"
	case "!!timestamp":
		return "a date"
	}
	return "a string"
}

// enum is the values a string type of the config takes, and what an error
// calls one.
type enum struct {
	what   string
	values []string
}

// enums are the string types of the config that take one of a set of
// values. Validate checks them too, for a Cluster built in code.
var enums = map[reflect.Type]enum{
	reflect.TypeFor[Role](): {"role", []string{
		string(RoleController), string(RoleDB), string(RoleAPI), string(RoleSubmitter), string(RoleWorker),
	}},
	reflect.TypeFor[IdentityMode](): {"identity mode", []string{
		string(IdentityLocal), string(IdentityNSSSlurm), string(IdentityClientIDs),
	}},
	reflect.TypeFor[AdminLevel]():  {"admin level", []string{string(AdminOperator), string(AdminAdmin)}},
	reflect.TypeFor[StorageType](): {"storage type", []string{string(StorageVolume), string(StorageHostPath)}},
}

// check reports a value of n that is not one of e's.
func (e enum) check(n *yaml.Node, path string) error {
	if n.Value == "" || slices.Contains(e.values, n.Value) {
		return nil
	}
	hint := ""
	if i := slices.IndexFunc(e.values, func(v string) bool { return strings.EqualFold(v, n.Value) }); i >= 0 {
		hint = fmt.Sprintf("; did you mean %q? %ss are case-sensitive", e.values[i], e.what)
	}
	return errorAt(n, path, "invalid %s %q (valid: %s)%s", e.what, n.Value, strings.Join(e.values, ", "), hint)
}

func errorAt(n *yaml.Node, path, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if path != "" {
		msg = path + ": " + msg
	}
	return fmt.Errorf("line %d: %s", n.Line, msg)
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// walk checks node n against type t, which is at path.
func walk(t reflect.Type, n *yaml.Node, path string) error {
	n = resolve(n)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if n.Tag == "!!null" {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		return walkStruct(t, n, path)
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			return errorAt(n, path, "expected a list, got %s", describe(n))
		}
		for i, item := range n.Content {
			if err := walk(t.Elem(), item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			return errorAt(n, path, "expected an object, got %s", describe(n))
		}
		if err := checkKeys(n, path, nil); err != nil {
			return err
		}
		pairs := pairsOf(n)
		for i := 0; i < len(pairs); i += 2 {
			key, value := pairs[i].Value, resolve(pairs[i+1])
			// Limits take numbers as well as strings, but no
			// booleans or dates.
			if t == reflect.TypeFor[Limits]() && (value.Tag == "!!bool" || value.Tag == "!!timestamp") {
				return errorAt(value, join(path, key), "expected a string or a number, got %s", describe(value))
			}
			if err := walk(t.Elem(), value, join(path, key)); err != nil {
				return err
			}
		}
	case reflect.Int:
		// The decoder reads a whole number too large for an int64 as a
		// decimal one.
		var i int
		switch {
		case n.Tag == "!!int" && n.Decode(&i) == nil:
		case n.Tag == "!!int", n.Tag == "!!float" && wholeNumber.MatchString(n.Value):
			return errorAt(n, path, "%s is out of range", n.Value)
		default:
			return errorAt(n, path, "expected a whole number, got %s", describe(n))
		}
	case reflect.String:
		if n.Kind != yaml.ScalarNode {
			return errorAt(n, path, "expected a string, got %s", describe(n))
		}
		if e, ok := enums[t]; ok {
			return e.check(n, path)
		}
	case reflect.Bool:
		// A quoted value is a string, even "yes", which the decoder
		// would take.
		var b bool
		if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 || n.Decode(&b) != nil {
			return errorAt(n, path, "expected true or false, got %s", describe(n))
		}
	}
	return nil
}

func walkStruct(t reflect.Type, n *yaml.Node, path string) error {
	fields := fieldsOf(t)
	custom := reflect.PointerTo(t).Implements(unmarshalerType)
	if n.Kind == yaml.ScalarNode && custom {
		// A name, mode or role. A space means a list item that got
		// glued to the one above by its indentation.
		if t == reflect.TypeFor[Node]() && strings.ContainsAny(n.Value, " \t") {
			return errorAt(n, path, "%q is not a role; check the indentation of the list", n.Value)
		}
		if t == reflect.TypeFor[Section]() {
			return nil // its content
		}
		// It is the value of the type's first field.
		return walk(fields[0].typ, n, path)
	}
	if t == reflect.TypeFor[Section]() {
		return walk(reflect.TypeFor[map[string]string](), n, path) // fragments
	}
	if n.Kind != yaml.MappingNode {
		return errorAt(n, path, "expected an object, got %s", describe(n))
	}
	if err := checkKeys(n, path, nil); err != nil {
		return err
	}
	if t == reflect.TypeFor[Node]() && !hasKey(n, "role") {
		return walkShorthand(n, path)
	}
	pairs := pairsOf(n)
	for i := 0; i < len(pairs); i += 2 {
		key, value := pairs[i], pairs[i+1]
		j := slices.IndexFunc(fields, func(f field) bool { return f.key == key.Value })
		if j < 0 {
			return unknownKey(fields, key, join(path, key.Value))
		}
		if err := walk(fields[j].typ, value, join(path, key.Value)); err != nil {
			return err
		}
		if err := checkID0(key, value, join(path, key.Value)); err != nil {
			return err
		}
	}
	return nil
}

// checkID0 reports a uid or gid of 0, root's, which the decoder would take
// for one left out, to be assigned.
func checkID0(key, value *yaml.Node, path string) error {
	value = resolve(value)
	if (key.Value != "uid" && key.Value != "gid") || value.Tag != "!!int" {
		return nil
	}
	var id int
	if value.Decode(&id) != nil || id != 0 { // walk reports a number out of range
		return nil
	}
	return errorAt(value, path, "must be between %d and %d, got 0; leave it out for the lowest free ID", MinUID, MaxUID)
}

// walkShorthand checks a node object without a role, which is the
// shorthand "worker: 3" unless it is an object that lacks its role.
func walkShorthand(n *yaml.Node, path string) error {
	fields := fieldsOf(reflect.TypeFor[Node]())
	pairs := pairsOf(n)
	for i := 0; i < len(pairs); i += 2 {
		if strings.EqualFold(pairs[i].Value, "role") {
			return unknownKey(fields, pairs[i], join(path, pairs[i].Value))
		}
	}
	if len(pairs) != 2 {
		return errorAt(n, path, `a node object needs "role: NAME"; the shorthand "NAME: COUNT" takes no other keys`)
	}
	key, v := pairs[0], resolve(pairs[1])
	roles := enums[reflect.TypeFor[Role]()]
	isCount := v.Kind == yaml.ScalarNode && (v.Tag == "!!int" || v.Tag == "!!null")
	isField := slices.ContainsFunc(fields, func(f field) bool { return f.key == key.Value })
	isRole := slices.ContainsFunc(roles.values, func(r string) bool { return strings.EqualFold(r, key.Value) })
	if isField || (!isCount && !isRole) {
		return errorAt(n, path, `a node object needs "role: NAME"`)
	}
	if err := roles.check(key, path); err != nil {
		return err
	}
	if !isCount {
		return errorAt(v, join(path, key.Value), "expected a whole number, got %s", describe(v))
	}
	return nil
}

// resolve follows the alias n to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// isMerge reports whether key is a merge key, <<, whose value is an object
// or a list of objects whose keys the mapping takes on.
func isMerge(key *yaml.Node) bool {
	return key.Tag == "!!merge"
}

// mergeSources returns the objects that the value of a merge key names.
func mergeSources(value *yaml.Node) []*yaml.Node {
	value = resolve(value)
	if value.Kind == yaml.SequenceNode {
		return value.Content
	}
	return []*yaml.Node{value}
}

// checkKeys checks that mapping n, and each object its merge keys name,
// has no key twice, that the merge keys name objects or lists of objects,
// and that no object merges itself; outer holds the objects whose merges
// are being checked.
func checkKeys(n *yaml.Node, path string, outer []*yaml.Node) error {
	outer = append(outer, n)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		for j := 0; j < i; j += 2 {
			if first := n.Content[j]; first.Kind == key.Kind && first.Value == key.Value {
				return errorAt(key, join(path, key.Value), "duplicate key (first on line %d)", first.Line)
			}
		}
		if !isMerge(key) {
			continue
		}
		for _, src := range mergeSources(n.Content[i+1]) {
			src = resolve(src)
			switch {
			case src.Kind != yaml.MappingNode:
				return errorAt(src, join(path, "<<"), "expected an object or a list of objects, got %s", describe(src))
			case slices.Contains(outer, src):
				return errorAt(n.Content[i], join(path, "<<"), "an object cannot merge itself")
			}
			if err := checkKeys(src, path, outer); err != nil {
				return err
			}
		}
	}
	return nil
}

// pairsOf returns the keys and values of mapping n, alternating: its own,
// then those of the objects its merge keys name, which checkKeys has
// checked.
func pairsOf(n *yaml.Node) []*yaml.Node {
	var pairs, merged []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMerge(n.Content[i]) {
			pairs = append(pairs, n.Content[i], n.Content[i+1])
			continue
		}
		for _, src := range mergeSources(n.Content[i+1]) {
			merged = append(merged, pairsOf(resolve(src))...)
		}
	}
	return append(pairs, merged...)
}

// hasKey reports whether mapping n has the key name, itself or by a merge.
func hasKey(n *yaml.Node, name string) bool {
	pairs := pairsOf(n)
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i].Value == name {
			return true
		}
	}
	return false
}

func unknownKey(fields []field, key *yaml.Node, path string) error {
	names := make([]string, len(fields))
	hint := ""
	for i, f := range fields {
		names[i] = f.key
		if strings.EqualFold(f.key, key.Value) {
			hint = fmt.Sprintf(`; did you mean %q? keys are case-sensitive`, f.key)
		}
	}
	return errorAt(key, "", "%s: unknown key (valid: %s)%s", path, strings.Join(names, ", "), hint)
}
