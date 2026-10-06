// Package aliasing holds the check that applies the alias limit of the
// aliaslimit package to a document before the go-yaml decoder reads it.
//
// The decoder writes out the whole content of an alias it spells as text,
// such as an alias used as a key. It also reads a mapping a merge key
// brings in again at every merge, so a document of a few hundred bytes
// can take minutes to decode. [CheckDecode] counts what a decode of the
// whole document would read and returns
// [aliaslimit.ErrExcessiveAliasing] for a document past the limit before
// anything decodes a node in it that holds an alias. Where the decoder
// writes a node out as text, each alias to a scalar copies the text of
// the scalar, so the count weighs such a copy by its length in bytes.
// Every decode of a niceyaml Node runs [CheckDecode], the decode the
// schema validator runs among them, and the content matcher runs it
// before it decodes too. A registry that routes a document by its content
// thus refuses the same documents its schemas do. A decode into a type
// that [DecodesText] reports, by a Node or by the content matcher, runs
// [CheckDecodeText] as well.
//
// Neither count can see a reference document of a decode, so an alias to
// one of its anchors counts as one node. [HoldsReferenceAlias] reports a
// document that holds such an alias, and a caller that reads the decoded
// value again at every alias then limits that value itself.
//
// The source of a document turns the limit off for all of these readers
// with niceyaml.WithAliasLimit. Both checks then pass every node of the
// document, and a caller that limits a decoded value asks [Limited]
// whether the limit applies.
package aliasing
