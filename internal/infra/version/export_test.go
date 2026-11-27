package version

// Resolve is resolve, for the tests.
func Resolve(version, commit, fallback string) string {
	return resolve(version, commit, fallback)
}
