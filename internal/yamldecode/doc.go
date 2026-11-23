// Package yamldecode is a copy of the decoder of go-yaml, which every decode
// of niceyaml runs.
//
// The copy holds go-yaml's decode.go, context.go, struct.go, and
// internal/format at the commit that UPSTREAM_COMMIT names. The sync.sh
// script copies them from a checkout of go-yaml at that commit. It rewrites
// the package clause and the import paths of go-yaml's internal packages,
// and it ports go-yaml's decoder and fuzz tests. The copy builds on the
// go-yaml module that go.mod requires for the ast, token, and parser
// packages. So the commit of the copy must match the version that go.mod
// requires. LICENSE holds the license of go-yaml.
//
// Each diff under patches fixes a defect of go-yaml's decoder, and sync.sh
// applies the diffs in the order of their names after it copies the files.
// A change to a copied file goes into a diff of its own, or into the diff
// of the fix that it belongs to, so the next sync keeps it. With the
// diffs, the copy differs from go-yaml as follows:
//
//   - A struct that holds a map inline does not make a decode under
//     [DisallowUnknownField] panic, and the map takes every key.
//   - An integer under a !!int tag decodes into each integer type, and a
//     value under the tag that does not spell an integer fails.
//   - An alias inside the anchor that it names reads as null, in a typed
//     target and in the text that the decoder hands an unmarshaler.
//   - The decoder checks its context as it reads, and [AliasBudget] bounds
//     what it reads through aliases.
//   - The error of an unmarshaler stays as the unmarshaler returned it,
//     with the field that a TypeError in it names.
//   - A nil pointer below the target gets a value, and a pointer that
//     holds a value decodes from that value.
//   - A pointer type that never ends in a type that is not a pointer
//     fails with a TypeError.
//   - A pointer target at an alias to a null becomes nil.
//   - The formatter reads the tokens of a parse once for each decoder.
//   - An alias without an anchor under a merge key, or at a mapping or a
//     sequence of a typed target, fails with a [MissingAnchorError].
//   - An alias with a comment after its name finds its anchor.
//
// The files that sync.sh does not write stand in for the parts of go-yaml
// that the copy needs outside those files. The decode options are copies of
// go-yaml's, because go-yaml's options set the unexported fields of
// go-yaml's own Decoder. The Unmarshal and NodeToValue functions are copies
// of go-yaml's too. The comment, map, validator, and unmarshaler types are
// aliases of go-yaml's types. So a type that implements an unmarshaler
// interface of go-yaml decodes itself through the copy. The errors package
// builds go-yaml's error types, so [errors.As] to a
// [*github.com/goccy/go-yaml.TypeError] matches an error of the copy.
//
// A decode of the copy does not call the functions that
// [github.com/goccy/go-yaml.RegisterCustomUnmarshaler] registers, because
// go-yaml keeps them in unexported package state. Give a decode the
// [CustomUnmarshaler] option instead.
package yamldecode
