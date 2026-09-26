package schema

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ErrNoBaseDir reports a relative file path given to [FileOrURL] with an
// empty baseDir, which leaves nothing to resolve the path against.
var ErrNoBaseDir = errors.New("relative schema path has no base directory")

// FileOrURL creates a [Ref] for a schema reference as written in a
// directive or on a command line, naming a URL as [URL] does for HTTP/HTTPS
// references and a file as [File] does for file paths. Use those directly
// for a reference written in the program, where an empty one is a mistake
// rather than input to report.
//
// Schemes match case-insensitively, and an HTTP/HTTPS reference resolves
// to a [Ref] whose URL carries the scheme in lower case. A file:// URL
// resolves to the local path it names. A file:// URL with a host other
// than localhost names no local path, so FileOrURL treats the whole
// reference as a relative file path, which then fails to resolve or read.
// A relative file path joins baseDir; the path a file:// URL names, an
// absolute path, and an HTTP/HTTPS URL ignore baseDir. When baseDir is
// empty and the path is relative, the error wraps [ErrNoBaseDir], and an
// empty ref is [ErrEmptyPath] whatever baseDir is. The registry fetches
// an HTTP/HTTPS reference with the client [WithHTTPClient] gave it.
//
// FileOrURL uses the reference as written, so whoever wrote it picks the
// file or host, as the note on [File] says of a path. A registry that
// takes references from another trust domain confines its reads with
// [WithFS] and the file system of an [os.Root], and its fetches with a
// client that restricts hosts. It also serves one batch or request rather
// than the whole process, since it keeps a schema for every distinct URL
// it fetches, and an allowed host can answer endless ones.
//
// The result is the shape a [Resolver] returns, so a resolver that builds
// the reference from the document hands it back as it is:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    return schema.FileOrURL(filepath.Dir(doc.FilePath()), pickSchema(doc))
//	})
//
// A reference from a command line reports its error where the flag is
// read:
//
//	ref, err := schema.FileOrURL(cwd, flag)
//	if err != nil {
//	    return fmt.Errorf("--schema: %w", err)
//	}
//
//	reg := schema.NewRegistry(schema.WithResolvers(ref))
func FileOrURL(baseDir, ref string) (Ref, error) {
	// Check for an HTTP/HTTPS URL by string prefix, so a malformed URL that
	// fails to parse does not fall through as a file path.
	if isHTTPURL(ref) {
		return URL(ref), nil
	}

	// An empty reference names no file, so it must not join baseDir and
	// resolve to the base directory itself.
	if ref == "" {
		return Ref{}, ErrEmptyPath
	}

	path, fromFileURL := ref, false
	if isFileURL(ref) {
		path, fromFileURL = fileURLPath(ref)
	}

	// A file URL names a local path, which never joins baseDir, whether or
	// not the platform reads that path as absolute. A drive-letter path is
	// absolute on Windows and names nothing a POSIX base directory can
	// resolve, so it never joins baseDir either. The drive then survives
	// into the URL and the read error.
	if fromFileURL || filepath.IsAbs(path) || hasDriveLetter(path) {
		return file(path)
	}

	if baseDir == "" {
		return Ref{}, fmt.Errorf("%w: %q", ErrNoBaseDir, ref)
	}

	return file(filepath.Join(baseDir, path))
}

// isHTTPURL reports whether ref starts with http:// or https://, in any
// letter case.
func isHTTPURL(ref string) bool {
	return hasScheme(ref, "http") || hasScheme(ref, "https")
}

// isFileURL reports whether ref is a file URL, in any letter case. RFC 8089
// allows the form without an authority, file:/path, alongside file:///path,
// so the check is for "file:/" rather than "file://".
func isFileURL(ref string) bool {
	return hasPrefixFold(ref, "file:/")
}

// hasScheme reports whether ref starts with scheme followed by "://",
// compared case-insensitively.
func hasScheme(ref, scheme string) bool {
	return hasPrefixFold(ref, scheme+"://")
}

// hasPrefixFold reports whether s starts with prefix, compared
// case-insensitively.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// fileURLPath returns the local path a file URL names, in the native
// separator of the platform, whether the URL carries an empty authority
// (file:///path) or none (file:/path), and whether the URL names a path
// at all. A URL that does not parse, names a host other than localhost,
// or names no path comes back unchanged and false, so resolving or
// reading the reference reports it.
//
// A Windows path carries its drive letter behind the leading slash of the
// URL path, as in file:///C:/schemas/config.json, which is the form
// [File] names such a path by. The function drops that slash so the
// result is the absolute path C:\schemas\config.json rather than the
// relative path \C:\schemas\config.json.
func fileURLPath(ref string) (string, bool) {
	u, err := url.Parse(ref)
	if err != nil || u.Path == "" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
		return ref, false
	}

	return filepath.FromSlash(trimDriveSlash(u.Path)), true
}

// trimDriveSlash drops the leading slash of a URL path whose first segment
// is a Windows drive letter, so "/C:/schemas" becomes "C:/schemas". Any
// other path comes back unchanged.
func trimDriveSlash(p string) string {
	if p == "" || p[0] != '/' || !hasDriveLetter(p[1:]) {
		return p
	}

	return p[1:]
}

// hasDriveLetter reports whether p starts with a Windows drive letter and
// nothing else or a separator behind it, as in "C:" or "C:/schemas". Such
// a path is absolute wherever it is read, so the check does not depend on
// the platform running it.
//
// A colon is a legal character in a POSIX file name, so "a:b.json" is a
// relative path. Windows reads it as a path relative to the current
// directory of drive A, which is no more absolute.
func hasDriveLetter(p string) bool {
	const driveLen = 2 // A letter and a colon.

	if len(p) < driveLen || p[1] != ':' {
		return false
	}

	if len(p) > driveLen && p[driveLen] != '/' && p[driveLen] != '\\' {
		return false
	}

	letter := p[0]

	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}
