// Package reftable finds the anchors of the reference documents of a
// niceyaml Source by name, as niceyaml.WithReferences gives them.
//
// A [Table] reads the trees that each reference Source parsed once.
// Every decode of every Source that reads that reference Source shares
// them. Where the documents define one name more than once, the table
// holds the last anchor of the name in the text. The decoder asks the
// table for the anchor of each alias that reads a reference document. A
// decode thus costs what its aliases reach, whatever the size of the
// reference documents.
//
// A table also keeps what the alias limit weighs for its anchors, as
// [Table.Weights] returns it. The check of a document thus reads through
// the aliases that the reference documents resolve.
package reftable
