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
// Each diff under patches fixes a defect of go-yaml's decoder or adds a
// feature that niceyaml reads its errors with. The sync.sh script applies
// the diffs in the order of their names after it copies the files.
// A change to a copied file goes into a diff of its own, or into the diff
// of the fix that it belongs to, so the next sync keeps it. With the
// diffs, the copy differs from go-yaml as follows:
//
//   - A struct that holds a map inline does not make a decode under
//     [DisallowUnknownField] panic, and the map takes every key.
//   - An integer under a !!int tag decodes into each integer type, and a
//     value under the tag that does not spell an integer fails.
//   - An alias inside the anchor that it names reads as null, in a typed
//     target and in the text that the decoder hands an unmarshaler. A `<<`
//     merge of such an alias, alone or in a sequence, fails with a
//     [MergeLoopError], where go-yaml does not find the anchor.
//   - The decoder checks its context as it reads, and [AliasBudget] bounds
//     what it reads through aliases.
//   - The error of a value that decodes itself comes back in an
//     [UnmarshalerError] with the node of that value. The error of the
//     unmarshaler stays as the unmarshaler returned it, with the field that
//     a TypeError in it names. An unmarshaler can return an error of its
//     callback unchanged, and that error stays where the decoder found it.
//   - A nil pointer below the target gets a value, and a pointer that
//     holds a value decodes from that value.
//   - A pointer type that never ends in a type that is not a pointer
//     fails with a TypeError.
//   - A pointer target at an alias to a null becomes nil.
//   - The formatter reads the tokens of a parse once for each decoder.
//   - An alias without an anchor under a merge key, or at a mapping or a
//     sequence of a typed target, fails with a [MissingAnchorError].
//   - An alias with a comment after its name finds its anchor.
//   - With [ResolveAlias], each alias reads the node that a function binds
//     it to, and the decoder holds each anchor by its node. An alias inside
//     the anchor that it reads reads as null, even where the decoder has not
//     read that anchor before. The decoder reads the entries that a merge
//     brings in inside the anchor of their alias, also in a sequence or in
//     a sequence that an alias reads, so the rule holds in those entries
//     too. A merge of such an alias fails with a [MergeLoopError]. The
//     decoder keeps the value of an anchor that an alias reads into an any
//     value, so each later alias takes it. It does not keep the value of a
//     read in which such an alias read as null, because a read from
//     another place can give another value. An inline alias field reads an
//     anchor that the decoder has not read.
//   - A pointer to a node type of the go-yaml ast package points to the node
//     of the document, and not to a copy, also where the node sits under an
//     anchor or a tag.
//   - The decoder names the unknown fields of a mapping in the order of the
//     document, so a decode that returns one names the first. It builds the
//     mapping of an inline field in that order too, so it reads the entries,
//     and finds their errors, in the same order at every decode.
//   - With [CollectErrors], a decode goes on past each error and returns
//     every error it finds, as an [Errors] in the order of the document. A
//     map still stops at the error of a key or of a `<<` merge key. Each
//     error comes in a [PathError] with the path on which the decoder read
//     its value, so a value that several aliases read gives one error for
//     each alias.
//   - With [ReportUnknownField], a decode hands each unknown field to a
//     function in place of returning its error. It hands on the fields that
//     the decoder finds in the anchor of each alias that takes the value
//     of that anchor. It drops the fields of a failed call of the callback
//     of an unmarshaler, unless the unmarshaler returns the error of that
//     call as it is.
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
