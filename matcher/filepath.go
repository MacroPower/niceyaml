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
	// ErrInvalidPattern reports a glob pattern that [FilePath] cannot use.
	// The pattern is one of these:
	//
	//   - An empty pattern, or one whose braces expand only to empty
	//     patterns.
	//   - A pattern whose syntax does not parse.
	//   - A pattern whose braces expand to more patterns, or take more work
	//     to expand, than the limits of brace expansion allow.
	//   - A pattern that keeps a ".." after a glob element such as "*",
	//     which cannot match any cleaned path.
	ErrInvalidPattern = filepaths.ErrInvalidPattern

	// ErrUnresolvedBase reports a [FilePath] matcher whose base directory
	// does not resolve to an absolute path, so the matcher cannot decide. In
	// a process without a working directory, a relative base does not
	// resolve to an absolute path. Pass [WithBaseDir] an absolute path to
	// avoid it.
	ErrUnresolvedBase = errors.New("file path pattern base directory does not resolve")
)

// FilePathOption configures [FilePath].
//
// Available options:
//   - [WithBaseDir]
type FilePathOption func(*filePathConfig)

// filePathConfig holds the settings that [FilePathOption] functions
// configure.
type filePathConfig struct {
	baseDir string
}

// WithBaseDir is a [FilePathOption] that sets the base directory of a
// [FilePath] matcher. A relative pattern matches an absolute file path as
// the path that leads to the file from the base directory. A pattern that
// starts with "**" stays under the base, and a pattern leaves the base
// only through the ".." elements that it writes. Without this option, the
// base is the working directory.
//
// Pass the root of a project, or the directory of the configuration file
// that lists the patterns, so the patterns apply wherever the program
// runs:
//
//	m, err := matcher.FilePath("configs/*.yaml", matcher.WithBaseDir(projectRoot))
//
// A relative dir resolves against the working directory when [FilePath]
// runs. A ".." in dir steps up from the directory that a symbolic link
// before it leads to, as [niceyaml.NewSourceFromFile] describes for the
// file path that it sets. When a caller passes WithBaseDir more than
// once, the last one wins.
//
// WithBaseDir panics if dir is empty.
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
	// The baseErr field holds the error from the resolution of the base
	// directory to an absolute path. Match reports it only when it needs the
	// directory.
	baseErr error
	// The baseDir field holds the base directory as an absolute path.
	baseDir string
	// The raw field holds the pattern as the caller wrote it, braces and
	// all, for the description.
	raw     string
	pattern filepaths.Pattern
}

// FilePath creates a new [Matcher] that matches documents based on a file
// path glob pattern.
//
// Match tests the file path of the document, which is
// [niceyaml.Node.FilePath], against the pattern using doublestar glob
// syntax. FilePath returns [ErrInvalidPattern] for a pattern whose syntax
// does not parse. Use [MustFilePath] for patterns that you know are valid
// at compile time.
//
//	// Matches any YAML file at any depth under the base directory.
//	m, err := matcher.FilePath("**/*.yaml")
//
//	// Matches any YAML file in a k8s directory under the base directory.
//	m, err := matcher.FilePath("**/k8s/*.yaml")
//
//	// Matches YAML files only in the base directory.
//	m, err := matcher.FilePath("*.yaml")
//
//	// Matches any YAML file in a k8s directory, in any location.
//	m, err := matcher.FilePath("/**/k8s/*.yaml")
//
// A relative file path matches as written. Documents that
// [niceyaml.NewSourceFromFS] reads carry the relative path of their file,
// and [niceyaml.WithFilePath] can set one. So "configs/*.yaml" matches a
// document that NewSourceFromFS read from "configs/app.yaml".
//
// [niceyaml.NewSourceFromFile] sets an absolute file path. Match tests a
// relative pattern against the path that leads to that file from the
// base directory, and never against the absolute path as written. Under
// the base "/repo", "configs/*.yaml" matches "/repo/configs/app.yaml",
// whether the caller spelled the file "configs/app.yaml" or
// "configs/sub/../app.yaml". There "**/k8s/*.yaml" matches
// "/repo/deploy/k8s/app.yaml" and does not match "/other/k8s/app.yaml".
//
// When a ".." in the path of the file follows a symbolic link,
// NewSourceFromFile sets a file path with the links of its prefix
// resolved, as it describes. A relative pattern matches that path only
// when the base directory spells the same prefix. So when the base
// reaches its directory through a link, such a file path leaves the
// base, even for a file under it. To match such a file, pass
// [WithBaseDir] a base with its links resolved, or set the file path
// with [niceyaml.WithFilePath].
//
// Wildcards match names and never match a "..", and "**" is one of them.
// So a relative pattern reaches a file outside the base only through the
// ".." elements that it writes, and "../shared/*.yaml" matches
// "/shared/app.yaml" under the base "/repo". Match compares the two
// paths as text and does not follow symbolic links.
//
// A rooted pattern opens with "/" and matches an absolute file path as
// written, whatever the base directory is. Write a pattern for any
// location rooted, such as "/**/k8s/*.yaml". On Windows, an absolute file
// path opens with a volume name, so a rooted pattern there opens with one
// too, as in "C:/**/k8s/*.yaml". When the braces of a pattern hold a
// rooted and a relative alternative, as in "{/etc/app,configs}/*.yaml",
// each alternative follows its own rule.
//
// The base directory is the working directory at the time FilePath runs.
// A later change of directory does not move it.
// [go.jacobcolvin.com/niceyaml/schema.File] fixes a relative path at the
// same moment, so a pattern and the schema that
// [go.jacobcolvin.com/niceyaml/schema.When] pairs with it resolve against
// one directory. [WithBaseDir] names another base. Use it in a program
// that changes directory before it reads its files, or in one whose
// patterns belong to the root of a project.
//
// In a process without a working directory, a relative base does not
// resolve to an absolute path. Match then reports [ErrUnresolvedBase]
// when it tests an absolute file path against a relative pattern. A
// rooted pattern and a relative file path do not need the base.
//
// FilePath also returns [ErrInvalidPattern] for an empty pattern, because
// such a pattern does not match any path and silently disables the
// [Matcher]. The same goes for a pattern such as "{,}" whose braces
// expand only to empty patterns.
//
// FilePath returns [ErrInvalidPattern] for a pattern that keeps a ".."
// after a glob element, as in "configs/*/../x.yaml", because a cleaned
// path holds a ".." only at its start. A ".." after "**" stays valid when
// only ".." and "**" elements come before it in a relative pattern. A
// "**" can match zero directories, so "**/../x.yaml" matches "../x.yaml".
// But FilePath returns [ErrInvalidPattern] for a rooted pattern such as
// "/**/../x.yaml", and for one with a name before the "**", such as
// "a/**/../x.yaml".
//
// The Matcher describes itself as `file path matches "configs/*.yaml"`,
// through [Describer]. That description is the reason that
// [go.jacobcolvin.com/niceyaml/schema.When] gives when it declines. It
// holds the pattern as written, with its braces. The base directory does
// not appear, but an absolute pattern names its own directory.
// [Described] words a pattern that the reader must not see.
func FilePath(pattern string, opts ...FilePathOption) (Matcher, error) {
	p, err := filepaths.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", err, pattern)
	}

	cfg := filePathConfig{baseDir: "."}
	for _, opt := range opts {
		opt(&cfg)
	}

	m := &filePathMatcher{pattern: p, raw: pattern}

	m.baseDir, m.baseErr = filepaths.Abs(cfg.baseDir)

	return m, nil
}

// MustFilePath is like [FilePath] but panics on a pattern that [FilePath]
// reports as [ErrInvalidPattern].
//
// Use it for patterns that you know are valid at compile time:
//
//	matcher.MustFilePath("**/k8s/*.yaml")
//
// Package-level variables initialize before main runs. A MustFilePath
// call in one takes the directory that the program started in as its
// base, unless [WithBaseDir] names another.
func MustFilePath(pattern string, opts ...FilePathOption) Matcher {
	m, err := FilePath(pattern, opts...)
	if err != nil {
		panic("matcher.MustFilePath: " + err.Error())
	}

	return m
}

// Describe implements [Describer].
func (m *filePathMatcher) Describe() string {
	return fmt.Sprintf("file path matches %q", m.raw)
}

// Match implements [Matcher].
func (m *filePathMatcher) Match(_ context.Context, doc *niceyaml.Node) (bool, error) {
	path := doc.FilePath()

	// Match does not know the directory that a relative path leads from,
	// so that path matches as written.
	if !filepath.IsAbs(path) {
		return m.pattern.Match(path), nil
	}

	// A rooted pattern names its own directory, so it does not need the
	// base.
	if m.pattern.MatchRooted(path) {
		return true, nil
	}

	if !m.pattern.Relative() {
		return false, nil
	}

	if m.baseErr != nil {
		return false, fmt.Errorf("%w: %w", ErrUnresolvedBase, m.baseErr)
	}

	// Rel fails for a path with no route from the base, such as one on
	// another Windows volume, and no relative pattern matches that path.
	rel, err := filepath.Rel(m.baseDir, path)
	if err != nil {
		return false, nil //nolint:nilerr // A path outside the base is a plain no.
	}

	return m.pattern.MatchRelative(rel), nil
}
