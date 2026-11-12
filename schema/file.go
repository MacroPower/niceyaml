package schema

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	slashpath "path"

	"go.jacobcolvin.com/niceyaml/internal/filepaths"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
	"go.jacobcolvin.com/niceyaml/internal/limitread"
)

var (
	// ErrEmptyPath reports an empty path given to [FileOrURL]. An empty
	// path does not name a file.
	ErrEmptyPath = errors.New("schema file path is empty")

	// ErrFileNotAllowed indicates that a registry refused to read a file
	// and did not read anything for it. The file is a path on disk that
	// the [Access] of the registry does not allow, such as one outside the
	// directories of [AllowRoot]. It can also be a path that leads out of the
	// file system of a document or of a [Ref] from [FileFS], such as an
	// absolute path or one that starts with "..". The program set the
	// Access and picked the file system, so the document that names the
	// file is not at fault, and [niceyaml.IsInvalid] reports false for the
	// error. The registry reports the refusal as a [*FetchError] that
	// names the path. A refused URL matches [ErrURLNotAllowed] instead.
	ErrFileNotAllowed = errors.New("file not allowed")
)

// onWindows reports whether the program runs on Windows, the one platform
// where a drive letter names a drive.
const onWindows = runtime.GOOS == "windows"

// driveLen is the length of a Windows drive prefix: a letter and a colon.
const driveLen = 2

// File creates a [Ref] that names a schema file on disk. The Ref is a
// [Resolver] that names the file for every document. File makes a
// relative path absolute against the working directory at the time it
// runs. A later change of working directory leaves the Ref on the same
// file. The registry reads the file with [Registry.Load] when the Ref
// loads, once however many documents name it. It reads only regular files
// within the limit of [WithLoadLimit]. File panics on an empty path.
//
// A '#' in path is part of the file name. [Ref.WithFragment] names a
// subschema of the file:
//
//	schema.File("schemas/defs.json").WithFragment("/definitions/Foo")
//
// # File Systems
//
// A schema shipped in an [embed.FS], or in any other file system, takes
// [FileFS] instead. The registry reads the path of such a Ref from that
// file system, never as a path on disk. The [Access] of the registry decides
// which files on disk it reads. Under [AllowRoot], the registry reads the
// path through an [os.Root], so a path outside the directories of the
// roots does not name a file. See that option for details.
//
// # Cache Key
//
// File names the schema by the file:// URL of the absolute path, such as
// file:///srv/schemas/config.json, whatever [Access] the registry holds.
// A schema that [Embedded] names by a digest of its bytes therefore never
// shares a cache entry with the file. The same holds for a schema that
// another resolver names by a bare path such as "schemas/config.json".
//
// File cleans the absolute path, so spellings of one path that differ
// only in "." segments, repeated separators, or ".." segments that
// follow no symbolic link resolve to the same URL. For example,
// "schemas/config.json" and "./schemas/config.json" from /srv, and
// "/srv/schemas/../schemas/config.json", all resolve to
// file:///srv/schemas/config.json. The registry therefore reads the file
// once and reuses the compiled schema for every document that names it.
// The registry reads the file when the Ref loads, not when File runs. A
// symbolic link and its target resolve to two URLs.
//
// A ".." element that follows a symbolic link steps up from the
// directory that the link leads to, as the OS steps. File replaces the
// path up to that link with its target, all of its links resolved, and
// steps up from there. With link leading to /data/v1,
// "/srv/link/../config.json" thus resolves to file:///data/config.json,
// the file that [os.Open] opens for the path. File checks each element
// before a ".." with [os.Lstat] when it runs. A path whose ".." elements
// follow no link keeps every element as written, so a symlinked name or
// working directory stays in the URL. On Windows, which drops each ".."
// element as text, File cleans the path as text.
//
// # References
//
// A $ref in the schema resolves against the URL of the file, so
// "defs.json" names the file beside it. The registry reads each file or
// HTTP URL that a reference names the way it reads the schema, once
// however many schemas reference it. When a reference fails to load, the
// schema fails to load, as [Registry.Schema] describes. A remote schema
// that names a local file fails to resolve, and the registry does not
// read that file.
//
// # File Limits
//
// The registry reads only regular files, so a path that names a
// directory, a device, or a named pipe fails to load. A file of more
// than the limit of [WithLoadLimit], [DefaultLoadLimit] by default,
// fails to load too, as a response from a [URL] does.
//
// Given a file system, the registry checks the path with [fs.Stat] before
// it opens the file. An [fs.FS] opens a file without flags, so a named
// pipe can still block the read until a writer opens it. A file system
// that implements [fs.StatFS], such as [os.DirFS] or the one that
// [os.Root.FS] returns, answers the check without an open. On such a file
// system, only a named pipe that replaces the file while the registry
// reads it blocks the read. On any other file system, such as the one
// that [fs.Sub] wraps around [os.DirFS], [fs.Stat] opens the file to
// check it. A named pipe already at the path then blocks the read as
// well.
//
// # No Working Directory
//
// A process without a working directory cannot make a relative path
// absolute. Examples include a process that runs in a deleted directory
// and one that runs in a browser. File then names the path by the URL of
// the same path under the root, so "schemas/config.json" becomes
// file:///schemas/config.json. The registry reports the missing directory
// when it loads such a Ref. A Ref from [FileFS] does not need a working
// directory.
//
// # Document Paths
//
// File is for a path written in the program, so it panics on an empty
// path, as [Loadable] panics on an empty key. A reference read from a
// command line might be empty or a URL. Such a reference goes through
// [FileOrURL], which returns an error instead. A reference that a document
// holds, such as the one in a directive, goes through [RefBeside]. File
// returns a [Ref], the type that a [Resolver] returns. A resolver that
// builds the path from the document thus returns the Ref beside a nil
// error:
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
//	        return schema.Ref{}, fmt.Errorf("%w: document has no kind", schema.ErrNoMatch)
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
// name any file that the registry can read. Check such a path before you
// pass it to File, as the example does. You can instead give the registry
// an [Access] from [AllowRoot], so a path outside the directories of its
// roots does not name a file.
func File(path string) Ref {
	ref, err := file(path, true)
	if err != nil {
		panic("schema.File: " + err.Error())
	}

	return ref
}

// file is the form of [File] that returns an error rather than
// panicking. It serves [FileOrURL], which takes a reference from the
// input. An empty path is [ErrEmptyPath]. A path that the process cannot
// make absolute is not an error, and the read of its Ref reports why.
//
// With physical, file makes the path absolute with [filepaths.Abs], so
// the Ref names the file that the OS opens for a path that a program or
// a command line names. Without it, file uses [filepath.Abs], which
// resolves a ".." as text, as a URI reference does, and never reads the
// disk. A path that a document holds, or a path in a file system, takes
// that form.
func file(path string, physical bool) (Ref, error) {
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
		if physical {
			abs, absErr = filepaths.Abs(path)
		} else {
			abs, absErr = filepath.Abs(path)
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
// schema that an [embed.FS] ships beside the program. The Ref is a
// [Resolver] that names the file for every document, as a Ref from [File]
// is. The registry reads path from fsys as written, in slash form,
// whatever file system its other schemas come from. One registry thus
// serves bundled schemas and schemas on disk:
//
//	reg := schema.NewRegistry(
//	    schema.WithAccess(schema.Unrestricted()),
//	    schema.WithResolvers(
//	        schema.Directive(),
//	        schema.When(isWidget, schema.FileFS(bundle, "schemas/widget.json")),
//	    ),
//	)
//
// A '#' in path is part of the file name, as it is for File.
// [Ref.WithFragment] names a subschema of the file, so one bundle serves
// each of its definitions. Its doc says how to escape a definition name
// that the program reads from a document:
//
//	schema.FileFS(bundle, "schemas/defs.json").WithFragment("/definitions/Widget")
//
// The working directory does not affect the Ref, and the registry reads
// it only through fsys, never as a path on disk. An absolute path does
// not name a file in fsys, and neither does a relative path that leads
// out of its root, such as "../schema.json". Both fail to load with
// [ErrFileNotAllowed].
//
// The registry knows the file by the URL of its path in fsys, such as
// file:///schemas/widget.json, which is its [Ref.Key]. A $ref in the
// schema resolves against that URL, so "defs.json" names
// schemas/defs.json, and the registry reads it from fsys too. A file URL
// in a $ref names a path in fsys the same way. The registry fetches an
// HTTP URL in a $ref as it does for any other schema.
//
// Two file systems can hold one path, so the registry caches the schema
// by the file system and the path together. It tells one file system
// from another by comparing the values, so fsys must be a value that Go
// can compare or a map. Values that Go can compare include an
// [embed.FS], a pointer, and the file system that [os.DirFS] returns. The
// registry knows a map, such as an [fstest.MapFS], by the map itself. A
// value of any other type, such as a struct that holds a slice, fails to
// load with [fs.ErrInvalid], but a pointer to it loads.
//
// Each distinct value is a file system of its own, so a program builds
// its file system once and passes the same value each time. A new
// [fs.Sub] or a new [os.Root] for each document adds a separate set of
// entries to the registry.
//
// The registry holds fsys for as long as it keeps an entry from it: a
// schema, a remembered failure, or a $ref document. [WithCacheLimit]
// bounds the entries, and with them the file systems that the registry
// holds. A registry without a limit holds each file system until
// [Registry.Forget] or [Registry.Purge] drops the entries from it. A
// program passes each file system to Forget when it stops using that
// file system.
//
// The registry reads only regular files within the limit of
// [WithLoadLimit], as [File] describes. The note in File on [fs.StatFS]
// applies to fsys.
//
// FileFS panics if fsys is nil or path is empty.
func FileFS(fsys fs.FS, path string) Ref {
	if fsys == nil {
		panic("schema.FileFS: fsys is nil")
	}

	ref, err := file(path, false)
	if err != nil {
		panic("schema.FileFS: " + err.Error())
	}

	return inFS(ref, fsys)
}

// inFS returns ref, a [Ref] from [file], as a Ref that names its path in
// fsys. The key becomes the URL of the path in fsys, with the fragment
// of the key of ref. The working directory therefore does not affect the
// key. A path that does not name a file in a file system keeps its key,
// and the read of the Ref reports why. For a nil fsys, or a Ref that does
// not name a file, inFS returns ref as it is.
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

// An fsDir is the directory on disk that a root of [AllowRoot] holds.
type fsDir struct {
	// The directory as an absolute path.
	dir string
	// The directory with its symbolic links resolved.
	physical string
}

// newFSDir returns the [fsDir] for root. It makes the name of root
// absolute against the working directory at the time of the call, with
// [filepaths.Abs], so a ".." climbs as the climb of [os.OpenRoot] does.
// It returns an error when that path does not name the directory that
// root holds.
func newFSDir(root *os.Root) (*fsDir, error) {
	name := root.Name()

	abs, err := filepaths.Abs(name)
	if err != nil {
		return nil, fmt.Errorf("no absolute path for directory %s: %w", name, err)
	}

	held, err := root.Stat(".")
	if err != nil {
		return nil, fmt.Errorf("root %s: %w", name, err)
	}

	named, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("root %s: %w", name, err)
	}

	if !os.SameFile(held, named) {
		return nil, fmt.Errorf("root %s: %s names a directory other than the one the root holds", name, abs)
	}

	return &fsDir{dir: abs, physical: resolveLinks(abs)}, nil
}

// rel returns the path in the file system that names path, an absolute
// path on disk, in slash form. It also reports whether the directory
// holds path as written, which is when path is the directory or lies
// under it.
func (d *fsDir) rel(path string) (string, bool) {
	rel, ok := under(d.dir, d.onVolume(path))
	if !ok {
		return "", false
	}

	return filepath.ToSlash(rel), true
}

// relPhysical is [fsDir.rel] for a path whose links lead to the
// directory, as [resolveLinksTo] returns it. It compares that path with
// the directory with its symbolic links resolved.
func (d *fsDir) relPhysical(path string) (string, bool) {
	rel, ok := under(d.physical, path)
	if !ok {
		return "", false
	}

	return filepath.ToSlash(rel), true
}

// holds reports whether path, a path with its symbolic links resolved,
// is the directory of d with its links resolved or lies under it.
func (d *fsDir) holds(path string) bool {
	_, ok := under(d.physical, path)

	return ok
}

// onVolume returns path with the volume of d before it when path is
// rooted but has no volume. Windows reads such a path, as in
// \proj\x.json, as relative. A $ref can name one, and it can lie only on
// the drive of the directory. Off Windows, a rooted path is absolute
// already, so onVolume returns it unchanged.
func (d *fsDir) onVolume(path string) string {
	if os.IsPathSeparator(path[0]) && filepath.VolumeName(path) == "" && !filepath.IsAbs(path) {
		return filepath.VolumeName(d.dir) + path
	}

	return path
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
// resolved. Where path does not name anything on disk, resolveLinks
// resolves the longest leading part that does and keeps the rest as
// written.
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

// maxLinks is the number of symbolic links that [resolveLinksTo] follows
// for one path, as [filepath.EvalSymlinks] does.
const maxLinks = 255

// resolveLinksTo returns the directory of path, an absolute path, with
// the symbolic links that lead to stop resolved. It also returns the
// names of path after that directory as written. It resolves one name at
// a time from the top of the path. It stops at the first resolved part
// for which stop reports true, and it never follows a link under such a
// part.
//
// The names after the stop can hold a ".." from the target of a link. A
// ".." after a link steps up from the target of the link, not from the
// link. While a ".." remains, resolveLinksTo therefore goes on past the
// stop through the directories up to the first link. The returned names
// then start at that link, and the file system of the caller resolves
// them.
//
// The name of path stays as written, so a file that is itself a link
// counts where it stands. Where a name does not name anything on disk,
// resolveLinksTo keeps it and the names after it as written. When the
// links loop, it returns the directory and name of path.
func resolveLinksTo(path string, stop func(string) bool) (string, []string) {
	dir := filepath.Dir(path)
	vol := filepath.VolumeName(dir)
	resolved := vol + string(filepath.Separator)
	names := splitNames(dir[len(vol):])
	links := 0

	for len(names) > 0 {
		held := stop(resolved)
		if held && !slices.Contains(names, "..") {
			break
		}

		name := names[0]

		// The resolved part holds no link, so a ".." steps up from the
		// directory that the OS steps up from.
		if name == "." || name == ".." {
			names = names[1:]

			if name == ".." {
				resolved = filepath.Dir(resolved)
			}

			continue
		}

		next := filepath.Join(resolved, name)

		info, err := os.Lstat(next)
		if err != nil {
			break
		}

		if info.Mode()&fs.ModeSymlink == 0 {
			names = names[1:]
			resolved = next

			continue
		}

		// A link inside a directory of stop stays for the caller to follow
		// or refuse.
		if held {
			break
		}

		links++
		if links > maxLinks {
			return filepath.Dir(path), []string{filepath.Base(path)}
		}

		target, err := os.Readlink(next)
		if err != nil {
			break
		}

		names = names[1:]

		// A link that starts at a root, with or without a volume, leads
		// from the top of the disk. Any other link leads from resolved.
		if targetVol := filepath.VolumeName(target); targetVol != "" ||
			(target != "" && os.IsPathSeparator(target[0])) {
			if targetVol == "" {
				targetVol = filepath.VolumeName(resolved)
			}

			resolved = targetVol + string(filepath.Separator)
			target = target[len(filepath.VolumeName(target)):]
		}

		names = append(splitNames(target), names...)
	}

	return resolved, append(names, filepath.Base(path))
}

// splitNames returns the names in path between its separators.
func splitNames(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == filepath.Separator
	})
}

// isRelative reports whether path, as given to [File], is relative. Such
// a path is not absolute, does not start at a root, and does not name a
// drive.
func isRelative(path string) bool {
	return !filepath.IsAbs(path) && !hasDriveLetter(path) &&
		!os.IsPathSeparator(path[0]) && filepath.VolumeName(path) == ""
}

// filePath returns the absolute path that names the file of ref, a [Ref]
// from [File] or [FileFS]. The file URL of that path is the URL that the
// registry knows the file by, and [Registry.readFile] reads it.
//
// For a Ref from File, the path is the path on disk that File made
// absolute. A Ref that File built without a working directory does not
// have one, and filePath returns an error for it. For a Ref that names
// its file in a file system, the path is the path as given, cleaned, in
// slash form, with a leading slash. For example, schemas/config.json
// becomes /schemas/config.json. The working directory does not affect
// that path. An absolute path, or one that leads out of the root, is
// [ErrFileNotAllowed]. Each error is a [*FetchError] that names the path
// as given.
func filePath(ref Ref) (string, error) {
	if ref.fsys == nil {
		if ref.abs == "" {
			return "", readError(ref.file, fmt.Errorf("no absolute path: %w", ref.absErr))
		}

		return ref.abs, nil
	}

	if !isRelative(ref.file) {
		return "", readError(ref.file, fmt.Errorf(
			"%w: an absolute path names no file in the file system of the document or the Ref",
			ErrFileNotAllowed,
		))
	}

	name := slashpath.Clean(filepath.ToSlash(ref.file))
	if !fs.ValidPath(name) {
		return "", readError(ref.file, fmt.Errorf(
			"%w: not a path in the file system of the document or the Ref",
			ErrFileNotAllowed,
		))
	}

	return slashpath.Join("/", name), nil
}

// readFile returns the bytes of the file at path. The path is absolute,
// as [filePath] returns it for a [Ref] and as a file URL in a $ref names
// it. The file system fsys is the one that the Ref names its file in, or
// nil for a Ref that names a file on disk. It refuses a file of more
// than the load limit of the registry.
//
// With a file system, readFile reads path from it without the leading
// slash, and its errors name that path. A drive-letter path is
// [ErrFileNotAllowed] there on every platform. Without a file system,
// readFile reads path from disk as the [Access] of the registry allows,
// and its errors name path. Off Windows, a drive-letter path does not
// name a file on disk either, as [AllowRoot] and [readDisk] describe.
//
// Each error is a [*FetchError]. A document names the path through a
// $schema directive, and a schema names it through a $ref. The message
// of each error therefore replaces its control characters with printable
// text, as FetchError describes.
func (r *Registry) readFile(fsys fs.FS, path string) ([]byte, error) {
	if fsys == nil {
		return r.access.readFile(path, r.loadLimit)
	}

	rooted := slashpath.Clean(filepath.ToSlash(path))
	if !strings.HasPrefix(rooted, "/") {
		return nil, readError(path, fmt.Errorf(
			"%w: a drive letter names no file in the file system of the document or the Ref",
			ErrFileNotAllowed,
		))
	}

	// The root itself is "." to an fs.FS.
	name := strings.TrimPrefix(rooted, "/")
	if name == "" {
		name = "."
	}

	return readFS(fsys, name, name, r.loadLimit)
}

// readDisk returns the bytes of the file at abs, an absolute path on
// disk, as [readBounded] reads them within limit. Off Windows, readDisk
// refuses a drive-letter path with [fs.ErrInvalid].
func readDisk(abs string, limit int) ([]byte, error) {
	// Off Windows, a drive letter is an ordinary directory name, so
	// the read would resolve the path against the working directory
	// while the key stays the cwd-independent drive URL. One key would
	// then name different bytes per directory, so refuse the read.
	if hasDriveLetter(abs) && !onWindows {
		return nil, readError(abs, noDriveLetter())
	}

	// Stat before the open, since opening a FIFO blocks until a writer
	// opens the other end. A FIFO can replace the file after the Stat,
	// so openFile also opens without waiting where the platform allows
	// it, and readBounded checks the opened file again.
	info, err := os.Stat(abs)
	if err != nil {
		return nil, readError(abs, err)
	}

	if !info.Mode().IsRegular() {
		return nil, readError(abs, notRegular())
	}

	f, err := openFile(abs)
	if err != nil {
		return nil, readError(abs, err)
	}
	defer f.Close() //nolint:errcheck // Best-effort close.

	return readBounded(f, abs, limit)
}

// readFS returns the bytes of the file at fsPath in fsys, as
// [readBounded] reads them within limit. Its errors name the file by name.
func readFS(fsys fs.FS, fsPath, name string, limit int) ([]byte, error) {
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
		return nil, readError(name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, readError(name, notRegular())
	}

	f, err := fsys.Open(fsPath)
	if err != nil {
		return nil, readError(name, err)
	}
	defer f.Close() //nolint:errcheck // Best-effort close.

	return readBounded(f, name, limit)
}

// rootFiles is an [fs.FS] over an [*os.Root] that takes a name with ".."
// in it, which [os.Root.FS] refuses. It passes the name to the root as
// written, so the root follows each link before a ".." and refuses a
// name that leads out of its directory.
type rootFiles struct {
	root *os.Root
}

// Open opens the file at name, a path in slash form, through the root.
func (f rootFiles) Open(name string) (fs.File, error) {
	file, err := f.root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of the root names the file.
	}

	return file, nil
}

// Stat returns the [fs.FileInfo] of the file at name, a path in slash
// form, through the root.
func (f rootFiles) Stat(name string) (fs.FileInfo, error) {
	info, err := f.root.Stat(filepath.FromSlash(name))
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of the root names the file.
	}

	return info, nil
}

// readBounded returns the bytes of f, the file at name. The file at the
// path can change after the check of the caller, so readBounded checks
// again that f is a regular file. It refuses a file of more than limit
// bytes, the same limit that a schema fetched from a URL has, and a limit
// of [math.MaxInt] reads a file of any size. Each error is a [*FetchError]
// that names the file by name.
func readBounded(f fs.File, name string, limit int) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, readError(name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, readError(name, notRegular())
	}

	data, err := limitread.ReadAll(f, limit)
	if err != nil {
		return nil, readError(name, err)
	}

	if len(data) > limit {
		return nil, readError(name, httpfetch.TooLarge("file", limit))
	}

	return data, nil
}

// notRegular returns the [fs.ErrInvalid] error for a path that names
// something other than a regular file, such as a directory, a device, or
// a named pipe.
func notRegular() error {
	return fmt.Errorf("%w: not a regular file", fs.ErrInvalid)
}

// noDriveLetter returns the [fs.ErrInvalid] error for a drive-letter
// path on a platform other than Windows, where it does not name a file.
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
