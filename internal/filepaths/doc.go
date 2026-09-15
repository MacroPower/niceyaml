// Package filepaths matches file paths against glob patterns.
//
// This package wraps [github.com/bmatcuk/doublestar] so the schema matchers
// and the SchemaStore catalog agree on pattern syntax. It supports extended
// glob patterns including `**` for recursive directory matching, unlike
// [path/filepath.Match].
//
// # Pattern Matching
//
// Use [Pattern] for repeated matching against a validated pattern. Patterns
// follow doublestar syntax:
//
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches any sequence including separators (recursive).
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//
// Examples:
//
//	**/*.yaml      # Matches YAML files in any directory.
//	*.yaml         # Matches YAML files in root only.
//	**/k8s/*.yaml  # Matches YAML files in any k8s directory.
//	config.yaml    # Matches exactly "config.yaml".
package filepaths
