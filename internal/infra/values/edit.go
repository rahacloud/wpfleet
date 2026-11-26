package values

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// ErrUnsupportedLayout is returned when a site file is laid out in a way
// SetImage will not edit byte for byte, e.g. an image tag written as a block
// scalar or through an anchor.
var ErrUnsupportedLayout = errors.New("cannot edit this layout in place")

// ErrChangedOnDisk is returned when a site file changed between planning an
// edit and writing it.
var ErrChangedOnDisk = errors.New("the site file changed since it was read")

// ErrEmptyImage is returned when SetImage gets an empty repository or tag.
var ErrEmptyImage = errors.New("image repository and tag must not be empty")

// Keys of the image block.
const (
	keyImage      = "image"
	keyRepository = "repository"
	keyTag        = "tag"
)

// defaultIndent is the indentation of a new nested key when the file gives no
// example of its own.
const defaultIndent = 2

// SetImage sets image.repository and image.tag in the site file at path. Only
// the bytes of the two values change: comments, key order, quoting and every
// other line stay as they are. A missing key is added under image:, and a
// missing image: block is appended at the end of the file. It is PlanImage
// followed by Write.
func SetImage(path, repository, tag string) error {
	e, err := PlanImage(path, repository, tag)
	if err != nil {
		return err
	}

	return e.Write()
}

// ImageEdit is the edit SetImage makes to one site file, computed but not yet
// written, so that a caller editing several files can refuse them all before
// it writes any.
type ImageEdit struct {
	// Path is the site file.
	Path string
	// Before is the file as it was read; After is what it becomes.
	Before, After []byte
}

// PlanImage reads the site file at path and computes the edit that sets
// image.repository and image.tag, refusing with ErrUnsupportedLayout a layout
// it cannot edit in place. It writes nothing.
func PlanImage(path, repository, tag string) (ImageEdit, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller names the site file to edit
	if err != nil {
		return ImageEdit{}, fmt.Errorf("reading site file: %w", err)
	}

	out, err := setImage(data, repository, tag)
	if err != nil {
		return ImageEdit{}, fmt.Errorf("%s: %w", path, err)
	}

	return ImageEdit{Path: path, Before: data, After: out}, nil
}

// Changed reports whether the edit changes the file.
func (e ImageEdit) Changed() bool {
	return !bytes.Equal(e.Before, e.After)
}

// Write writes the edited file, unless the edit changes nothing. It refuses
// with ErrChangedOnDisk a file that changed since PlanImage read it.
func (e ImageEdit) Write() error {
	if !e.Changed() {
		return nil
	}

	current, err := os.ReadFile(e.Path)
	if err != nil {
		return fmt.Errorf("reading site file: %w", err)
	}

	if !bytes.Equal(current, e.Before) {
		return fmt.Errorf("%s: %w", e.Path, ErrChangedOnDisk)
	}

	return writeFileAtomic(e.Path, e.After)
}

// writeFileAtomic replaces path with data through a temporary file in the same
// directory, keeping the file's mode.
func writeFileAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("reading site file mode: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing site file: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, werr := tmp.Write(data)
	cerr := tmp.Close()

	err = errors.Join(werr, cerr)
	if err != nil {
		return fmt.Errorf("writing site file: %w", err)
	}

	err = os.Chmod(tmp.Name(), info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("writing site file: %w", err)
	}

	err = os.Rename(tmp.Name(), path)
	if err != nil {
		return fmt.Errorf("writing site file: %w", err)
	}

	return nil
}

// edit replaces the bytes [start, end) of the source with text.
type edit struct {
	start, end int
	text       string
}

// editor turns node positions into byte offsets of one source.
type editor struct {
	src        []byte
	lineStarts []int
	edits      []edit
}

func newEditor(src []byte) *editor {
	starts := []int{0}

	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}

	return &editor{src: src, lineStarts: starts, edits: nil}
}

// offset turns a 1-based line and a 1-based column counted in characters, as
// the YAML parser reports them, into a byte offset.
func (e *editor) offset(line, column int) (int, error) {
	if line < 1 || line > len(e.lineStarts) {
		return 0, fmt.Errorf("%w: line %d is outside the file", ErrUnsupportedLayout, line)
	}

	off := e.lineStarts[line-1]
	for range column - 1 {
		if off >= len(e.src) || e.src[off] == '\n' {
			return 0, fmt.Errorf("%w: column %d is outside line %d", ErrUnsupportedLayout, column, line)
		}

		_, size := utf8.DecodeRune(e.src[off:])
		off += size
	}

	return off, nil
}

// lineEnd is the offset just past the newline ending the line of off, or the
// end of the source.
func (e *editor) lineEnd(off int) int {
	if i := bytes.IndexByte(e.src[off:], '\n'); i >= 0 {
		return off + i + 1
	}

	return len(e.src)
}

// apply returns the source with every edit made, last first so the offsets
// stay valid.
func (e *editor) apply() []byte {
	slices.SortFunc(e.edits, func(a, b edit) int { return b.start - a.start })

	out := slices.Clone(e.src)
	for _, ed := range e.edits {
		out = slices.Concat(out[:ed.start], []byte(ed.text), out[ed.end:])
	}

	return out
}

// replaceScalar records an edit that writes value over the scalar node,
// keeping its quoting style.
func (e *editor) replaceScalar(node *yaml.Node, flow bool, value string) error {
	if node.Kind != yaml.ScalarNode || node.Anchor != "" {
		return fmt.Errorf("%w: line %d is not a plain or quoted scalar", ErrUnsupportedLayout, node.Line)
	}

	start, err := e.offset(node.Line, node.Column)
	if err != nil {
		return err
	}

	end, err := e.scalarEnd(node, start, flow)
	if err != nil {
		return err
	}

	text, err := formatScalar(node.Style, value)
	if err != nil {
		return err
	}

	e.edits = append(e.edits, edit{start: start, end: end, text: text})

	return nil
}

// scalarEnd finds where the scalar that starts at start ends on its line.
func (e *editor) scalarEnd(node *yaml.Node, start int, flow bool) (int, error) {
	line := e.src[start:e.lineEnd(start)]

	var (
		n  int
		ok bool
	)

	switch {
	case node.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.TaggedStyle) != 0:
	case node.Style&yaml.DoubleQuotedStyle != 0:
		n, ok = doubleQuotedLen(line)
	case node.Style&yaml.SingleQuotedStyle != 0:
		n, ok = singleQuotedLen(line)
	default:
		n, ok = plainLen(line, flow, node.Value)
	}

	if !ok {
		return 0, fmt.Errorf("%w: the value on line %d must sit on one line, without a tag, anchor or block style",
			ErrUnsupportedLayout, node.Line)
	}

	return start + n, nil
}

// doubleQuotedLen is the length of the double-quoted scalar at the start of
// line, quotes included.
func doubleQuotedLen(line []byte) (int, bool) {
	for i := 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}

	return 0, false
}

// singleQuotedLen is the length of the single-quoted scalar at the start of
// line, quotes included; ” is an escaped quote.
func singleQuotedLen(line []byte) (int, bool) {
	for i := 1; i < len(line); i++ {
		if line[i] != '\'' {
			continue
		}

		if i+1 < len(line) && line[i+1] == '\'' {
			i++

			continue
		}

		return i + 1, true
	}

	return 0, false
}

// plainLen is the length of the plain scalar at the start of line, which must
// read as value: it ends at a comment, the line's end or, in a flow mapping, a
// flow indicator.
func plainLen(line []byte, flow bool, value string) (int, bool) {
	end := len(line)
	if i := bytes.Index(line, []byte(" #")); i >= 0 {
		end = i
	}

	if i := bytes.IndexAny(line[:end], ",}]"); flow && i >= 0 {
		end = i
	}

	token := bytes.TrimRight(line[:end], " \t\r\n")

	return len(token), string(token) == value
}

// formatScalar writes value in the given style. A plain value that YAML would
// read as something other than that string, such as 1.0 or true, is quoted.
func formatScalar(style yaml.Style, value string) (string, error) {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		return strconv.Quote(value), nil
	case style&yaml.SingleQuotedStyle != 0:
		if strings.ContainsAny(value, "\n\r") {
			return strconv.Quote(value), nil
		}

		return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
	}

	out, err := yaml.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("formatting %q: %w", value, err)
	}

	text := strings.TrimSuffix(string(out), "\n")
	if strings.Contains(text, "\n") {
		return strconv.Quote(value), nil
	}

	return text, nil
}

// mappingValue returns the value node of key in a mapping node, and the key
// node.
func mappingValue(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}

	return nil, nil
}

// indentUnit guesses the file's indentation from its first nested block
// mapping.
func indentUnit(root *yaml.Node) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if v.Kind == yaml.MappingNode && v.Style&yaml.FlowStyle == 0 && len(v.Content) > 0 && v.Content[0].Column > k.Column {
			return v.Content[0].Column - k.Column
		}
	}

	return defaultIndent
}

// setImage is SetImage on bytes.
func setImage(src []byte, repository, tag string) ([]byte, error) {
	if repository == "" || tag == "" {
		return nil, ErrEmptyImage
	}

	root, ok, err := parseRoot(src)
	if err != nil {
		return nil, err
	}

	wanted := map[string]string{keyRepository: repository, keyTag: tag}

	if !ok {
		return appendVerified(src, defaultIndent, wanted)
	}

	key, image := mappingValue(root, keyImage)
	if image == nil {
		return appendVerified(src, indentUnit(root), wanted)
	}

	ed := newEditor(src)

	err = ed.editImageNode(key, image, indentUnit(root), wanted)
	if err != nil {
		return nil, err
	}

	return verify(src, ed.apply(), repository, tag)
}

// parseRoot parses src and returns its top-level block mapping; ok is false
// for an empty document.
func parseRoot(src []byte) (*yaml.Node, bool, error) {
	var doc yaml.Node

	err := yaml.Unmarshal(src, &doc)
	if err != nil {
		return nil, false, fmt.Errorf("parsing: %w", err)
	}

	if len(doc.Content) == 0 {
		return nil, false, nil
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 {
		return nil, false, fmt.Errorf("%w: the file must be a block mapping", ErrUnsupportedLayout)
	}

	return root, true, nil
}

// editImageNode records the edits for the value of the image: key: keys
// under an empty image:, or the keys of an image mapping.
func (e *editor) editImageNode(key, image *yaml.Node, indent int, wanted map[string]string) error {
	switch {
	case image.Kind == yaml.ScalarNode && image.Tag == "!!null" && image.Value == "":
		return e.insertAfter(key, key.Column-1+indent, wanted, []string{keyRepository, keyTag})
	case image.Kind == yaml.MappingNode:
		return e.editImage(key, image, wanted)
	default:
		return fmt.Errorf("%w: image on line %d is not a mapping", ErrUnsupportedLayout, key.Line)
	}
}

// editImage records the edits of an image: mapping: its repository and tag
// replaced, or added when missing.
func (e *editor) editImage(key, image *yaml.Node, wanted map[string]string) error {
	flow := image.Style&yaml.FlowStyle != 0

	var missing []string

	for _, k := range []string{keyRepository, keyTag} {
		_, v := mappingValue(image, k)
		if v == nil {
			missing = append(missing, k)

			continue
		}

		err := e.replaceScalar(v, flow, wanted[k])
		if err != nil {
			return err
		}
	}

	if len(missing) == 0 {
		return nil
	}

	if flow || len(image.Content) == 0 {
		return fmt.Errorf("%w: image on line %d is a flow mapping without %s",
			ErrUnsupportedLayout, key.Line, strings.Join(missing, " and "))
	}

	return e.insertAfter(key, image.Content[0].Column-1, wanted, missing)
}

// insertAfter records new keys on the lines right after the key node's line.
func (e *editor) insertAfter(key *yaml.Node, column int, wanted map[string]string, keys []string) error {
	start, err := e.offset(key.Line, 1)
	if err != nil {
		return err
	}

	at := e.lineEnd(start)

	var b strings.Builder

	if at == len(e.src) && (at == 0 || e.src[at-1] != '\n') {
		b.WriteString("\n")
	}

	for _, k := range keys {
		text, err := formatScalar(0, wanted[k])
		if err != nil {
			return err
		}

		b.WriteString(strings.Repeat(" ", column) + k + ": " + text + "\n")
	}

	e.edits = append(e.edits, edit{start: at, end: at, text: b.String()})

	return nil
}

// appendVerified appends an image: block and verifies the result.
func appendVerified(src []byte, indent int, wanted map[string]string) ([]byte, error) {
	out, err := appendImageBlock(src, indent, wanted)
	if err != nil {
		return nil, err
	}

	return verify(src, out, wanted[keyRepository], wanted[keyTag])
}

// appendImageBlock adds an image: block at the end of the source.
func appendImageBlock(src []byte, indent int, wanted map[string]string) ([]byte, error) {
	var b strings.Builder

	b.Write(src)

	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteString("\n")
	}

	b.WriteString(keyImage + ":\n")

	for _, k := range []string{keyRepository, keyTag} {
		text, err := formatScalar(0, wanted[k])
		if err != nil {
			return nil, err
		}

		b.WriteString(strings.Repeat(" ", indent) + k + ": " + text + "\n")
	}

	return []byte(b.String()), nil
}

// verify checks that out decodes to the same tree as src with only
// image.repository and image.tag changed. It is the guard against an edit that
// lands in the wrong place.
func verify(src, out []byte, repository, tag string) ([]byte, error) {
	var before, after Tree
	err := yaml.Unmarshal(src, &before)
	if err != nil {
		return nil, fmt.Errorf("parsing: %w", err)
	}

	err = yaml.Unmarshal(out, &after)
	if err != nil {
		return nil, fmt.Errorf("%w: the edit does not parse: %w", ErrUnsupportedLayout, err)
	}

	img, _ := before[keyImage].(Tree)
	want := Merge(before, Tree{keyImage: Merge(img, Tree{keyRepository: repository, keyTag: tag})})

	if !reflect.DeepEqual(want, after) {
		return nil, fmt.Errorf("%w: the edit changed more than the image", ErrUnsupportedLayout)
	}

	return out, nil
}
