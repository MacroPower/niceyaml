package input

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/inputhook"
)

// stdinName is the name that messages give standard input.
const stdinName = "<stdin>"

// Input names where the files of a read come from. It names a file or a
// directory on disk, one in an [fs.FS], the text of an [io.Reader] such
// as standard input, or [niceyaml.Source] values that the app built. It
// can also name several inputs in order.
//
// [Input.Files] reads the files of an Input.
// [go.jacobcolvin.com/niceyaml/manifest.Registry.Load] reads the
// manifests in them.
//
// The zero Input does not name a file, so a read of it finds no file.
//
// Create instances with [Path], [FS], [Stdin], [Reader], [Sources], or
// [Join].
type Input struct {
	in *inputDef
}

// inputDef is what an [Input] names.
type inputDef struct {
	fsys   fs.FS
	stream *stream
	path   string
	srcs   []*niceyaml.Source
	ins    []Input
	cfg    config
	kind   inputKind
}

// inputKind names the kind of an [Input].
type inputKind int

// The kinds of [Input].
const (
	inputPath inputKind = iota + 1
	inputFS
	inputStream
	inputSources
	inputList
)

// Path creates a new [Input] that reads the file at p or the files of the
// directory at p. A file that p names reads whatever its name or its
// type, so p can name a pipe. A directory reads the files that match
// "*.yaml" or "*.yml", as the package documentation describes, and opts
// change the selection. A p of "-" names the file "-". [Stdin] reads
// standard input.
//
// [go.jacobcolvin.com/niceyaml/manifest.WriteAtomic] rewrites only the
// files of a Path input.
func Path(p string, opts ...Option) Input {
	return Input{in: &inputDef{kind: inputPath, path: p, cfg: newConfig(opts)}}
}

// FS creates a new [Input] that reads the file or the directory at p in
// fsys, such as an [embed.FS] that ships defaults with the app. It selects
// files as [Path] does.
//
// A walk applies the link rule of Path only to a symbolic link that fsys
// reports through [fs.ReadLinkFS]. A file system reports a link when its
// Lstat describes the link, not the file that the link leads to. A file
// system without the interface reports no link. [fs.Sub] of that file
// system has the methods of the interface and reports no link either,
// because its Lstat follows the link. The same holds for each type that
// forwards Lstat to that file system with [fs.Lstat].
//
// A link whose Lstat fails does not read, and neither does a reported
// link whose ReadLink fails. The [File] of the link holds the error of
// that method, with or without [WithOutsideLinks].
//
// A walk learns that an entry is a link from the listing of its
// directory. A listing can name a link that fsys does not report. Each of
// those links to a regular file fails with an error that matches
// [ErrOutsideLink]. [WithOutsideLinks] makes the walk read what Open
// returns for that link, wherever the link leads. A type that wraps a
// file system must forward ReadLink and Lstat to keep the link rule of
// that file system.
//
// A listing can also describe a link as a regular file or as a
// directory. The walk does not ask fsys about that entry. It reads the
// entry as that file, and under [WithRecursive] it enters the entry as
// that directory. The files that it reads there can lie outside the
// input directory, with or without WithOutsideLinks. A file reads once
// for each path that reaches it through one of those entries. A file
// that one entry and its own path both reach thus reads twice.
func FS(fsys fs.FS, p string, opts ...Option) Input {
	return Input{in: &inputDef{kind: inputFS, fsys: fsys, path: p, cfg: newConfig(opts)}}
}

// Stdin creates a new [Input] that reads standard input, which messages
// name "<stdin>". It reads as [Reader] does, so one Stdin value serves
// each read of it. Each Stdin value reads standard input for itself, and
// a second one finds the text that the first left.
//
// A CLI whose --config flag takes "-" for standard input picks the input
// itself:
//
//	in := input.Path(config)
//	if config == "-" {
//		in = input.Stdin()
//	}
func Stdin(opts ...Option) Input {
	return newStreamInput(stdinName, os.Stdin, inputhook.Stdin, opts)
}

// Reader creates a new [Input] that reads r, such as the body of a
// request. Messages refer to the text as name. The first read of the
// Input reads r to its end, and every later one reuses that text. One
// Input thus serves
// [go.jacobcolvin.com/niceyaml/manifest.Registry.PlanMigration] and the
// load after it.
//
// A read that fails gives the [File] of the text an error, as a file of
// [Path] that the read cannot open does. A text longer than the byte limit
// of [niceyaml.WithByteLimit] does the same. Every later read of the
// Input gives the same error.
func Reader(name string, r io.Reader, opts ...Option) Input {
	return newStreamInput(name, r, inputhook.Reader, opts)
}

// newStreamInput returns the [Input] that reads r under name with opts.
// The kind tells the manifest package why it cannot rewrite the text.
func newStreamInput(name string, r io.Reader, kind inputhook.Kind, opts []Option) Input {
	cfg := newConfig(opts)

	s := &stream{
		name: name,
		kind: kind,
		read: sync.OnceValues(func() (string, error) {
			// The name goes last, so the error of a text past the byte limit
			// names the Input as every other problem of the Input does.
			opts := append(slices.Clone(cfg.sourceOptions), niceyaml.WithName(name))

			src, err := niceyaml.NewSourceFromReader(r, opts...)
			if err != nil {
				return "", err
			}

			return src.Text(), nil
		}),
	}

	return Input{in: &inputDef{kind: inputStream, stream: s, cfg: cfg}}
}

// stream is the text of a [Stdin] or [Reader] input. The first read of
// the input reads the text, and every read after it reuses the text.
type stream struct {
	// The read of the text. The first call reads the reader and returns
	// the text or the problem of the read. Each later call returns what the
	// first one found.
	read func() (string, error)
	name string
	// The place that the text came from.
	kind inputhook.Kind
}

// Sources creates a new [Input] that reads srcs, which the app built
// itself. A read gives each Source as it is, with its own name and its
// own limits.
func Sources(srcs ...*niceyaml.Source) Input {
	return Input{in: &inputDef{kind: inputSources, srcs: srcs}}
}

// Join creates a new [Input] that reads the files of each of ins, in the
// order given. A file that several inputs name reads once, at its first
// place.
func Join(ins ...Input) Input {
	return Input{in: &inputDef{kind: inputList, ins: ins}}
}

// Option configures the selection of the files of a directory, and the
// [niceyaml.Source] of each file, for [Path], [FS], [Stdin], and
// [Reader]. A Stdin or Reader input reads one text, so only
// [WithSourceOptions] changes what it reads. A pattern of [WithInclude] or
// [WithExclude] that a directory input rejects still fails the read.
//
// Available options:
//   - [WithRecursive]
//   - [WithHidden]
//   - [WithOutsideLinks]
//   - [WithInclude]
//   - [WithExclude]
//   - [WithSourceOptions]
type Option func(*config)

// config holds the settings that each [Option] configures.
type config struct {
	include       []string
	exclude       []string
	sourceOptions []niceyaml.SourceOption
	recursive     bool
	hidden        bool
	outsideLinks  bool
}

// newConfig returns the settings of opts.
func newConfig(opts []Option) config {
	var c config

	for _, opt := range opts {
		opt(&c)
	}

	return c
}

// WithRecursive is an [Option] that sets whether a directory input also
// reads its subdirectories. A walk does not enter a directory that a
// directory listing names as a symbolic link. The listing of an [FS]
// input can describe that link as a directory, and the walk then enters
// it, as FS describes.
func WithRecursive(recursive bool) Option {
	return func(c *config) {
		c.recursive = recursive
	}
}

// WithHidden is an [Option] that sets whether a walk keeps each entry
// whose name starts with ".", such as ".github" or ".gitlab-ci.yml".
// Without it, a walk skips those entries before it reads a pattern. With
// it, the patterns of [WithInclude] and [WithExclude] select among those
// files and, under [WithRecursive], among those directories.
//
// A walk with WithHidden meets the temporary files that
// [go.jacobcolvin.com/niceyaml/manifest.WriteAtomic] names
// ".<name>.<hex>", and under [WithRecursive] it enters directories such as
// ".git". The default patterns do not match the temporary files. An app
// that passes an include pattern as broad as "*" should exclude those
// entries with [WithExclude].
//
// A symbolic link can lead to a file in a directory whose name starts
// with ".". A volume that Kubernetes mounts from a ConfigMap keeps its
// files in a directory named "..<timestamp>". Each file beside that
// directory is a link into it. An include pattern that names the
// directory, "..*/*.yaml" for that volume, fails a read without
// WithHidden, as [WithInclude] describes. A recursive walk with
// WithHidden also enters the directory and names each file by its path
// there. An app should exclude the directory from a recursive walk, with
// "..*/" for that volume.
func WithHidden(hidden bool) Option {
	return func(c *config) {
		c.hidden = hidden
	}
}

// WithOutsideLinks is an [Option] that sets whether a walk reads a file
// that a symbolic link leads to outside the input directory. An app
// should pass it for the directory that it reads by default, such as
// ~/.config/cafe. Tools such as GNU stow fill that directory with links to
// files outside it. An app should not pass it for a directory that a
// flag names. [go.jacobcolvin.com/niceyaml/manifest.WriteAtomic] never
// rewrites a file outside the directory.
//
// The walk of a [Path] input opens each file through an [os.Root] of the
// directory. Without the option, the walk thus refuses a link that leads
// outside even when another process changes the directory during the
// read. The refusal covers symbolic links only, so the walk reads a file
// that a hard link names. An app that only reads a directory can also
// open an [os.Root] on the directory and pass [FS] its FS and ".". That
// input keeps the same refusal.
//
// The walk of an [FS] input learns where a link leads only when the file
// system reports the link, as FS describes. A reported link that leaves
// the file system fails with an error that matches [ErrOutsideLink], even
// with the option. A walk follows a link that the file system does not
// report only with the option, and then reads what Open returns. That
// file can lie outside the input directory, and outside the file system
// when Open follows a link there. A file that an unreported link and its
// own path both reach then reads twice.
func WithOutsideLinks(follow bool) Option {
	return func(c *config) {
		c.outsideLinks = follow
	}
}

// WithInclude is an [Option] that replaces the patterns that select the
// files of a directory, "*.yaml" and "*.yml" by default. A pattern reads
// as a .gitignore line without negation, with the glob syntax of
// [go.jacobcolvin.com/niceyaml/matcher.FilePath]. A pattern without
// a "/" matches a name at any depth, and one with a "/" matches the path
// below the input directory.
//
// Without [WithHidden], a walk skips each entry whose name starts with
// ".", so no pattern selects such an entry or an entry below such a
// directory. A read then fails for a pattern with an element that starts
// with a literal ".", such as ".cafe.yaml" or ".github/*.yaml", with an
// error that matches [ErrInvalidPattern]. The "." element of a pattern
// such as "./cafe.yaml" names the directory itself, so it does not fail
// the read.
//
// A walk reads the file that a symbolic link leads to inside the input
// directory only when a pattern also matches the path of that file. That
// path can run through a directory whose name starts with ".". A pattern
// that names the directory, such as ".store/*.yaml", fails the read in the
// same way, so an input with that pattern must pass [WithHidden].
func WithInclude(globs ...string) Option {
	return func(c *config) {
		c.include = append(c.include, globs...)
	}
}

// WithExclude is an [Option] that skips each file and each directory that
// a pattern matches, such as "*.example.yaml" or "testdata/". A pattern
// reads as [WithInclude] describes. A pattern that ends with "/" matches
// only directories.
//
// Without [WithHidden], a walk skips each entry whose name starts with
// "." before it reads a pattern. A pattern that matches only those
// entries, such as ".git/", changes the selection only under [WithHidden].
func WithExclude(globs ...string) Option {
	return func(c *config) {
		c.exclude = append(c.exclude, globs...)
	}
}

// WithSourceOptions is an [Option] that passes opts to each
// [niceyaml.Source] that the input builds, such as
// [niceyaml.WithReferences] or [niceyaml.WithByteLimit]. [Path] and [FS]
// name each file themselves, so [niceyaml.WithName],
// [niceyaml.WithFilePath], and [niceyaml.WithFS] do not affect them.
// [Stdin] and [Reader] name their text themselves, so [niceyaml.WithName]
// does not affect them.
func WithSourceOptions(opts ...niceyaml.SourceOption) Option {
	return func(c *config) {
		c.sourceOptions = append(c.sourceOptions, opts...)
	}
}

// File is one file that [Input.Files] found.
//
// A file that the read could not take in has an Err and a nil Source. Err
// is the problem of the read, of a link that a walk does not follow, or
// of a text past the byte limit of its Source, bound to Name. A file that
// the read took in has a Source and a nil Err. A problem inside its text,
// such as a byte that UTF-8 does not allow, shows in the parse of the
// Source.
type File struct {
	// The problem of the file as a whole, bound to Name, or nil.
	Err error
	// The Source of the file, or nil when Err is set.
	Source *niceyaml.Source
	// The name of the file, as messages show it. It is the name of Source
	// when Source is set.
	Name string
}

// Files reads the files of in and returns them in read order. The
// inputs read in order, the files of a directory sorted by path. A file
// that several inputs or several paths reach reads once, at its first
// place, except for the links that [FS] describes.
//
// The error covers only what fails before Files reads a file. A pattern
// that does not parse fails with an error that matches
// [ErrInvalidPattern]. Without [WithHidden], so does an include pattern
// that names an entry whose name starts with ".", as [WithInclude]
// describes. An input path that does not exist fails with an error that
// matches [fs.ErrNotExist]. A ctx that ends fails with an error that
// matches ctx.Err(). Each other problem belongs to one file, and its
// [File] holds it, so a caller knows which files to skip.
//
// A directory that holds no file that the patterns select gives no file
// and no error.
func (in Input) Files(ctx context.Context) ([]File, error) {
	rd, err := readInputs(ctx, in, nil)
	if err != nil {
		return nil, err
	}

	files := make([]File, 0, len(rd.files))
	for _, f := range rd.files {
		files = append(files, File{Err: f.Err, Source: f.Source, Name: f.Name})
	}

	return files, nil
}

// heading returns the name that the heading of a load of in starts with,
// or "" when the load does not have one. The name is the path of a lone
// [Path] or [FS] input, the name of a lone [Stdin] or [Reader] input, or
// the name of a lone Source.
func (in Input) heading() string {
	d := in.in

	for d != nil && d.kind == inputList && len(d.ins) == 1 {
		d = d.ins[0].in
	}

	switch {
	case d == nil:
		return ""
	case d.kind == inputPath:
		return filepath.Clean(d.path)
	case d.kind == inputFS:
		return cleanFSPath(d.path)
	case d.kind == inputStream:
		return d.stream.name
	case d.kind == inputSources && len(d.srcs) == 1 && d.srcs[0] != nil:
		return d.srcs[0].Name()
	default:
		return ""
	}
}
