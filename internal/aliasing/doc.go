// Package aliasing holds the limit on how much YAML aliases may make a
// reader read, and the check that applies it to a document before the
// go-yaml decoder reads it.
//
// The decoder writes out the whole content of an alias it spells as text,
// such as an alias used as a key. It also reads a mapping a merge key
// brings in again at every merge, so a document of a few hundred bytes
// can take minutes to decode. [CheckDecode] counts what a decode of a
// node would read and returns [ErrExcessiveAliasing] for a document past
// the limit before anything decodes it. The schema validator and the
// content matcher both run it, so a registry that routes a document by
// its content refuses the same documents its schemas do.
//
// [Excessive] is the limit itself, which the schema validator also
// applies to a decoded value whose maps and slices a decode shares
// between aliases.
package aliasing
