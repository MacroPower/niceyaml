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

// driveLen is the length of a Windows drive prefix: a letter and a colon.
const driveLen = 2

// File creates a [Ref] that names a schema file. The Ref is a [Resolver]
// that names the file for every document. The registry reads the file
// with [Registry.Load] from the working directory, with path made
// absolute against it. Given [WithFS], the registry reads path from that
// file system instead, whose root stands for the working directory, so a
// schema shipped in an [embed.FS] loads without touching the disk. A
// relative path reads relative to that root, in slash form. An absolute
// path reads relative to the working directory File made it absolute
// against, so an absolute path outside that directory names no file in
// the file system. See [WithFS] for details.
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
// device, or a named pipe fails to load. Given [WithFS], the registry
// checks the path with [fs.Stat] before it opens the file, and an [fs.FS]
// opens a file with no flags, so a named pipe can still block the read
// until a writer opens it. A file system that implements [fs.StatFS],
// such as [os.DirFS] or the one [os.Root.FS] returns, answers the check
// without an open, so only a named pipe that replaces the file while the
// registry reads it blocks. On any other file system, such as the one
// [fs.Sub] wraps around [os.DirFS], [fs.Stat] opens the file to check it,
// so a named pipe already at the path blocks the read as well.
//
// A relative path has no absolute form in a process without a working
// directory, such as one whose directory no longer exists. File then
// names the path by the URL of the same path under the root, so
// "schemas/config.json" becomes file:///schemas/config.json, and the
// registry reports the missing directory when it loads the Ref.
//
// File is for a path written in the program, so it panics on an empty
// path, as [Loadable] panics on an empty key. A reference read from a
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
// [ErrEmptyPath]. A path with no absolute form is not an error, and the
// read of its Ref reports why it has none.
func file(path string) (Ref, error) {
	if path == "" {
		return Ref{}, ErrEmptyPath
	}

	// A registry with a file system reads an absolute path relative to
	// this directory. When os.Getwd fails, wd stays empty and the registry
	// uses the working directory at the time of the read, so an absolute
	// path still builds a Ref.
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}

	var (
		abs    = path
		absErr error
	)

	switch {
	case !onWindows && wd != "" && !filepath.IsAbs(path) && !hasDriveLetter(path):
		// Off Windows, filepath.Abs joins a relative path to a second read
		// of the working directory, which can differ from wd if another
		// goroutine changes directory in between. Join path to wd instead,
		// so the key and wd name one directory. Windows keeps
		// filepath.Abs, which resolves rooted and drive-relative paths
		// that Join does not.
		abs = filepath.Join(wd, path)

	case !hasDriveLetter(path) || onWindows:
		abs, absErr = filepath.Abs(path)

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

	// A relative path has no absolute form while the process has no
	// working directory. The key then names the path under the root, and
	// the read reports absErr.
	named := abs
	if absErr != nil {
		abs, named = "", slashpath.Clean("/"+filepath.ToSlash(path))
	}

	return Ref{key: fileURL(named), file: path, abs: abs, wd: wd, absErr: absErr}, nil
}

// noAbsPath returns the error a read of ref reports when [File] could not
// make its path absolute, and nil for any other Ref.
func noAbsPath(ref Ref) error {
	if ref.file == "" || ref.abs != "" {
		return nil
	}

	return fmt.Errorf("read %s: no absolute path: %w", ref.file, ref.absErr)
}

// readFile returns the bytes of the file a [Ref] from [File] names. It
// reads abs, the path [File] made absolute to build the key, so a change
// of working directory after [File] does not put another file's bytes
// under the key.
//
// The root of fsys stands for wd, the working directory [File] made the
// path absolute against, so readFile reads abs from fsys relative to wd,
// in slash form. Its errors then name the file by name, the path as
// given. A path outside wd, or a drive-letter path off Windows, names no
// file in fsys. A rooted path without a drive, such as \proj\x.json,
// counts as absolute on the drive of wd. Off Windows, readFile refuses a
// drive-letter path with [fs.ErrInvalid] whether fsys is nil or not.
func readFile(fsys fs.FS, name, abs, wd string) ([]byte, error) {
	if fsys != nil {
		return readFS(fsys, name, abs, wd)
	}

	// Off Windows, a drive letter is an ordinary directory name, so
	// the read would resolve the path against the working directory
	// while the key stays the cwd-independent drive URL. One key would
	// then name different bytes per directory, so refuse the read.
	if hasDriveLetter(abs) && !onWindows {
		return nil, fmt.Errorf("read %s: %w: a drive letter names no file on %s", abs, fs.ErrInvalid, runtime.GOOS)
	}

	// Stat before the open, since opening a FIFO blocks until a writer
	// opens the other end. A FIFO can replace the file after the Stat,
	// so openFile also opens without waiting where the platform allows
	// it, and readBounded checks the opened file again.
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}

	if !info.Mode().IsRegular() {
		return nil, notRegular(abs)
	}

	f, err := openFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	defer f.Close() //nolint:errcheck // Best-effort close.

	return readBounded(f, abs)
}

// readFS returns the bytes of the file at abs, an absolute path, in
// fsys, whose root stands for wd. It reads abs relative to wd, so the
// file it reads depends only on abs and wd, and errors name the file by
// name. A rooted path without a drive, such as \proj\x.json, counts as
// absolute, as it does for [FileOrURL]. An empty wd stands for the
// working directory at the time of the read. A path outside wd, or a
// drive-letter path off Windows, is [fs.ErrInvalid].
func readFS(fsys fs.FS, name, abs, wd string) ([]byte, error) {
	rel, err := fsRelative(abs, wd)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	fsPath := slashpath.Clean(filepath.ToSlash(rel))
	if !fs.ValidPath(fsPath) {
		return nil, fmt.Errorf("read %s: %w: not a path in the registry's file system", name, fs.ErrInvalid)
	}

	// Stat before the open, as readFile does. The Stat of [os.DirFS]
	// follows a symbolic link, so a link to a device is not regular.
	// Unlike readFile, the open takes no flags, since [fs.FS] has none
	// to pass. A FIFO that replaces the file after the Stat therefore
	// blocks the open until a writer opens the other end. When fsys does
	// not implement [fs.StatFS], as the [fs.Sub] wrapper of [os.DirFS]
	// does not, fs.Stat opens the file to stat it, so a FIFO already at
	// the path blocks the Stat itself.
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

// fsRelative returns abs, an absolute path, relative to wd, the
// directory the root of the registry's file system stands for, for
// [readFS]. An empty wd stands for the working directory at the time of
// the call.
func fsRelative(abs, wd string) (string, error) {
	if wd == "" {
		var err error

		wd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
	}

	// Windows reads a rooted path without a volume, such as \proj\x.json,
	// as relative, though File made it absolute on the drive of the
	// working directory, so it takes the drive of wd. Off Windows, a
	// rooted path is absolute already.
	target := abs
	if abs != "" && os.IsPathSeparator(abs[0]) && filepath.VolumeName(abs) == "" && !filepath.IsAbs(abs) {
		target = filepath.VolumeName(wd) + abs
	}

	rel, err := filepath.Rel(wd, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"%w: not under the working directory the registry's file system stands for",
			fs.ErrInvalid,
		)
	}

	return rel, nil
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
