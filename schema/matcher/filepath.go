package matcher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

var (
	// ErrInvalidPattern reports a glob pattern [FilePath] cannot use. That is
	// an empty pattern or one whose braces expand only to empty patterns, one
	// whose syntax does not parse, or one whose braces expand to more
	// patterns than matching can afford. It is also a pattern that keeps a
	// ".." after a glob element such as "*", which no cleaned path can match.
	ErrInvalidPattern = filepaths.ErrInvalidPattern

	// ErrNoBaseDir reports a [FilePath] matcher that cannot decide because
	// its base directory has no absolute path, which happens to a relative
	// base in a process without a working directory.
	ErrNoBaseDir = errors.New("file path pattern has no base directory")
)

// FilePathOption configures [FilePath].
//
// Available options:
//   - [WithBaseDir]
type FilePathOption func(*filePathConfig)

// filePathConfig holds the settings a [FilePathOption] configures.
type filePathConfig struct {
	baseDir string
}

// WithBaseDir is a [FilePathOption] that sets the base directory of a
// [FilePath] matcher, which is the directory a relative pattern matches
// an absolute file path under. Without it, the base is the working
// directory.
//
// A program whose patterns belong to the root of a project, or to the
// directory of the configuration file that lists them, names that
// directory, and the patterns then apply wherever the program runs:
//
//	m, err := matcher.FilePath("configs/*.yaml", matcher.WithBaseDir(projectRoot))
//
// A relative dir resolves against the working directory when [FilePath]
// runs. Given more than once, the last option wins.
//
// Panics if dir is empty.
func WithBaseDir(dir string) FilePathOption {
	if dir == "" {
		panic("matcher.WithBaseDir: dir is empty")
	}

	return func(c *filePathConfig) {
		c.baseDir = dir
	}
}

// filePathMatcher matches documents by file path glob pattern.
type filePathMatcher struct {
	// Why the base directory has no absolute path, which Match reports
	// when it needs the directory.
	baseErr error
	// The base directory as an absolute path.
	baseDir string
	pattern filepaths.Pattern
}

// FilePath creates a new [Matcher] that matches documents based on a file path
// glob pattern.
//
// Match tests the file path of the document, which is
// [niceyaml.Node.FilePath], against the pattern using doublestar glob
// syntax. A pattern whose syntax does not parse comes back as
// [ErrInvalidPattern]. Use [MustFilePath] for patterns known to be valid at
// compile time.
//
//	// Matches any YAML file recursively.
//	m, err := matcher.FilePath("**/*.yaml")
//
//	// Matches any YAML file in k8s directories.
//	m, err := matcher.FilePath("**/k8s/*.yaml")
//
//	// Matches YAML files only in the base directory.
//	m, err := matcher.FilePath("*.yaml")
//
// A relative file path matches as written. That is the path of a
// document [niceyaml.NewSourceFromFS] read or one
// [niceyaml.WithFilePath] named, so "configs/*.yaml" matches
// "configs/app.yaml" in a bundle.
//
// An absolute file path, which [niceyaml.NewSourceFromFile] sets,
// matches as written too, so "**/k8s/*.yaml" and "/etc/app/*.yaml" match
// it in any directory. It also matches as the path that leads to it from
// the base directory. Under the base "/repo", "configs/*.yaml" matches
// "/repo/configs/app.yaml" however the caller spelled the file, and
// "../shared/*.yaml" matches "/shared/app.yaml". Match compares the two
// paths as text and follows no symbolic link.
//
// The base directory is the working directory at the time FilePath
// runs, and a later change of directory does not move it.
// [go.jacobcolvin.com/niceyaml/schema.File] fixes a relative path at the
// same moment, so a pattern and the schema that
// [go.jacobcolvin.com/niceyaml/schema.When] pairs with it resolve
// against one directory. [WithBaseDir] names another base, for a program
// that changes directory before it reads its files or whose patterns
// belong to the root of a project.
//
// In a process without a working directory, a relative base has no
// absolute path. Match then reports [ErrNoBaseDir] for an absolute file
// path that a relative pattern does not match as written.
//
// An empty pattern is [ErrInvalidPattern] too, since it would match nothing
// and silently disable the [Matcher], and so is a pattern such as "{,}"
// whose braces expand only to empty patterns. So is a pattern that keeps
// a ".." after a glob element, as in "configs/*/../x.yaml", because a
// cleaned path holds a ".." only at its start. A ".." after "**" stays
// valid when only ".." and "**" elements come before it in a relative
// pattern, since "**" can match no directory at all, so "**/../x.yaml"
// matches "../x.yaml". A rooted pattern such as "/**/../x.yaml", or one
// with a name before the "**" such as "a/**/../x.yaml", is
// [ErrInvalidPattern].
func FilePath(pattern string, opts ...FilePathOption) (Matcher, error) {
	p, err := filepaths.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", err, pattern)
	}

	cfg := filePathConfig{baseDir: "."}
	for _, opt := range opts {
		opt(&cfg)
	}

	m := &filePathMatcher{pattern: p}

	m.baseDir, m.baseErr = filepath.Abs(cfg.baseDir)

	return m, nil
}

// MustFilePath is like [FilePath] but panics on a pattern [FilePath]
// reports as [ErrInvalidPattern].
//
// Use it for patterns known to be valid at compile time:
//
//	matcher.MustFilePath("**/k8s/*.yaml")
//
// A package-level variable calls MustFilePath before main runs, so its
// base directory is the directory the program started in unless
// [WithBaseDir] names one.
func MustFilePath(pattern string, opts ...FilePathOption) Matcher {
	m, err := FilePath(pattern, opts...)
	if err != nil {
		panic("matcher.MustFilePath: " + err.Error())
	}

	return m
}

// Match implements [Matcher].
func (m *filePathMatcher) Match(_ context.Context, doc *niceyaml.Node) (bool, error) {
	path := doc.FilePath()

	if m.pattern.Match(path) {
		return true, nil
	}

	// Only a relative pattern matches a path that leads from the base,
	// and only an absolute path has one.
	if !filepath.IsAbs(path) || !m.pattern.Relative() {
		return false, nil
	}

	if m.baseErr != nil {
		return false, fmt.Errorf("%w: %w", ErrNoBaseDir, m.baseErr)
	}

	// Rel fails for a path with no route from the base, such as one on
	// another Windows volume, and no relative pattern matches that path.
	rel, err := filepath.Rel(m.baseDir, path)
	if err != nil {
		return false, nil //nolint:nilerr // A path outside the base is a plain no.
	}

	return m.pattern.Match(rel), nil
}
