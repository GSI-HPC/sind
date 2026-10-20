// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// checkSyntax walks the YAML document against the Cluster type and reports
// the first mistake with its line and key path, e.g. `line 7:
// nodes[1].cpu: unknown key (valid: ...)`. The decoder that follows
// (sigs.k8s.io/yaml) goes through JSON and reports neither, and names Go
// types.
func checkSyntax(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil && !errors.Is(err, io.EOF) {
		return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if doc.Kind == 0 || len(doc.Content) == 0 || doc.Content[0].Tag == "!!null" {
		return errors.New("empty configuration")
	}
	return walk(reflect.TypeFor[Cluster](), doc.Content[0], "")
}

// jsonUnmarshalerType is the interface of the types that accept more than
// one YAML form (a name or an object).
var jsonUnmarshalerType = reflect.TypeFor[interface{ UnmarshalJSON([]byte) error }]()

// field is a key of a struct, as the YAML file spells it.
type field struct {
	key string
	typ reflect.Type
}

// fieldsOf returns the keys of struct type t in declaration order, or nil if
// t has none with a json tag.
func fieldsOf(t reflect.Type) []field {
	var fields []field
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
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
	case "!!int", "!!float":
		return "a number"
	case "!!bool":
		return "a boolean"
	}
	return "a string"
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
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
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
	case reflect.Int:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
			return errorAt(n, path, "expected a whole number, got %s", describe(n))
		}
	case reflect.String:
		if n.Kind != yaml.ScalarNode {
			return errorAt(n, path, "expected a string, got %s", describe(n))
		}
	}
	return nil
}

func walkStruct(t reflect.Type, n *yaml.Node, path string) error {
	fields := fieldsOf(t)
	custom := reflect.PointerTo(t).Implements(jsonUnmarshalerType)
	if n.Kind == yaml.ScalarNode && custom {
		// A name, mode or role. A space means a list item that got
		// glued to the one above by its indentation.
		if t == reflect.TypeFor[Node]() && strings.ContainsAny(n.Value, " \t") {
			return errorAt(n, path, "%q is not a role; check the indentation of the list", n.Value)
		}
		return nil
	}
	if len(fields) == 0 {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return errorAt(n, path, "expected an object, got %s", describe(n))
	}
	if t == reflect.TypeFor[Node]() && !hasKey(n, "role") {
		return walkShorthand(n, path)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if key.Value == "<<" {
			continue
		}
		j := slices.IndexFunc(fields, func(f field) bool { return f.key == key.Value })
		if j < 0 {
			return unknownKey(fields, key, join(path, key.Value))
		}
		if err := walk(fields[j].typ, value, join(path, key.Value)); err != nil {
			return err
		}
	}
	return nil
}

// walkShorthand checks a node of the form "worker: 3".
func walkShorthand(n *yaml.Node, path string) error {
	if len(n.Content) != 2 {
		return errorAt(n, path, `a node object needs "role: NAME"; the shorthand "NAME: COUNT" takes no other keys`)
	}
	if v := n.Content[1]; v.Kind != yaml.ScalarNode || (v.Tag != "!!int" && v.Tag != "!!null") {
		return errorAt(v, join(path, n.Content[0].Value), "expected a whole number, got %s", describe(v))
	}
	return nil
}

func hasKey(n *yaml.Node, name string) bool {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == name {
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

// goTypeError matches the message of a JSON decoding type error.
var goTypeError = regexp.MustCompile(`json: cannot unmarshal (\w+) into Go (?:struct field [\w.\[\]]*?(\w+) |value )?of type (\S+)`)

// friendlyType names a Go type for a config author.
func friendlyType(goType string) string {
	switch {
	case strings.HasPrefix(goType, "[]"), strings.HasPrefix(goType, "map["):
		return "a list or an object"
	case goType == "string":
		return "a string"
	case strings.HasPrefix(goType, "int"), strings.HasPrefix(goType, "uint"):
		return "a whole number"
	case goType == "bool":
		return "true or false"
	}
	return "an object"
}

// jsonKind names the JSON kind of a decoding error for a config author.
func jsonKind(kind string) string {
	switch kind {
	case "object":
		return "an object"
	case "array":
		return "a list"
	case "bool":
		return "a boolean"
	}
	return "a " + kind
}

// plainError rewrites an error of the JSON decoding that checkSyntax does
// not catch: it names JSON, and Go fields and types.
func plainError(err error) error {
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "error unmarshaling JSON: while decoding JSON: ", "")
	msg = goTypeError.ReplaceAllStringFunc(msg, func(m string) string {
		sub := goTypeError.FindStringSubmatch(m)
		want := friendlyType(sub[3])
		got := jsonKind(sub[1])
		hint := ""
		if want == "a string" && got == "a number" {
			hint = " (quote it)"
		}
		if sub[2] != "" {
			return fmt.Sprintf("%q must be %s, got %s%s", sub[2], want, got, hint)
		}
		return fmt.Sprintf("expected %s, got %s%s", want, got, hint)
	})
	return errors.New(strings.TrimPrefix(msg, "json: "))
}
