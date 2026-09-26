// Package filepaths matches file paths against glob patterns.
//
// This package wraps [github.com/bmatcuk/doublestar/v4] so the schema
// matchers and the SchemaStore catalog agree on pattern syntax. Doublestar
// patterns add `**` for recursive directory matching and `{a,b}`
// alternatives to what [path/filepath.Match] offers. They do not support
// extglob groups, so `!(config)` matches only its literal text.
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
package filepaths
