package schema

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	slashpath "path"
)

// ErrEmptyPath reports an empty path given to [FileOrURL], which names no
// file.
var ErrEmptyPath = errors.New("schema file path is empty")

// File creates a [Ref] that names a schema file. The Ref is a [Resolver]
// that names the file for every document, and the registry reads the
// file with [Registry.Load]: from the working directory, with path made
// absolute against it, or from the file system [WithFS] gave the
// registry, with path as a slash-separated path relative to its root, so
// a schema shipped in an [embed.FS] loads without touching the disk.
//
// File names the schema by the file:// URL of the path made absolute
// against the working directory, such as file:///srv/schemas/config.json,
// however the registry reads it. A schema that [Embedded] names by a
// digest of its bytes, or that another resolver names by a bare path such
// as "schemas/config.json", therefore never shares a cache entry with the
// file. Relative spellings of one path, such as "schemas/config.json" and
// "./schemas/config.json", resolve to the same URL, so the registry reads
// the file once and reuses the compiled schema for every document that
// names it. The registry reads the file when the Ref loads, not when File
// runs.
//
// File is for a path written in the program, so it panics on an empty
// path, as [Loadable] panics on an empty key, and on a working directory
// that cannot be read to make the path absolute. A reference read from a
// directive or a command line, which may be empty or a URL, goes through
// [FileOrURL], which returns an error instead. The result is the shape a
// [Resolver] returns, so a resolver that builds the path from the document
// hands it back beside a nil error:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    node, err := doc.At(kindPath)
//	    if errors.Is(err, paths.ErrNotFound) {
//	        return schema.Ref{}, schema.ErrNoMatch
//	    }
//
//	    if err != nil {
//	        return schema.Ref{}, err
//	    }
//
//	    kind, err := node.Decode[string](ctx)
//	    if err != nil {
//	        return schema.Ref{}, err
//	    }
//
//	    return schema.File("schemas/" + kind + ".json"), nil
//	})
//
// File uses the path as written, without validating it. Validate a path
// from an untrusted source before passing it to File, to prevent path
// traversal attacks.
func File(path string) Ref {
	ref, err := file(path)
	if err != nil {
		panic("schema.File: " + err.Error())
	}

	return ref
}

// file is [File] that returns an error rather than panicking, for
// [FileOrURL], which takes a reference from the input. An empty path is
// [ErrEmptyPath].
func file(path string) (Ref, error) {
	if path == "" {
		return Ref{}, ErrEmptyPath
	}

	abs := path

	// A drive-letter path is absolute wherever it is read, but filepath.Abs
	// on a POSIX platform treats it as relative and puts the working
	// directory in front of it. Keep it as written, so the drive survives
	// into the URL and the read error.
	if !hasDriveLetter(path) {
		var err error

		abs, err = filepath.Abs(path)
		if err != nil {
			return Ref{}, fmt.Errorf("resolve %s: %w", path, err)
		}
	}

	return Ref{key: fileURL(abs), file: path, abs: abs}, nil
}

// readFile returns the bytes of the file a [Ref] from [File] names: from
// fsys, with name in slash form relative to its root, or from the working
// directory when fsys is nil, at abs, the path [File] made absolute to
// build the key, so a change of working directory between the two does
// not put another file's bytes under the key. When abs is empty, readFile
// makes name absolute against the working directory of the read.
func readFile(fsys fs.FS, name, abs string) ([]byte, error) {
	if fsys != nil {
		fsPath := slashpath.Clean(filepath.ToSlash(name))
		if !fs.ValidPath(fsPath) {
			return nil, fmt.Errorf("read %s: %w: not a path in the registry's file system", name, fs.ErrInvalid)
		}

		data, err := fs.ReadFile(fsys, fsPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}

		return data, nil
	}

	if abs == "" {
		abs = name

		if !hasDriveLetter(name) {
			var err error

			abs, err = filepath.Abs(name)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", name, err)
			}
		}
	}

	// Off Windows, a drive letter is an ordinary directory name, so
	// os.ReadFile would read the path against the working directory
	// while the key stays the cwd-independent drive URL. One key would
	// then name different bytes per directory, so refuse the read.
	if hasDriveLetter(abs) && runtime.GOOS != "windows" {
		return nil, fmt.Errorf("read %s: a drive letter names no file on %s", abs, runtime.GOOS)
	}

	data, err := os.ReadFile(abs) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}

	return data, nil
}

// fileURL returns the file:// URL that names the absolute path abs.
func fileURL(abs string) string {
	p := filepath.ToSlash(abs)

	// A Windows path starts with a drive letter, which needs a slash in front
	// of it in a URL path, as in file:///C:/schemas/config.json.
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return (&url.URL{Scheme: "file", Path: p}).String()
}
