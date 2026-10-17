// Package decodetree builds the tree the go-yaml decoder reads for one
// document of a niceyaml Source.
//
// The decoder finds an anchor by its name, in the order it reads the
// anchors. It therefore might bind an alias to a different anchor than
// the document does. It reads an alias inside its own anchor as null, but
// writes such an alias as text that never ends. It also rejects an
// integer under a !!int tag. A [Tree] avoids each of these decoder
// faults. It gives each anchor that shares a name a name of its own. It
// holds a null in place of each alias inside its anchor, and spells each
// !!int tag in its verbatim form. It makes each change in a second parse
// of the Source, so the tree the Source shares stays as the parser built
// it. A document that does not need any of these changes reads the tree
// of the Source through [DecodeView].
//
// A decode of a node below the body asks [Tree.Prime] for the anchors
// to register first. The errors of the decoder name the anchors with
// the names the tree gave them, and [Tree.RestoreNames] spells them as
// the document does.
package decodetree
