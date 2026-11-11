// Package aliasing holds the checks that apply the alias limit of the
// aliaslimit package to a document before the go-yaml decoder reads it.
// The checks also apply the [Budget] of the source of the document.
//
// The decoder writes out the whole content of an alias that it spells as
// text, such as an alias used as a key. It also reads a mapping that a merge key
// brings in again at every merge. A document of a few hundred bytes can
// therefore take minutes to decode.
//
// [CheckDecode] counts what a decode of the whole document reads. It returns
// a [*RatioError] for a document past the limit before anything decodes a
// node in it that holds an alias. The error matches
// [aliaslimit.ErrExcessiveAliasing] and names the alias that repeats the
// most. Where the decoder writes a node out as text, each alias to a scalar
// copies the text of the scalar. The count therefore weighs such a copy by
// its length in bytes.
//
// Every decode of a niceyaml Node runs [CheckDecode], including the decode
// that the schema validator runs and the decodes of the content and text
// matchers. A registry that routes a document by its content thus refuses
// the same documents that its schemas refuse. A decode by a Node into a
// type that [DecodesTextWith] reports for the unmarshalers of the decode
// runs [CheckDecodeText] as well.
//
// The text that the decoder writes out must also fit the [Budget] of the
// document. The niceyaml package takes the Budget from the document byte
// limit and the depth limit of the source. Those limits bound the text that the
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
// Both counts read through an alias that only a reference document of the
// decode resolves, such as one of niceyaml.WithReferences. The alias
// counts what the anchors of its name hold in the reference documents,
// and [ReferenceWeights] keeps that weight for every document that reads
// them. For the alias limit, a document thus gets the verdict that it gets
// with the reference anchors that it reaches defined inside it. Reference
// anchors that form a loop of names are past the limit instead. An alias
// of a name that several reference anchors share weighs the largest of
// them that a decode can read. A document whose decode reads a smaller
// anchor of the name can thus fail. ReferenceWeights describes what a loop
// and a shared name weigh.
//
// The Budget does not give that same verdict. It bounds the text that the
// aliases of the document write out, and an alias inside a reference
// anchor is not one of them. The text of such an alias spends the Budget
// only where an alias of the document repeats the anchor that holds it.
// With the anchors defined inside the document, each alias inside them
// spends the Budget as well.
//
// The source of a document turns the limit and the Budget off for all of
// these readers with niceyaml.WithAliasLimit. Both checks then pass every
// node of the document.
//
// Every Node of a document shares one [State], so a check of each item of
// a list counts the document once. [StateOf] returns that State. The State
// holds the [Facts] of the document, its anchors and aliases from one
// walk. The decode tree and the layer merge read the Facts as well.
package aliasing
