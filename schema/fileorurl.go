package schema

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrNoBaseDir reports a relative file path given to [FileOrURL] with an
// empty baseDir, which leaves nothing to resolve the path against.
var ErrNoBaseDir = errors.New("relative schema path has no base directory")

// FileOrURL creates a [Ref] for a schema reference as written in a
// directive or on a command line. It names a URL as [URL] does for an
// HTTP/HTTPS reference and a file as [File] does for a file path. Use
// those directly for a reference written in the program, where an empty
// one is a mistake rather than input to report.
//
// Schemes match case-insensitively, and an HTTP/HTTPS reference resolves
// to a [Ref] whose URL carries the scheme in lower case. A file:// URL
// resolves to the local path it names. A file:// URL with a host other
// than localhost names no local path, so FileOrURL treats the whole
// reference as a relative file path, which then fails to resolve or read.
// A fragment on an HTTP/HTTPS or file:// URL selects a subschema, as the
// note on [URL] says, and a '#' in a plain path is part of the file name.
// [Directive] splits a fragment off a plain path before it calls
// FileOrURL, as yaml-language-server does. A relative file path joins
// baseDir. The path a file:// URL names, an absolute or rooted path, and
// an HTTP/HTTPS URL ignore baseDir, so on Windows "/schemas/config.json"
// names a file at the root of the current drive rather than one below
// baseDir. When baseDir is empty and the path is relative, the error wraps
// [ErrNoBaseDir], and an empty ref is [ErrEmptyPath] whatever baseDir is.
// The registry fetches an HTTP/HTTPS reference with the client
// [WithHTTPClient] gave it.
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
// the reference from the document hands it back as it is. [filepath.Dir]
// of an empty path is ".", so the resolver leaves baseDir empty for a
// document without a file path. A relative reference from such a document
// then reports [ErrNoBaseDir] instead of resolving against the working
// directory:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    var baseDir string
//	    if path := doc.FilePath(); path != "" {
//	        baseDir = filepath.Dir(path)
//	    }
//
//	    return schema.FileOrURL(baseDir, pickSchema(doc))
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
	if httpfetch.IsHTTPURL(ref) {
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
	// not the platform reads that path as absolute. The path leaves out the
	// fragment, which names a subschema, so the key takes it back as
	// written, as the key of an HTTP/HTTPS URL keeps it.
	if fromFileURL {
		r, err := file(path)
		if err != nil {
			return Ref{}, err
		}

		if _, fragment, _ := strings.Cut(ref, "#"); fragment != "" {
			r.key += "#" + fragment
		}

		return r, nil
	}

	// A drive-letter path is absolute on Windows and names nothing a POSIX
	// base directory can resolve, so it never joins baseDir either. The
	// drive then survives into the URL and the read error. A rooted path
	// counts as absolute everywhere, as the path of a file URL does, though
	// Windows reads one that carries no volume as relative. There it
	// resolves on the current drive.
	if filepath.IsAbs(path) || hasDriveLetter(path) || os.IsPathSeparator(path[0]) {
		return file(path)
	}

	if baseDir == "" {
		return Ref{}, fmt.Errorf("%w: %q", ErrNoBaseDir, ref)
	}

	return file(filepath.Join(baseDir, path))
}

// isFileURL reports whether ref is a file URL, in any letter case. RFC 8089
// allows the form without an authority, file:/path, alongside file:///path,
// so the check is for "file:/" rather than "file://".
func isFileURL(ref string) bool {
	return hasPrefixFold(ref, "file:/")
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
//
// Off Windows, a colon escaped as %3A, as in
// file:///C%3A/schemas/config.json, marks a directory at the root that
// only looks like a drive, the form [File] names the POSIX path
// /C:/schemas/config.json by, so the function keeps that slash. Windows
// has no such directory, so there the escaped colon names the drive, as
// it does in the URLs VS Code writes, and the function drops the slash.
func fileURLPath(ref string) (string, bool) {
	u, err := url.Parse(ref)
	if err != nil || u.Path == "" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) {
		return ref, false
	}

	return filepath.FromSlash(trimDriveSlash(u)), true
}

// trimDriveSlash returns the path of u, without its leading slash when
// its first segment is a Windows drive letter, so "/C:/schemas" becomes
// "C:/schemas". Any other path comes back unchanged. On Windows, a colon
// escaped as %3A names the drive too. Off Windows, it names a directory,
// so "/C%3A/schemas" comes back as "/C:/schemas".
func trimDriveSlash(u *url.URL) string {
	p := u.Path
	if p == "" || p[0] != '/' || !hasDriveLetter(p[1:]) {
		return p
	}

	if onWindows {
		return p[1:]
	}

	// RawPath holds the path as the URL spells it whenever that differs
	// from the default escaping, as a %3A always does. EscapedPath ignores
	// RawPath that leaves a byte such as a space unescaped, and then spells
	// the colon as itself.
	esc := u.RawPath
	if esc == "" {
		esc = u.Path
	}

	// The letter is one byte, or three when percent-encoded as in %43.
	colon := len("/C")
	if strings.HasPrefix(esc, "/%") {
		colon = len("/%43")
	}

	if len(esc) <= colon || esc[colon] != ':' {
		return p
	}

	return p[1:]
}

// hasDriveLetter reports whether p starts with a Windows drive letter and
// nothing else or a separator behind it, as in "C:" or "C:/schemas". Such
// a path is absolute wherever a program reads it, so the check does not
// depend on the platform running it.
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
