package manifest

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
// read the files of its user with Path.
func Path(p string, opts ...InputOption) Input {
	return Input{in: &inputDef{kind: inputPath, path: p, cfg: newInputConfig(opts)}}
}

// FS creates a new [Input] that reads the file or the directory at p in
// fsys, such as an [embed.FS] that ships defaults with the app. It selects
// files as [Path] does. A walk follows the symbolic links of a file system
// that reports them through [fs.ReadLinkFS], and reads what Open returns
// in any other file system.
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
			src, err := niceyaml.NewSourceFromReader(r, cfg.sourceOptions...)
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
// [WithExclude] that does not parse still fails the load.
//
// Available options:
//   - [WithRecursive]
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
// also reads its subdirectories. A walk never enters a directory that a
// symbolic link names.
func WithRecursive(recursive bool) InputOption {
	return func(c *inputConfig) {
		c.recursive = recursive
	}
}

// WithOutsideLinks is an [InputOption] that sets whether a walk reads a
// file that a symbolic link leads to outside the input directory. An app
// should pass it for the directory that it reads by default, such as
// ~/.config/cafe. Tools such as GNU stow fill that directory with links to
// files outside it. An app should not pass it for a directory that a
// flag names. [Plan.Apply] never rewrites a file outside the directory.
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
func WithInclude(globs ...string) InputOption {
	return func(c *inputConfig) {
		c.include = append(c.include, globs...)
	}
}

// WithExclude is an [InputOption] that skips each file and each directory
// that a pattern matches, such as "*.example.yaml" or "testdata/". A
// pattern reads as [WithInclude] describes. A pattern that ends with "/"
// matches only directories.
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
