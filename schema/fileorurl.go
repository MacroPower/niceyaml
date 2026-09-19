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
// directive or on a command line, routing to [URL] for HTTP/HTTPS
// references and [File] for file paths. Input the program did not write
// can name no schema, so FileOrURL returns that as an error where [File]
// and [URL] panic. Use those directly when you know the reference at
// construction time.
//
// Schemes match case-insensitively, and an HTTP/HTTPS reference resolves
// to a [Ref] whose URL carries the scheme in lower case. A file:// URL
// resolves to the local path it names. A file:// URL with a host other
// than localhost names no local path, so FileOrURL treats the whole
// reference as a relative file path, which then fails to resolve or read.
// A relative file path joins baseDir; an absolute path or an HTTP/HTTPS
// URL ignores baseDir. When baseDir is empty and the path is relative,
// the error wraps [ErrNoBaseDir], and an empty ref is [ErrEmptyPath]
// whatever baseDir is. HTTPOptions apply when ref is an HTTP/HTTPS URL and
// do nothing for file paths.
//
// The result is the shape a [Resolver] returns, so a resolver that builds
// the reference from the document hands it back as it is:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
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
func FileOrURL(baseDir, ref string, opts ...HTTPOption) (Ref, error) {
	// Check for an HTTP/HTTPS URL by string prefix, so a malformed URL that
	// fails to parse does not fall through as a file path.
	if isHTTPURL(ref) {
		return URL(ref, opts...), nil
	}

	// An empty reference names no file, so it must not join baseDir and
	// resolve to the base directory itself.
	if ref == "" {
		return Ref{}, ErrEmptyPath
	}

	path := ref
	if isFileURL(ref) {
		path = fileURLPath(ref)
	}

	// A drive-letter path is absolute on Windows and names nothing a POSIX
	// base directory can resolve, so never join it to baseDir. The drive
	// then survives into the URL and the read error.
	if filepath.IsAbs(path) || hasDriveLetter(path) {
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
// (file:///path) or none (file:/path). A URL that does not parse, names a
// host other than localhost, or names no path comes back unchanged, so
// resolving or reading the reference reports it.
//
// A Windows path carries its drive letter behind the leading slash of the
// URL path, as in file:///C:/schemas/config.json, which is the form
// [File] names such a path by. The function drops that slash so the
// result is the absolute path C:\schemas\config.json rather than the
// relative path \C:\schemas\config.json.
func fileURLPath(ref string) string {
	u, err := url.Parse(ref)
	if err != nil || u.Path == "" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
		return ref
	}

	return filepath.FromSlash(trimDriveSlash(u.Path))
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

// hasDriveLetter reports whether p starts with a Windows drive letter, as
// in "C:/schemas". Such a path is absolute wherever it is read, so the
// check does not depend on the platform running it.
func hasDriveLetter(p string) bool {
	const driveLen = 2 // A letter and a colon.

	if len(p) < driveLen || p[1] != ':' {
		return false
	}

	letter := p[0]

	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}
