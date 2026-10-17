// Package aliasing holds the checks that apply the alias limit of the
// aliaslimit package to a document before the go-yaml decoder reads it.
// The checks also apply the [Budget] of the source of the document.
//
// The decoder writes out the whole content of an alias that it spells as
// text, such as an alias used as a key. It also reads a mapping that a merge key
// brings in again at every merge. A document of a few hundred bytes can
// therefore take minutes to decode.
//
// [CheckDecode] counts what a decode of the whole document reads. It
// returns [aliaslimit.ErrExcessiveAliasing] for a document past the limit
// before anything decodes a node in it that holds an alias. Where the
// decoder writes a node out as text, each alias to a scalar copies the
// text of the scalar. The count therefore weighs such a copy by its length
// in bytes.
//
// Every decode of a niceyaml Node runs [CheckDecode], including the decode
// that the schema validator runs. The content and text matchers also run
// it before they decode. A registry that routes a document by its content
// thus refuses the same documents that its schemas refuse. A decode into a
// type that [DecodesText] reports, by a Node or by the content matcher,
// runs [CheckDecodeText] as well. So does a decode by a Node into a type
// that [DecodesTextWith] reports for the unmarshalers that its options
// name.
//
// The text that the decoder writes out must also fit the [Budget] of the
// document. The niceyaml package takes the Budget from the byte limit and
// the depth limit of the source. Those limits bound the text that the
// parser reads. The aliases of a document within those limits can still
// write out far more text, or nest it far deeper.
//
// [CheckDecodeText] refuses such a document with an [*ExpansionError] at
// the alias that takes the text past a bound. It weighs the text of each
// alias as a writer in block style gives it, or as a copy of the text of
// the document gives it, whichever is longer. A niceyaml merge of layers
// runs CheckDecodeText on each layer before it writes the merged text, so
// the Budget bounds that text too.
//
// Neither count can see a reference document of a decode, so an alias to
// one of its anchors counts as one node. [HoldsReferenceAlias] reports a
// document that holds such an alias. A caller that reads the decoded
// value again at every alias then limits that value itself.
//
// The source of a document turns the limit and the Budget off for all of
// these readers with niceyaml.WithAliasLimit. Both checks then pass every
// node of the document, and a caller that limits a decoded value asks
// [Limited] whether the limit applies.
//
// Every Node of a document shares one [State], so a check of each item of
// a list counts the document once. [StateOf] returns that State. The State
// holds the [Facts] of the document, its anchors and aliases from one
// walk. The decode tree and the layer merge read the Facts as well.
package aliasing
