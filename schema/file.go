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

// File creates a [Ref] that names a schema file on disk. The Ref is a
// [Resolver] that names the file for every document. The registry reads
// the file with [Registry.Load], with a relative path made absolute
// against the working directory at the time File runs. A change of
// working directory after File therefore leaves the Ref on the same file.
//
// A schema shipped in an [embed.FS], or in any other file system, takes
// [FileFS] instead, which reads the path from that file system and never
// from disk. Given [WithFSAt], the registry reads the path it would read
// from disk through a file system that stands for one directory, so a
// path outside that directory names no file. See that option for
// details.
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
// A $ref in the schema resolves against the URL of the file, so
// "defs.json" names the file beside it. The registry reads each file or
// HTTP URL a reference names the
// way it reads the schema, once however many schemas reference it. When
// a reference fails to load, a later validation that reaches it loads it
// again. A remote schema that names a local file fails to resolve, and
// the registry does not read that file.
//
// The registry reads only a regular file of at most 10 MB, the limit it
// sets on a response from a [URL], so a path that names a directory, a
// device, or a named pipe fails to load. Given a file system, the
// registry checks the path with [fs.Stat] before it opens the file, and
// an [fs.FS] opens a file with no flags, so a named pipe can still block
// the read until a writer opens it. A file system that implements
// [fs.StatFS], such as [os.DirFS] or the one [os.Root.FS] returns,
// answers the check without an open, so only a named pipe that replaces
// the file while the registry reads it blocks. On any other file system,
// such as the one [fs.Sub] wraps around [os.DirFS], [fs.Stat] opens the
// file to check it, so a named pipe already at the path blocks the read
// as well.
//
// A relative path has no absolute form in a process without a working
// directory, such as one whose directory no longer exists or one that
// runs in a browser. File then names the path by the URL of the same
// path under the root, so "schemas/config.json" becomes
// file:///schemas/config.json. The registry reports the missing
// directory when it loads such a Ref. A Ref from [FileFS] needs no
// working directory.
//
// File is for a path written in the program, so it panics on an empty
// path, as [Loadable] panics on an empty key. A reference read from a
// directive or a command line, which may be empty or a URL, goes through
// [FileOrURL], which returns an error instead. The result is the shape a
// [Resolver] returns, so a resolver that builds the path from the document
// hands it back beside a nil error:
//
//	schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
//	    var kind string
//
//	    found, err := doc.DecodeIfPresent(ctx, kindPath, &kind)
//	    if err != nil {
//	        return schema.Ref{}, err
//	    }
//
//	    if !found {
//	        return schema.Ref{}, schema.ErrNoMatch
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
// [WithFSAt], so a path outside its file system names no file.
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

	var (
		abs    = path
		absErr error
	)

	switch {
	case !hasDriveLetter(path) || onWindows:
		// The Ref keeps the absolute path rather than the working
		// directory, so every read names the file the key does, wherever
		// the process stands by then.
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
	// a read from disk reports absErr.
	named := abs
	if absErr != nil {
		abs, named = "", slashpath.Clean("/"+filepath.ToSlash(path))
	}

	return Ref{key: fileURL(named), file: path, abs: abs, absErr: absErr}, nil
}

// FileFS creates a [Ref] that names a schema file in fsys, such as a
// schema an [embed.FS] ships beside the program. The Ref is a [Resolver]
// that names the file for every document, as a Ref from [File] is. The
// registry reads path from fsys as written, in slash form, whatever
// file system its other schemas come from, so one registry serves
// bundled schemas and schemas on disk:
//
//	reg := schema.NewRegistry(schema.WithResolvers(
//	    schema.Directive(),
//	    schema.When(isWidget, schema.FileFS(bundle, "schemas/widget.json")),
//	))
//
// The working directory plays no part, and the registry never reads the
// disk for the Ref. An absolute path names no file in fsys, and neither
// does a relative path that leads out of its root, such as
// "../schema.json". Both fail to load with [fs.ErrInvalid].
//
// The registry knows the file by the URL of its path in fsys, such as
// file:///schemas/widget.json, which is its [Ref.Key]. A $ref in the
// schema resolves against that URL, so "defs.json" names
// schemas/defs.json, and the registry reads it from fsys too. A file URL
// in a $ref names a path in fsys the same way, and an HTTP URL is
// fetched as for any other schema.
//
// Two file systems can hold one path, so the registry caches the schema
// by the file system and the path together. It tells one file system
// from another by comparing the values, so fsys must be a value Go can
// compare, such as an [embed.FS], a pointer, or the file system
// [os.DirFS] returns, or a map, such as an [fstest.MapFS], which the
// registry knows by the map itself. A value of any other type, such as
// a struct that holds a slice, fails to load with [fs.ErrInvalid], and a
// pointer to it loads. Each distinct value is a file system of its own,
// so a program builds its file system once and passes the same value
// each time. A new [fs.Sub] or a new [os.Root] per document would add an
// entry to the registry per document.
//
// The registry reads only a regular file of at most 10 MB, as [File]
// describes, and the note there on [fs.StatFS] holds for fsys.
//
// Panics if fsys is nil or path is empty.
func FileFS(fsys fs.FS, path string) Ref {
	if fsys == nil {
		panic("schema.FileFS: fsys is nil")
	}

	ref, err := file(path)
	if err != nil {
		panic("schema.FileFS: " + err.Error())
	}

	return inFS(ref, fsys)
}

// inFS returns ref, a [Ref] from [file], as a Ref that names its path in
// fsys. The key becomes the URL of the path in fsys, with the fragment
// of the key of ref, so the working directory has no part in it. A path
// that names no file in a file system keeps its key, and the read of the
// Ref reports why. A nil fsys, and a Ref that names no file, return ref
// as it is.
func inFS(ref Ref, fsys fs.FS) Ref {
	if fsys == nil || ref.file == "" {
		return ref
	}

	fragment := fileFragment(ref)

	ref.fsys = fsys
	ref.abs, ref.absErr = "", nil

	path, err := filePath(ref)
	if err == nil {
		ref.key = fileURL(path) + fragment
	}

	return ref
}

// An fsDir is the directory on disk that the root of a registry's file
// system stands for, as [WithFSAt] names it.
type fsDir struct {
	// Why the directory has no absolute path, which every read reports.
	err error
	// The directory as an absolute path.
	dir string
	// The directory with its symbolic links resolved.
	physical string
}

// newFSDir returns the [fsDir] for dir. It makes a relative dir absolute
// against the working directory at the time of the call.
func newFSDir(dir string) *fsDir {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return &fsDir{err: fmt.Errorf("no absolute path for directory %s: %w", dir, err)}
	}

	return &fsDir{dir: abs, physical: resolveLinks(abs)}
}

// rel returns the path in the file system that names path, an absolute
// path on disk, in slash form. A path names a file there when it is the
// directory or lies under it as written. Otherwise rel resolves the
// symbolic links in the directories of both and compares them again.
// That second comparison finds a path made absolute against a working
// directory that a link led to. Any other path, and a drive-letter path
// off Windows, is [fs.ErrInvalid].
func (d *fsDir) rel(path string) (string, error) {
	if d.err != nil {
		return "", d.err
	}

	// Off Windows, a drive letter is an ordinary directory name, so the
	// path is relative there, while its key names the drive.
	if hasDriveLetter(path) && !onWindows {
		return "", noDriveLetter()
	}

	// Windows reads a rooted path without a volume, such as \proj\x.json,
	// as relative. A $ref can name one, and it can lie only on the drive
	// of the directory. Off Windows, a rooted path is absolute already.
	if os.IsPathSeparator(path[0]) && filepath.VolumeName(path) == "" && !filepath.IsAbs(path) {
		path = filepath.VolumeName(d.dir) + path
	}

	// The path as written comes first, so a link under the directory
	// reaches the file system, which decides whether to follow it.
	rel, ok := under(d.dir, path)
	if !ok && filepath.IsAbs(path) {
		// Resolve the directory of the file and keep its name, so a file
		// that is itself a link counts where it stands.
		physical := filepath.Join(resolveLinks(filepath.Dir(path)), filepath.Base(path))
		rel, ok = under(d.physical, physical)
	}

	if !ok {
		return "", fmt.Errorf(
			"%w: not under %s, the directory the registry's file system stands for",
			fs.ErrInvalid, d.dir,
		)
	}

	return filepath.ToSlash(rel), nil
}

// under returns path relative to dir, and whether path is dir or lies
// under it.
func under(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return rel, true
}

// resolveLinks returns path, an absolute path, with its symbolic links
// resolved. Where path names nothing on disk, resolveLinks resolves the
// longest leading part that does and keeps the rest as written.
func resolveLinks(path string) string {
	rest := ""

	for dir := path; ; {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(resolved, rest)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}

		dir, rest = parent, filepath.Join(filepath.Base(dir), rest)
	}
}

// isRelative reports whether path, as given to [File], is relative. Such
// a path is not absolute, does not start at a root, and names no drive.
func isRelative(path string) bool {
	return !filepath.IsAbs(path) && !hasDriveLetter(path) &&
		!os.IsPathSeparator(path[0]) && filepath.VolumeName(path) == ""
}

// filePath returns the absolute path that names the file of ref, a [Ref]
// from [File] or [FileFS]. The file URL of that path is the URL the
// registry knows the file by, and [Registry.readFile] reads it.
//
// For a Ref from File, that is the path on disk File made absolute. A
// Ref that File built without a working directory has none. For a Ref
// that names its file in a file system, it is the path as given,
// cleaned, in slash form, and behind a slash, so schemas/config.json
// becomes /schemas/config.json. The working directory plays no part
// there. An absolute path, or one that leads out of the root, is
// [fs.ErrInvalid].
func filePath(ref Ref) (string, error) {
	if ref.fsys == nil {
		if ref.abs == "" {
			return "", fmt.Errorf("read %s: no absolute path: %w", ref.file, ref.absErr)
		}

		return ref.abs, nil
	}

	if !isRelative(ref.file) {
		return "", fmt.Errorf(
			"read %s: %w: an absolute path names no file in the file system of the document or the Ref",
			ref.file, fs.ErrInvalid,
		)
	}

	name := slashpath.Clean(filepath.ToSlash(ref.file))
	if !fs.ValidPath(name) {
		return "", fmt.Errorf(
			"read %s: %w: not a path in the file system of the document or the Ref",
			ref.file,
			fs.ErrInvalid,
		)
	}

	return slashpath.Join("/", name), nil
}

// readFile returns the bytes of the file at path, an absolute path as
// [filePath] returns it for a [Ref] and as a file URL in a $ref names
// it. The file system is the one the Ref names its file in, or nil for a
// Ref that names a file on disk.
//
// With a file system, readFile reads path without its leading slash from
// it, and its errors name that path. A drive-letter path is
// [fs.ErrInvalid] there on every platform. With none, readFile reads
// path from disk, or under [WithFSAt] relative to the directory the file
// system of the registry stands for, where a path outside that directory
// is [fs.ErrInvalid]. Its errors name path either way, and off Windows a
// drive-letter path is invalid the same way.
func (r *Registry) readFile(fsys fs.FS, path string) ([]byte, error) {
	switch {
	case fsys != nil:
		rooted := slashpath.Clean(filepath.ToSlash(path))
		if !strings.HasPrefix(rooted, "/") {
			return nil, fmt.Errorf(
				"read %s: %w: a drive letter names no file in the file system of the document or the Ref",
				path, fs.ErrInvalid,
			)
		}

		// The root itself is "." to an fs.FS.
		name := strings.TrimPrefix(rooted, "/")
		if name == "" {
			name = "."
		}

		return readFS(fsys, name, name)

	case r.fsys == nil:
		return readDisk(path)

	default:
		name, err := r.fsAt.rel(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		return readFS(r.fsys, name, path)
	}
}

// readDisk returns the bytes of the file at abs, an absolute path on
// disk. Off Windows, readDisk refuses a drive-letter path with
// [fs.ErrInvalid].
func readDisk(abs string) ([]byte, error) {
	// Off Windows, a drive letter is an ordinary directory name, so
	// the read would resolve the path against the working directory
	// while the key stays the cwd-independent drive URL. One key would
	// then name different bytes per directory, so refuse the read.
	if hasDriveLetter(abs) && !onWindows {
		return nil, fmt.Errorf("read %s: %w", abs, noDriveLetter())
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

// readFS returns the bytes of the file at fsPath in fsys. Its errors
// name the file by name.
func readFS(fsys fs.FS, fsPath, name string) ([]byte, error) {
	// Stat before the open, as readDisk does. The Stat of [os.DirFS]
	// follows a symbolic link, so a link to a device is not regular.
	// Unlike readDisk, the open takes no flags, since [fs.FS] has none
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

// noDriveLetter returns the [fs.ErrInvalid] error for a drive-letter
// path on a platform other than Windows, where it names no file.
func noDriveLetter() error {
	return fmt.Errorf("%w: a drive letter names no file on %s", fs.ErrInvalid, runtime.GOOS)
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
