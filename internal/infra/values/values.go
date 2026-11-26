// Package values reads a site's values files and merges them the way Helm
// does, and edits a site file in place without disturbing anything but the
// value it changes.
package values

import (
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Tree is a decoded YAML mapping. The values are whatever YAML holds
// (strings, numbers, booleans, lists and nested mappings), so this is one of
// the edges where any is the honest type.
type Tree = map[string]any

// Values is the merged values of a release, read through typed getters. Paths
// are dot-separated keys, e.g. image.tag; a key that itself contains a dot
// cannot be reached this way.
type Values struct {
	tree Tree
}

// New wraps an already decoded tree, e.g. in tests.
func New(tree Tree) Values {
	if tree == nil {
		tree = Tree{}
	}

	return Values{tree: tree}
}

// Load reads the files in order and merges them like helm -f a -f b: later
// files win, mappings merge key by key at every depth, and anything else,
// lists included, is replaced whole. An explicit null is kept, so Helm still
// sees it and deletes the chart's default.
func Load(files ...string) (Values, error) {
	merged := Tree{}

	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec // the caller names the values files to read
		if err != nil {
			return Values{}, fmt.Errorf("reading values: %w", err)
		}

		var tree Tree
		err = yaml.Unmarshal(data, &tree)
		if err != nil {
			return Values{}, fmt.Errorf("parsing %s: %w", file, err)
		}

		merged = Merge(merged, tree)
	}

	return Values{tree: merged}, nil
}

// Merge returns base with over merged into it the way Helm merges values
// files. Neither argument is modified.
func Merge(base, over Tree) Tree {
	out := maps.Clone(base)
	if out == nil {
		out = Tree{}
	}

	for k, v := range over {
		if sub, ok := v.(Tree); ok {
			if prev, ok := out[k].(Tree); ok {
				out[k] = Merge(prev, sub)

				continue
			}
		}

		out[k] = v
	}

	return out
}

// Tree returns the merged tree, for handing to Helm. Callers must not modify
// it.
func (v Values) Tree() Tree {
	return v.tree
}

// Lookup returns the raw value at path and whether it is present.
func (v Values) Lookup(path string) (any, bool) {
	var cur any = v.tree

	for key := range strings.SplitSeq(path, ".") {
		m, ok := cur.(Tree)
		if !ok {
			return nil, false
		}

		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}

	return cur, true
}

// Get returns the value at path when it is present and of type T.
//
//nolint:ireturn // T is the caller's concrete type, not an interface
func Get[T any](v Values, path string) (T, bool) {
	raw, ok := v.Lookup(path)
	if !ok {
		var zero T

		return zero, false
	}

	t, ok := raw.(T)

	return t, ok
}

// String returns the scalar at path as a string, the way a template prints
// it: a number or a boolean is formatted, and a missing value, a null, a list
// or a mapping give "".
func (v Values) String(path string) string {
	raw, _ := v.Lookup(path)

	return scalarString(raw)
}

// StringSlice returns the list at path with every scalar item as a string;
// nil when the value is missing or not a list.
func (v Values) StringSlice(path string) []string {
	list, ok := Get[[]any](v, path)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, scalarString(item))
	}

	return out
}

// Bool returns the boolean at path; false when it is missing or not a
// boolean.
func (v Values) Bool(path string) bool {
	b, _ := Get[bool](v, path)

	return b
}

// scalarString formats a YAML scalar.
func scalarString(raw any) string {
	switch s := raw.(type) {
	case string:
		return s
	case int:
		return strconv.Itoa(s)
	case int64:
		return strconv.FormatInt(s, 10)
	case uint64:
		return strconv.FormatUint(s, 10)
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	default:
		return ""
	}
}
