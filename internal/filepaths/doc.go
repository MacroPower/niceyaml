// Package filepaths matches file paths against glob patterns and
// resolves the paths that a program or a command line names.
//
// This package wraps [github.com/bmatcuk/doublestar/v4] so the file path
// matchers, the input patterns, and the SchemaStore catalog agree on
// pattern syntax. Doublestar patterns add `**` for recursive directory
// matching and `{a,b}` alternatives to what [path/filepath.Match]
// offers. They do not support extglob groups, so `!(config)` matches
// only its literal text.
//
// # Pattern Matching
//
// Use [Pattern] for repeated matching against a validated pattern. Patterns
// follow doublestar syntax:
//
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches zero or more directories when it forms a whole path
//     segment. Inside a longer segment it matches as `*` does.
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//   - `[!abc]` or `[^abc]` matches any character not in the set.
//   - `{a,b}` matches any one of the comma-separated alternatives, and
//     groups can nest.
//   - `\` escapes the character after it, so `\*` matches a literal `*`.
//
// Examples:
//
//	**/*.yaml      # Matches YAML files in any directory.
//	*.yaml         # Matches YAML files in root only.
//	*.{yml,yaml}   # Matches .yml and .yaml files in root only.
//	**/k8s/*.yaml  # Matches YAML files in any k8s directory.
//	config.yaml    # Matches exactly "config.yaml".
//
// # Path Resolution
//
// [filepath.Clean] and [filepath.Abs] drop a ".." element together with
// the element before it, as text. On Unix, the OS instead steps up from
// the directory that a symbolic link leads to. So "link/../config.yaml"
// can name one file for [filepath.Abs] and another for [os.Open]. Use
// [Abs] and [Clean] for a path that the program opens on disk, so the
// label of a file names the file that the read opened. A path that a
// document holds resolves as a URI reference does, as text, and does not
// go through them.
package filepaths
