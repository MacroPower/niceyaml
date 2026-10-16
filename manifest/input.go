package manifest

import (
	"io/fs"
	"path/filepath"

	"go.jacobcolvin.com/niceyaml"
)

// Input names where the documents of a load come from: a file or a
// directory on disk, one in an [fs.FS], [niceyaml.Source] values that the
// app built, or several inputs in order.
//
// The zero Input names no file, so a load of it finds no manifest.
//
// Create instances with [Path], [FS], [Sources], or [Inputs].
type Input struct {
	in *inputDef
}

// inputDef is what an [Input] names.
type inputDef struct {
	fsys fs.FS
	path string
	srcs []*niceyaml.Source
	ins  []Input
	cfg  inputConfig
	kind inputKind
}

// inputKind tells the kinds of [Input] apart.
type inputKind int

// The kinds of [Input].
const (
	inputPath inputKind = iota + 1
	inputFS
	inputSources
	inputList
)

// Path creates a new [Input] that reads the file at p, the files of the
// directory at p, or standard input for "-". A file that p names loads
// whatever its name or its type, so p can name a pipe. A directory loads
// the files that match "*.yaml" or "*.yml", as "File Selection" in the
// package documentation describes, and opts change the selection.
//
// [Plan.Apply] rewrites only the files of a Path input, so an app reads
// the files of its user with Path.
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
// the [niceyaml.Source] of each file, for [Path] and [FS].
//
// Available options:
//   - [WithRecursive]
//   - [WithOutsideLinks]
//   - [WithInclude]
//   - [WithExclude]
//   - [WithSourceOptions]
type InputOption func(*inputConfig)

// inputConfig holds the settings each [InputOption] configures.
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
// passes it for the directory it reads by default, such as
// ~/.config/cafe, where tools such as GNU stow link each file out of the
// directory, and not for a directory that a flag names. [Plan.Apply] never
// rewrites a file outside the directory.
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
// pattern reads as [WithInclude] describes, and one that ends with "/"
// matches directories alone.
func WithExclude(globs ...string) InputOption {
	return func(c *inputConfig) {
		c.exclude = append(c.exclude, globs...)
	}
}

// WithSourceOptions is an [InputOption] that hands opts to each
// [niceyaml.Source] that [Path] or [FS] builds, such as
// [niceyaml.WithReferences] or [niceyaml.WithByteLimit]. The input names
// each file itself, so [niceyaml.WithName], [niceyaml.WithFilePath], and
// [niceyaml.WithFS] change nothing. The unmarshalers of a
// [niceyaml.WithUnmarshalers] among opts apply over those of
// [WithUnmarshalers].
func WithSourceOptions(opts ...niceyaml.SourceOption) InputOption {
	return func(c *inputConfig) {
		c.sourceOptions = append(c.sourceOptions, opts...)
	}
}

// heading returns the name that the heading of a load of in starts with,
// or "" when the load has none: the path of a lone [Path] or [FS] input,
// or the name of a lone Source.
func (in Input) heading() string {
	d := in.in

	for d != nil && d.kind == inputList && len(d.ins) == 1 {
		d = d.ins[0].in
	}

	switch {
	case d == nil:
		return ""
	case d.kind == inputPath && d.path == stdinPath:
		return stdinName
	case d.kind == inputPath:
		return filepath.Clean(d.path)
	case d.kind == inputFS:
		return cleanFSPath(d.path)
	case d.kind == inputSources && len(d.srcs) == 1 && d.srcs[0] != nil:
		return d.srcs[0].Name()
	default:
		return ""
	}
}
