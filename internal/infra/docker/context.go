package docker

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ContextDir is the directory of the build context that holds the site's
// tree; the Dockerfile copies it to /var/www/html.
const ContextDir = "site"

// DockerfileName is the generated Dockerfile at the root of the context.
const DockerfileName = "Dockerfile"

// dockerfileMode is the mode of the generated Dockerfile in the context.
const dockerfileMode = 0o644

// ErrUnsupportedFile is returned for a file in the source tree that a tar
// stream cannot carry, such as a socket or a device.
var ErrUnsupportedFile = errors.New("unsupported file type")

// configSample is the one wp-config file at the root that holds no secrets.
const configSample = "wp-config-sample.php"

// Excluded reports whether the path rel, slash-separated and relative to the
// source tree, stays out of the image, because it holds the site's secrets or
// is written at run time:
//
//   - at the root, every file whose name starts with wp-config except
//     wp-config-sample.php (wp-config.php and the copies a server collects,
//     such as wp-config.php.bak or wp-config-old.php), and database dumps
//     (*.sql, *.sql.gz and the like);
//   - wp-content/uploads, wp-content/cache and wp-content/upgrade;
//   - at any depth, .git, .svn and .hg, dotenv files (.env, .env.*) and every
//     error_log.
//
// A directory that is excluded is left out with everything below it.
func Excluded(rel string) bool {
	switch rel {
	case "wp-content/uploads", "wp-content/cache", "wp-content/upgrade":
		return true
	}

	switch base := path.Base(rel); {
	case base == ".git", base == ".svn", base == ".hg", base == "error_log":
		return true
	case base == ".env", strings.HasPrefix(base, ".env."):
		return true
	case strings.Contains(rel, "/"):
		return false
	}

	lower := strings.ToLower(rel)

	return (strings.HasPrefix(lower, "wp-config") && lower != configSample) || isDump(lower)
}

// isDump reports whether a lowercase file name is a database dump: .sql, or
// .sql compressed.
func isDump(name string) bool {
	switch ext := path.Ext(name); ext {
	case ".gz", ".bz2", ".xz", ".zst", ".zip":
		name = strings.TrimSuffix(name, ext)
	}

	return path.Ext(name) == ".sql"
}

// Dockerfile is the Dockerfile the build runs: the site's tree copied onto
// the base image, owned by the web server's user, with OCI labels.
func Dockerfile(spec Spec) string {
	labels := [][2]string{
		{"org.opencontainers.image.title", spec.Site},
		{"org.opencontainers.image.version", spec.Image.Tag},
		{"org.opencontainers.image.created", spec.Created.UTC().Format(time.RFC3339)},
		{"org.opencontainers.image.base.name", spec.Base},
		{"org.opencontainers.image.ref.name", spec.Image.String()},
	}

	var b strings.Builder

	b.WriteString("FROM " + spec.Base + "\n")
	b.WriteString("COPY --chown=www-data:www-data " + ContextDir + "/ /var/www/html/\n")
	b.WriteString("LABEL")

	for _, l := range labels {
		b.WriteString(" \\\n      " + l[0] + "=" + strconv.Quote(l[1]))
	}

	b.WriteString("\n")

	return b.String()
}

// WriteContext writes the build context of spec to w as a tar stream: the
// generated Dockerfile, then the source tree under site/ without the
// excluded paths. File names are written byte for byte in the GNU format,
// which carries names that are not valid UTF-8; modes and symbolic links are
// kept, owners are not (the Dockerfile sets them).
func WriteContext(w io.Writer, spec Spec) error {
	root, err := filepath.EvalSymlinks(spec.Source)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}

	tw := tar.NewWriter(w)

	dockerfile := Dockerfile(spec)

	// Set field by field: a literal would have to name the deprecated Xattrs.
	hdr := new(tar.Header)
	hdr.Typeflag = tar.TypeReg
	hdr.Name = DockerfileName
	hdr.Size = int64(len(dockerfile))
	hdr.Mode = dockerfileMode
	hdr.ModTime = spec.Created.UTC().Truncate(time.Second)
	hdr.Format = tar.FormatGNU

	err = tw.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("writing %s: %w", DockerfileName, err)
	}

	_, err = io.WriteString(tw, dockerfile)
	if err != nil {
		return fmt.Errorf("writing %s: %w", DockerfileName, err)
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		return addEntry(tw, root, p, d)
	})
	if err != nil {
		return fmt.Errorf("source %s: %w", spec.Source, err)
	}

	err = tw.Close()
	if err != nil {
		return fmt.Errorf("closing the tar stream: %w", err)
	}

	return nil
}

// addEntry writes one walked path to the tar stream, skipping it (and a
// directory's contents) when it is excluded.
func addEntry(tw *tar.Writer, root, p string, d fs.DirEntry) error {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	rel = filepath.ToSlash(rel)

	if rel != "." && Excluded(rel) {
		if d.IsDir() {
			return filepath.SkipDir
		}

		return nil
	}

	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	hdr, err := header(info, p, rel)
	if err != nil {
		return err
	}

	err = tw.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	if hdr.Typeflag != tar.TypeReg {
		return nil
	}

	return copyFile(tw, p)
}

// header is the tar header of one file of the tree, named under site/.
func header(info fs.FileInfo, p, rel string) (*tar.Header, error) {
	mode := info.Mode()

	link := ""

	switch {
	case mode.IsRegular(), mode.IsDir():
	case mode&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}

		link = target
	default:
		return nil, fmt.Errorf("%s: %w %s", p, ErrUnsupportedFile, mode.Type())
	}

	hdr, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}

	name := ContextDir
	if rel != "." {
		name += "/" + rel
	}

	if mode.IsDir() {
		name += "/"
	}

	hdr.Name = name
	hdr.Uid, hdr.Gid = 0, 0
	hdr.Uname, hdr.Gname = "", ""
	hdr.ModTime = hdr.ModTime.UTC().Truncate(time.Second)
	hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
	hdr.Format = tar.FormatGNU

	return hdr, nil
}

// copyFile copies the file at p into the tar stream.
func copyFile(w io.Writer, p string) error {
	f, err := os.Open(p) //nolint:gosec // p comes from walking the source tree the user named
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	_, err = io.Copy(w, f)

	closeErr := f.Close()

	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	if closeErr != nil {
		return fmt.Errorf("%s: %w", p, closeErr)
	}

	return nil
}
