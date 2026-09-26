package schema

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	slashpath "path"

	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// ErrEmptyPath reports an empty path given to [FileOrURL], which names no
// file.
var ErrEmptyPath = errors.New("schema file path is empty")

// onWindows reports whether the program runs on Windows, the one platform
// where a drive letter names a drive.
const onWindows = runtime.GOOS == "windows"

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
// file. Spellings of one path, such as "schemas/config.json" and
// "./schemas/config.json" from /srv, and
// "/srv/schemas/../schemas/config.json", resolve to the same URL, so the
// registry reads the file once and reuses the compiled schema for every
// document that names it. The registry reads the file when the Ref
// loads, not when File runs.
//
// A $ref in the schema resolves against that file:// URL, so "defs.json"
// names the file beside it. The registry reads each file or HTTP URL a
// reference names the way it reads the schema, once however many schemas
// reference it. When a reference fails to load, a later validation that
// reaches it loads it again. A remote schema that names a local file fails
// to resolve, and the registry does not read that file.
//
// The registry reads only a regular file of at most 10 MB, the limit it
// sets on a response from a [URL], so a path that names a directory, a
// device, or a named pipe fails to load.
//
// File is for a path written in the program, so it panics on an empty
// path, as [Loadable] panics on an empty key, and when it cannot get the
// working directory to make the path absolute. A reference read from a
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
//	    // The document picks kind, so keep it to a file name in schemas/.
//	    if strings.ContainsAny(kind, `/\`) {
//	        return schema.Ref{}, fmt.Errorf("kind %q: not a schema name", kind)
//	    }
//
//	    return schema.File("schemas/" + kind + ".json"), nil
//	})
//
// File uses the path as written, so a path built from a document can
// name any file the registry can read. Check such a path before passing
// it to File, as the example does, or confine the registry with
// [WithFS], so a path outside its file system names no file.
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

	const driveLen = 2 // A letter and a colon.

	// A registry with a file system reads an absolute path relative to
	// this directory. When os.Getwd fails, wd stays empty and the registry
	// uses the working directory at the time of the read, so an absolute
	// path still builds a Ref.
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}

	abs := path

	switch {
	case !hasDriveLetter(path) || onWindows:
		abs, err = filepath.Abs(path)
		if err != nil {
			return Ref{}, fmt.Errorf("resolve %s: %w", path, err)
		}

	case len(path) > driveLen:
		// A drive-letter path is absolute on every platform, but
		// filepath.Abs on a POSIX platform treats it as relative and puts
		// the working directory in front of it. Clean only the part behind
		// the drive, as Windows does, so a ".." at the drive root stays
		// there and the drive survives into the URL and the read error. A
		// bare drive has nothing to clean and stays as written.
		rest := strings.ReplaceAll(path[driveLen:], `\`, "/")
		abs = path[:driveLen] + slashpath.Clean(rest)
	}

	return Ref{key: fileURL(abs), file: path, abs: abs, wd: wd}, nil
}

// readFile returns the bytes of the file a [Ref] from [File] names. It
// reads name from fsys, in slash form relative to its root. When fsys is
// nil, it reads abs, the path [File] made absolute to build the key, so
// a change of working directory after [File] does not put another file's
// bytes under the key.
//
// The root of fsys stands for wd, the working directory [File] made the
// path absolute against, so an absolute name reads relative to wd, and
// one outside wd, or a drive-letter path off Windows, names no file in
// fsys.
func readFile(fsys fs.FS, name, abs, wd string) ([]byte, error) {
	if fsys != nil {
		return readFS(fsys, name, wd)
	}

	// Off Windows, a drive letter is an ordinary directory name, so
	// the read would resolve the path against the working directory
	// while the key stays the cwd-independent drive URL. One key would
	// then name different bytes per directory, so refuse the read.
	if hasDriveLetter(abs) && !onWindows {
		return nil, fmt.Errorf("read %s: a drive letter names no file on %s", abs, runtime.GOOS)
	}

	// Stat before the open, since opening a FIFO blocks until a writer
	// opens the other end.
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}

	if !info.Mode().IsRegular() {
		return nil, notRegular(abs)
	}

	f, err := os.Open(abs) //nolint:gosec // User-provided file paths are intentional.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	defer f.Close() //nolint:errcheck // Best-effort close.

	return readBounded(f, abs)
}

// readFS returns the bytes of name in fsys, whose root stands for wd. A
// relative name reads from the root as it is, and an absolute one reads
// relative to wd. An empty wd stands for the working directory at the
// time of the read. A name outside wd, or a drive-letter path off
// Windows, is [fs.ErrInvalid].
func readFS(fsys fs.FS, name, wd string) ([]byte, error) {
	rel := name

	if filepath.IsAbs(name) || hasDriveLetter(name) {
		var err error

		if wd == "" {
			wd, err = os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", name, err)
			}
		}

		rel, err = filepath.Rel(wd, name)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf(
				"read %s: %w: not under the working directory the registry's file system stands for",
				name, fs.ErrInvalid,
			)
		}
	}

	fsPath := slashpath.Clean(filepath.ToSlash(rel))
	if !fs.ValidPath(fsPath) {
		return nil, fmt.Errorf("read %s: %w: not a path in the registry's file system", name, fs.ErrInvalid)
	}

	// Stat before the open, as readFile does. The Stat of [os.DirFS]
	// follows a symbolic link, so a link to a device is not regular.
	info, err := fs.Stat(fsys, fsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, notRegular(name)
	}

	f, err := fsys.Open(fsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	defer f.Close() //nolint:errcheck // Best-effort close.

	return readBounded(f, name)
}

// readBounded returns the bytes of f, the file at name. It checks again
// that f is a regular file, since the file at the path can change after
// the caller's check, and refuses a file over [httpfetch.MaxSize] bytes,
// the limit a schema fetched from a URL has.
func readBounded(f fs.File, name string) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, notRegular(name)
	}

	// Read one byte past the limit, as [httpfetch.Get] does, so an
	// over-size file reads as MaxSize+1 bytes.
	data, err := io.ReadAll(io.LimitReader(f, httpfetch.MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	if int64(len(data)) > httpfetch.MaxSize {
		return nil, fmt.Errorf("read %s: file exceeds %d bytes", name, httpfetch.MaxSize)
	}

	return data, nil
}

// notRegular returns the [fs.ErrInvalid] error for name, a path that
// names something other than a regular file, such as a directory, a
// device, or a named pipe.
func notRegular(name string) error {
	return fmt.Errorf("read %s: %w: not a regular file", name, fs.ErrInvalid)
}

// fileURL returns the file:// URL that names the absolute path abs.
func fileURL(abs string) string {
	p := filepath.ToSlash(abs)

	// A Windows path starts with a drive letter, which needs a slash in front
	// of it in a URL path, as in file:///C:/schemas/config.json.
	if !strings.HasPrefix(p, "/") {
		return (&url.URL{Scheme: "file", Path: "/" + p}).String()
	}

	u := &url.URL{Scheme: "file", Path: p}

	// Escape the colon when the first directory of a rooted path looks like
	// a drive, as /C: does in /C:/schemas on a POSIX platform. The URL
	// file:///C%3A/schemas then never shares a key with the drive path
	// C:/schemas, because RFC 3986 tells an escaped colon from a literal one.
	// From that URL, fileURLPath recovers the rooted path.
	if hasDriveLetter(p[1:]) {
		esc := u.EscapedPath()
		u.RawPath = esc[:len("/C")] + "%3A" + esc[len("/C:"):]
	}

	return u.String()
}
