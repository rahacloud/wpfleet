package ops

// Normalize normalises a manifest the way Diff does, with a redactor of its
// own.
func Normalize(manifests ...string) []string {
	r := newRedactor()
	out := make([]string, 0, len(manifests))

	for _, m := range manifests {
		out = append(out, r.normalize(m))
	}

	return out
}
