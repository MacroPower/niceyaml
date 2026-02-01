// Package filepaths provides standardized glob pattern matching for file paths.
//
// This package wraps [github.com/bmatcuk/doublestar] to provide consistent glob
// pattern matching throughout the codebase. It supports extended glob patterns
// including `**` for recursive directory matching, unlike [path/filepath.Glob].
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
//
// # File System Globbing
//
// Use [Glob] to expand patterns against the file system, supporting ** for
// recursive directory matching unlike [path/filepath.Glob].
//
// Use [ContainsGlobChars] to detect whether a string contains glob metacharacters.
package filepaths
