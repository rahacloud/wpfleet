package deploy

import (
	"bytes"
	"cmp"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"go.yaml.in/yaml/v3"

	"github.com/rahacloud/wpfleet/internal/infra/helm"
)

// diffContext is how many unchanged lines a hunk shows around a change.
const diffContext = 3

// tagLength is how many hex digits of a secret value's tag are printed.
const tagLength = 12

// yamlIndent is the indent redacted documents are written with.
const yamlIndent = 2

// secretKind is the kind whose data a diff never prints.
const secretKind = "Secret"

// redactor hides the values of Secrets in manifests. Each value becomes a tag,
// an HMAC of the value under a key made for this run, so a diff still shows
// which values changed while printing nothing an attacker could reverse or
// compare with another run.
type redactor struct {
	key []byte
}

// newRedactor makes a redactor with a fresh random key.
func newRedactor() redactor {
	return redactor{key: []byte(rand.Text())}
}

// tag is the printed stand-in for a secret value.
func (r redactor) tag(value string) string {
	mac := hmac.New(sha256.New, r.key)
	_, _ = mac.Write([]byte(value))

	return "(redacted " + hex.EncodeToString(mac.Sum(nil))[:tagLength] + ")"
}

// document is one object of a manifest, normalised for comparison.
type document struct {
	kind, name, text string
}

// normalize prepares a manifest for comparison: it splits it into documents,
// drops blank lines, redacts the values of Secrets and sorts the documents by
// kind and name, so neither blank lines nor document order count as a
// difference.
func (r redactor) normalize(manifest string) string {
	splits := helm.SplitManifest(manifest)
	docs := make([]document, 0, len(splits))

	for _, raw := range splits {
		docs = append(docs, r.document(raw))
	}

	slices.SortFunc(docs, func(a, b document) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.name, b.name), cmp.Compare(a.text, b.text))
	})

	var b strings.Builder

	for _, d := range docs {
		b.WriteString("---\n")
		b.WriteString(d.text)
		b.WriteString("\n")
	}

	return b.String()
}

// header is the part of an object that identifies it.
type header struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
}

// document normalises one document of a manifest.
func (r redactor) document(raw string) document {
	var node yaml.Node

	err := yaml.Unmarshal([]byte(raw), &node)
	if err != nil {
		// A document that does not parse cannot be checked for secrets; one
		// that might be a Secret is hidden whole.
		if strings.Contains(raw, secretKind) {
			return document{kind: secretKind, name: "", text: r.tag(raw)}
		}

		return document{kind: "", name: "", text: dropBlankLines(raw)}
	}

	var h header

	_ = node.Decode(&h)

	if h.Kind != secretKind {
		return document{kind: h.Kind, name: h.Metadata.Name, text: dropBlankLines(raw)}
	}

	text, err := r.redactSecret(&node)
	if err != nil {
		text = r.tag(raw)
	}

	return document{kind: h.Kind, name: h.Metadata.Name, text: text}
}

// redactSecret replaces every value under data and stringData with its tag
// and writes the document back out.
func (r redactor) redactSecret(doc *yaml.Node) (string, error) {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		r.redactMapping(doc.Content[0])
	}

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	err := enc.Encode(doc)
	if err != nil {
		return "", fmt.Errorf("writing redacted Secret: %w", err)
	}

	err = enc.Close()
	if err != nil {
		return "", fmt.Errorf("writing redacted Secret: %w", err)
	}

	return dropBlankLines(buf.String()), nil
}

// redactMapping redacts the secret fields of a Secret's top-level mapping.
func (r redactor) redactMapping(obj *yaml.Node) {
	if obj.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i+1 < len(obj.Content); i += 2 {
		if key := obj.Content[i].Value; key != "data" && key != "stringData" {
			continue
		}

		values := obj.Content[i+1]
		if values.Kind != yaml.MappingNode {
			setScalar(values, r.tag(nodeText(values)))

			continue
		}

		for j := 1; j < len(values.Content); j += 2 {
			v := values.Content[j]
			setScalar(v, r.tag(nodeText(v)))
		}
	}
}

// setScalar turns n into a plain string scalar holding value, dropping what
// it held, its anchor and its line comment included.
func setScalar(n *yaml.Node, value string) {
	n.Kind, n.Style, n.Tag, n.Value = yaml.ScalarNode, 0, "!!str", value
	n.Anchor, n.Alias, n.Content, n.LineComment = "", nil, nil, ""
}

// nodeText is what a value node holds, for tagging: a scalar's value, or the
// encoding of anything else.
func nodeText(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}

	out, err := yaml.Marshal(n)
	if err != nil {
		return ""
	}

	return string(out)
}

// dropBlankLines removes the lines of s that hold only white space.
func dropBlankLines(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.TrimSpace(l) == "" })

	return strings.Join(lines, "\n")
}

// unifiedDiff is the unified diff from the live manifest to the rendered one,
// both normalised, or "" when they are the same.
func unifiedDiff(live, rendered, liveName, renderedName string) (string, error) {
	if live == rendered {
		return "", nil
	}

	//nolint:exhaustruct_v5 // dates and the line ending keep their defaults
	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(live),
		B:        difflib.SplitLines(rendered),
		FromFile: liveName,
		ToFile:   renderedName,
		Context:  diffContext,
	})
	if err != nil {
		return "", fmt.Errorf("diffing manifests: %w", err)
	}

	return out, nil
}
