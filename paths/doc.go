// Package paths provides utilities for building YAML paths with key/value target specifications.
//
// The [Builder] type wraps [*yaml.PathBuilder] and offers a fluent API for
// constructing paths:
//
//	path := paths.Root().Child("metadata", "name").Key()
//	path.String() // "$.metadata.name.(key)"
//
// The [Path] type combines a [*YAMLPath] with a target [Part] (key or value),
// enabling precise location targeting within YAML documents. Use [Path.Token]
// to resolve the token at a path location within an [ast.File].
//
// [Part] constants [PartKey] and [PartValue] specify whether a path targets
// the key or value of a mapping entry.
package paths
