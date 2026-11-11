package manifest

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"go.jacobcolvin.com/niceyaml"
)

// stdinName is the name that messages give standard input.
const stdinName = "<stdin>"

// Input names where the documents of a load come from. It names a file or
// a directory on disk, one in an [fs.FS], the text of an [io.Reader] such
// as standard input, or [niceyaml.Source] values that the app built. It
// can also name several inputs in order.
//
// The zero Input does not name a file, so a load of it does not find a
// manifest.
//
// Create instances with [Path], [FS], [Stdin], [Reader], [Sources], or
// [Inputs].
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
	cfg    inputConfig
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
// directory at p. A file that p names loads whatever its name or its
// type, so p can name a pipe. A directory loads the files that match
// "*.yaml" or "*.yml", as the Loading section of the package documentation
// describes, and opts change the selection. A p of "-" names the file "-".
// [Stdin] reads standard input.
//
// [Plan.Apply] rewrites only the files of a Path input, so an app should
// read the files of its user with Path. An app that keeps its files
// elsewhere, such as a server that stores them in a database, writes
// [PlanFile.After] for each file of [Plan.Files] itself.
func Path(p string, opts ...InputOption) Input {
	return Input{in: &inputDef{kind: inputPath, path: p, cfg: newInputConfig(opts)}}
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
// A link whose Lstat fails does not load, and neither does a reported
// link whose ReadLink fails. The load fails with the error of that method,
// with or without [WithOutsideLinks].
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
// input directory, with or without WithOutsideLinks. A file loads once
// for each path that reaches it through one of those entries. A file
// that one entry and its own path both reach thus loads twice.
//
// [Plan.Apply] does not write a file of fsys, and fails it with an error
// that matches [ErrReadOnly].
func FS(fsys fs.FS, p string, opts ...InputOption) Input {
	return Input{in: &inputDef{kind: inputFS, fsys: fsys, path: p, cfg: newInputConfig(opts)}}
}

// Stdin creates a new [Input] that reads standard input, which messages
// name "<stdin>". It reads as [Reader] does, so one Stdin value serves
// [Registry.PlanMigration] and the [Registry.Load] after it. Each Stdin
// value reads standard input for itself, and a second one finds the text
// that the first left.
//
// A CLI whose --config flag takes "-" for standard input picks the input
// itself:
//
//	in := manifest.Path(config)
//	if config == "-" {
//		in = manifest.Stdin()
//	}
func Stdin(opts ...InputOption) Input {
	return newStreamInput(stdinName, os.Stdin, targetStdin, opts)
}

// Reader creates a new [Input] that reads r, such as the body of a
// request. Messages refer to the text as name. The first load or plan
// that reads the Input reads r to its end, and every later one reuses that
// text. One Input thus serves [Registry.PlanMigration] and the
// [Registry.Load] after it.
//
// A load reports a read that fails as it reports a file of [Path] that it
// cannot read. It reports a text longer than the byte limit of
// [niceyaml.WithByteLimit] the same way. Every later load of the Input
// reports the same problem.
func Reader(name string, r io.Reader, opts ...InputOption) Input {
	return newStreamInput(name, r, targetReader, opts)
}

// newStreamInput returns the [Input] that reads r under name with opts.
// The kind tells why Apply cannot rewrite the text.
func newStreamInput(name string, r io.Reader, kind targetKind, opts []InputOption) Input {
	cfg := newInputConfig(opts)

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

// stream is the text of a [Stdin] or [Reader] input. The first load of
// the input reads the text, and every load after it reuses the text.
type stream struct {
	// The read of the text. The first call reads the reader and returns
	// the text or the problem of the read. Each later call returns what the
	// first one found.
	read func() (string, error)
	name string
	// The place that the text came from, which tells why Apply cannot
	// rewrite it.
	kind targetKind
}

// Sources creates a new [Input] that reads srcs, which the app built
// itself. A load reads each Source as it is, with its own name and its own
// limits. It fails a Source whose text is not UTF-8, as it fails a file of
// [Path]. The decodes of a load apply the unmarshalers of
// [WithUnmarshalers] ahead of those of each Source.
func Sources(srcs ...*niceyaml.Source) Input {
	return Input{in: &inputDef{kind: inputSources, srcs: srcs}}
}

// Inputs creates a new [Input] that reads the documents of each of ins, in
// the order given. A file that several inputs name loads once, at its first
// place.
func Inputs(ins ...Input) Input {
	return Input{in: &inputDef{kind: inputList, ins: ins}}
}

// InputOption configures the selection of the files of a directory, and
// the [niceyaml.Source] of each file, for [Path], [FS], [Stdin], and
// [Reader]. A Stdin or Reader input reads one text, so only
// [WithSourceOptions] changes what it reads. A pattern of [WithInclude] or
// [WithExclude] that a directory input rejects still fails the load.
//
// Available options:
//   - [WithRecursive]
//   - [WithHidden]
//   - [WithOutsideLinks]
//   - [WithInclude]
//   - [WithExclude]
//   - [WithSourceOptions]
type InputOption func(*inputConfig)

// inputConfig holds the settings that each [InputOption] configures.
type inputConfig struct {
	include       []string
	exclude       []string
	sourceOptions []niceyaml.SourceOption
	recursive     bool
	hidden        bool
	outsideLinks  bool
}

// newInputConfig returns the settings of opts.
func newInputConfig(opts []InputOption) inputConfig {
	var c inputConfig

	for _, opt := range opts {
		opt(&c)
	}

	return c
}

// WithRecursive is an [InputOption] that sets whether a directory input
// also reads its subdirectories. A walk does not enter a directory that
// a directory listing names as a symbolic link. The listing of an [FS]
// input can describe that link as a directory, and the walk then enters
// it, as FS describes.
func WithRecursive(recursive bool) InputOption {
	return func(c *inputConfig) {
		c.recursive = recursive
	}
}

// WithHidden is an [InputOption] that sets whether a walk keeps each
// entry whose name starts with ".". Without it, a walk skips those entries
// before it reads a pattern. With it, the patterns of [WithInclude] and
// [WithExclude] select among those files and, under [WithRecursive], among
// those directories.
//
// A walk with WithHidden meets the temporary files that [Plan.Apply]
// names ".<name>.<hex>", and under [WithRecursive] it enters directories
// such as ".git". The default patterns do not match the temporary files.
// An app that passes an include pattern as broad as "*" should exclude
// those entries with [WithExclude].
//
// A symbolic link can lead to a file in a directory whose name starts
// with ".". A volume that Kubernetes mounts from a ConfigMap keeps its
// files in a directory named "..<timestamp>". Each file beside that
// directory is a link into it. An include pattern that names the
// directory, "..*/*.yaml" for that volume, fails a load without
// WithHidden, as [WithInclude] describes. A recursive walk with
// WithHidden also enters the directory and names each file by its path
// there. An app should exclude the directory from a recursive walk, with
// "..*/" for that volume.
func WithHidden(hidden bool) InputOption {
	return func(c *inputConfig) {
		c.hidden = hidden
	}
}

// WithOutsideLinks is an [InputOption] that sets whether a walk reads a
// file that a symbolic link leads to outside the input directory. An app
// should pass it for the directory that it reads by default, such as
// ~/.config/cafe. Tools such as GNU stow fill that directory with links to
// files outside it. An app should not pass it for a directory that a
// flag names. [Plan.Apply] never rewrites a file outside the directory.
//
// The walk of an [FS] input learns where a link leads only when the file
// system reports the link, as FS describes. A reported link that leaves
// the file system fails with an error that matches [ErrOutsideLink], even
// with the option. A walk follows a link that the file system does not
// report only with the option, and then reads what Open returns. That
// file can lie outside the input directory, and outside the file system
// when Open follows a link there. A file that an unreported link and its
// own path both reach then loads twice.
func WithOutsideLinks(follow bool) InputOption {
	return func(c *inputConfig) {
		c.outsideLinks = follow
	}
}

// WithInclude is an [InputOption] that replaces the patterns that select
// the files of a directory, "*.yaml" and "*.yml" by default. A pattern
// reads as a .gitignore line without negation, with the glob syntax of
// [go.jacobcolvin.com/niceyaml/schema/matcher.FilePath]. A pattern without
// a "/" matches a name at any depth, and one with a "/" matches the path
// below the input directory.
//
// Without [WithHidden], a walk skips each entry whose name starts with
// ".", so no pattern selects such an entry or an entry below such a
// directory. A load then fails for a pattern with an element that starts
// with a literal ".", such as ".cafe.yaml" or ".github/*.yaml", with an
// error that matches
// [go.jacobcolvin.com/niceyaml/schema/matcher.ErrInvalidPattern]. The "."
// element of a pattern such as "./cafe.yaml" names the directory itself,
// so it does not fail the load.
//
// A walk reads the file that a symbolic link leads to inside the input
// directory only when a pattern also matches the path of that file. That
// path can run through a directory whose name starts with ".". A pattern
// that names the directory, such as ".store/*.yaml", fails the load in the
// same way, so an input with that pattern must pass [WithHidden].
func WithInclude(globs ...string) InputOption {
	return func(c *inputConfig) {
		c.include = append(c.include, globs...)
	}
}

// WithExclude is an [InputOption] that skips each file and each directory
// that a pattern matches, such as "*.example.yaml" or "testdata/". A
// pattern reads as [WithInclude] describes. A pattern that ends with "/"
// matches only directories.
//
// Without [WithHidden], a walk skips each entry whose name starts with
// "." before it reads a pattern. A pattern that matches only those
// entries, such as ".git/", changes the selection only under [WithHidden].
func WithExclude(globs ...string) InputOption {
	return func(c *inputConfig) {
		c.exclude = append(c.exclude, globs...)
	}
}

// WithSourceOptions is an [InputOption] that passes opts to each
// [niceyaml.Source] that the input builds, such as
// [niceyaml.WithReferences] or [niceyaml.WithByteLimit]. [Path] and [FS]
// name each file themselves, so [niceyaml.WithName],
// [niceyaml.WithFilePath], and [niceyaml.WithFS] do not affect them.
// [Stdin] and [Reader] name their text themselves, so [niceyaml.WithName]
// does not affect them. The unmarshalers of a [niceyaml.WithUnmarshalers]
// among opts apply over those of [WithUnmarshalers].
func WithSourceOptions(opts ...niceyaml.SourceOption) InputOption {
	return func(c *inputConfig) {
		c.sourceOptions = append(c.sourceOptions, opts...)
	}
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
