// Package paths builds YAML paths that distinguish between keys and values.
//
// Standard YAML paths (like JSONPath) point to nodes in a document, but they
// cannot distinguish between a mapping's key and its value.
//
// When a path like `$.metadata.name` resolves to a mapping entry, you get the
// value node, but for error highlighting or precise editing, you often need the
// key instead.
//
// This package extends [yaml.Path] with [Part] targeting, so you can specify
// whether a path refers to the key or value of a mapping entry:
//
//	keyPath := paths.Root().Child("metadata", "name").Key()
//	keyPath.String() // "$.metadata.name.(key)"
//
//	valPath := paths.Root().Child("metadata", "name").Value()
//	valPath.String() // "$.metadata.name.(value)"
//
// Both paths resolve to the same YAML node, but [Path.Token] returns different
// tokens: the key token "name" for the first, the value token for the second.
//
// # Integration with niceyaml.Error
//
// [Path] is directly usable with [niceyaml.WithPath] to highlight either keys
// or values in error messages:
//
//	err := niceyaml.NewError(
//		"invalid value",
//		niceyaml.WithPath(paths.Root().Child("spec", "replicas").Value()),
//		niceyaml.WithSource(source),
//	)
//
// # Parsing Path Expressions
//
// Use [FromString] to parse a path expression string into a [Builder]:
//
//	b, err := paths.FromString("$.metadata.name")
//	keyPath := b.Key()    // targets the key
//	valPath := b.Value()  // targets the value
//
// [MustFromString] panics on invalid input, useful for compile-time constants:
//
//	path := paths.MustFromString("$.items[0].name").Value()
//
// # Building Paths
//
// Use [Root] to start a [Builder], chain selectors, and finalize with
// [Builder.Key] or [Builder.Value]:
//
//	paths.Root().Child("items").Index(0).Child("name").Key()  // $.items[0].name.(key)
//	paths.Root().Child("spec").IndexAll().Value()             // $.spec[*].(value)
//	paths.Root().Recursive("name").Value()                    // $..name.(value)
//
// For the underlying [YAMLPath] without targeting, use [Builder.Path].
package paths
