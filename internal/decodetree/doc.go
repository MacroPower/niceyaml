// Package decodetree binds the aliases of one document of a niceyaml
// Source for the go-yaml decoder.
//
// Go-yaml's decoder finds an anchor by its name, in the order it reads the
// anchors. It therefore might bind an alias to a different anchor than the
// document does. A [Tree] binds each alias to the anchor that the document
// binds it to, and the decoder asks the tree for that anchor. The decoder
// then reads the nodes of the document as they are. A decode of a node
// below the body reads the anchors outside the node through its aliases.
//
// [Changes] reports whether go-yaml's decoder alone, which yaml.Unmarshal
// runs, can read a document otherwise.
package decodetree
