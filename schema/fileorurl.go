package schema

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrNoBaseDir reports a relative file path given to [FileOrURL] with an
// empty baseDir. An empty baseDir does not give the path a directory to
// resolve against. [RefBeside] reports it for a relative path beside a
// document that does not have a file path. It also reports it beside a
// document that has a relative file path and does not have a file
// system.
var ErrNoBaseDir = errors.New("relative schema path has no base directory")

// FileOrURL creates a [Ref] for a schema reference as written in a
// directive or on a command line. For an HTTP or HTTPS reference, it
// names a URL as [URL] does. For a file path, it names a file as [File]
// does. Use those functions directly for a reference written in the
// program, where an empty reference is a mistake rather than input to
// report.
//
// Schemes match case-insensitively, and an HTTP or HTTPS reference
// resolves to a [Ref] whose URL carries the scheme in lower case. The
// registry fetches such a reference with the client that
// [WithHTTPClient] gave it. A file:// URL resolves to the local path
// that it names. A file:// URL with a host other than localhost does not
// name a local path. FileOrURL treats the whole of such a reference as a
// relative file path, which then fails to resolve or read.
//
// A fragment on an HTTP, HTTPS, or file:// URL selects a subschema, as
// the note on [URL] says. A '#' in a plain path is part of the file name.
// [Directive] splits a fragment off a plain path before it calls
// FileOrURL, as yaml-language-server does.
//
// A relative file path joins baseDir. The path that a file:// URL names,
// an absolute or rooted path, and an HTTP or HTTPS URL ignore baseDir. On
// Windows, "/schemas/config.json" therefore names a file at the root of
// the current drive rather than one below baseDir. When baseDir is empty
// and the path is relative, the error wraps [ErrNoBaseDir]. An empty ref
// is [ErrEmptyPath] whatever baseDir is.
//
// FileOrURL uses the reference as written, so whoever wrote it picks the
// file or host, as the note on [File] says of a path. A registry that
// takes references from another trust domain confines its reads, its
// fetches, and its cache. The Untrusted Documents section of the package
// documentation describes how.
//
// FileOrURL names a file on disk. A reference that belongs to a document
// goes through [RefBeside] instead. RefBeside takes the base directory
// from the document and names the file in the file system that the
// document came from.
//
// A program reports the error of a reference from a command line where
// it reads the flag:
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

// RefBeside creates a [Ref] for a schema reference that belongs to doc,
// such as one that a field of the document holds or that a resolver
// derives from its content. It reads the reference as [FileOrURL] does,
// with the directory of the document's file as the base directory. A
// relative path thus names a file beside the document. A document
// without a file path does not have such a directory, and a relative
// path then reports [ErrNoBaseDir].
//
// A document with a relative file path and without a file system does
// not have such a directory either. A relative reference beside it
// reports ErrNoBaseDir too. Such a file path names a file on disk only
// against the working directory, which the author of the document does
// not pick. [niceyaml.NewSourceFromFile] sets an absolute path. A program
// that builds a document from bytes passes an absolute path to
// [niceyaml.WithFilePath], or gives the document a file system with
// [niceyaml.WithFS].
//
// A Node of the document that [niceyaml.NewSourceFromLayers] builds has
// the file path of the layer that [niceyaml.MergeAs] names, or else of
// the lowest layer, whichever layer holds the reference. A resolver that
// reads the reference from a field passes [niceyaml.Node.Origin] of the
// Node of that field. A relative path then names a file beside the layer
// that sets the field.
//
// The file that the Ref names lives where the document does. For a
// document from [niceyaml.NewSourceFromFile], the file lives on disk. For
// one from [niceyaml.NewSourceFromFS], or one whose source
// [niceyaml.WithFS] gave a file system, the file lives in that file
// system, as a file that [FileFS] names does. An absolute path and a
// file:// URL then do not name a file, and both fail to load with
// [ErrFileNotAllowed]. The registry reads such a file only through the
// file system, so the file system decides what the reference can read,
// and the [Access] of the registry does not apply. An [os.DirFS] follows
// a symbolic link out of its directory, as Access describes. An HTTP or
// HTTPS reference names a URL either way.
//
// The registry tells the file system of one document from that of
// another by its value. The file system must be a value that Go can
// compare, or a map, as FileFS describes. A Ref in a file system of any
// other type fails to load with [fs.ErrInvalid].
//
// [Directive] resolves the reference of a directive this way. A resolver
// of the program does the same for a reference that it reads itself:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    return schema.RefBeside(doc, pickSchema(doc))
//	})
//
// RefBeside uses the reference as written, so whoever wrote it picks the
// file or host, as the note on [FileOrURL] says. The Untrusted Documents
// section of the package documentation gives the registry options that
// confine a reference from another trust domain.
func RefBeside(doc *niceyaml.Node, ref string) (Ref, error) {
	var baseDir string

	// A relative path on disk names a file only against the working
	// directory, which the document does not pick, so it gives no base
	// directory.
	path := doc.FilePath()
	relativeOnDisk := path != "" && doc.FS() == nil && !filepath.IsAbs(path)

	if path != "" && !relativeOnDisk {
		baseDir = filepath.Dir(path)
	}

	r, err := FileOrURL(baseDir, ref)
	if relativeOnDisk && errors.Is(err, ErrNoBaseDir) {
		return Ref{}, fmt.Errorf("%w: the document path %q is relative and names no file system", err, path)
	}

	if err != nil {
		return Ref{}, err
	}

	return inFS(r, doc.FS()), nil
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

// fileURLPath returns the local path that a file URL names, and whether
// the URL names a path at all. The path uses the native separator of the
// platform. fileURLPath reads a URL with an empty authority (file:///path)
// and one without an authority (file:/path) alike. A URL that does not
// parse, names a host other than localhost, or does not name a path comes
// back unchanged with false. The later resolve or read of the reference
// then reports it.
//
// A Windows path carries its drive letter behind the leading slash of the
// URL path, as in file:///C:/schemas/config.json. [File] names such a
// path by that form. The function drops that slash, so the result is the
// absolute path C:\schemas\config.json rather than the relative path
// \C:\schemas\config.json.
//
// Off Windows, a colon escaped as %3A, as in
// file:///C%3A/schemas/config.json, marks a directory at the root whose
// name looks like a drive. [File] names the POSIX path
// /C:/schemas/config.json by that form, so the function keeps that
// slash. Windows does not have such a directory. There, the escaped colon
// names the drive, as it does in the URLs that VS Code writes, and the
// function drops the slash.
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

// hasDriveLetter reports whether p starts with a Windows drive letter
// followed by the end of p or a separator, as in "C:" or "C:/schemas".
// Such a path is absolute wherever a program reads it, so the check does
// not depend on the platform that runs it.
//
// A colon is a legal character in a POSIX file name, so "a:b.json" is a
// relative path. Windows reads it as a path relative to the current
// directory of drive A, so it is relative there too.
func hasDriveLetter(p string) bool {
	if len(p) < driveLen || p[1] != ':' {
		return false
	}

	if len(p) > driveLen && p[driveLen] != '/' && p[driveLen] != '\\' {
		return false
	}

	letter := p[0]

	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}
