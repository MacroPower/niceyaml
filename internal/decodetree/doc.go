// Package decodetree builds the tree the go-yaml decoder reads for one
// document of a niceyaml Source.
//
// The decoder finds an anchor by its name, in the order it reads the
// anchors. It therefore might bind an alias to a different anchor than
// the document does. It also reads an alias inside its own anchor as null
// only while it reads that anchor, but the document reads such an alias as
// null wherever a read reaches it. A [Tree] gives each anchor that shares a
// name a name of its own, and holds a null in place of each alias inside
// its anchor. It makes each change in a second parse of the Source, so the
// tree the Source shares stays as the parser built it. A document that does
// not need any of these changes reads the tree of the Source itself.
//
// A decode of a node below the body asks [Tree.Prime] for the anchors
// to register first. The errors of the decoder name the anchors with
// the names the tree gave them, and [Tree.RestoreNames] spells them as
// the document does.
package decodetree
