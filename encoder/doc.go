// Package encoder writes Go values as YAML.
//
// An [Encoder] wraps the go-yaml encoder so the settings it supports are
// named options of this package. Create one with [New] and call
// [Encoder.Encode] for each value:
//
//	enc := encoder.New(os.Stdout, encoder.Pretty())
//	if err := enc.Encode(ctx, cfg); err != nil {
//		return err
//	}
//
// Each call writes one whole document to the writer, so an [Encoder] holds
// no output to flush and has no Close method.
//
// [Pretty] is the option for the layout prettier writes, with two-space
// indentation and each sequence indented below its parent key, and a
// sequence at the root of a document in the first column. Options apply in
// order, so an option after [Pretty] can change one of its settings, as in
// encoder.New(w, encoder.Pretty(), encoder.WithIndent(4)).
// [WithYAMLOptions] passes go-yaml options through for settings that have
// no option of their own. [WithYAMLComments] adds comments to each
// document by YAML path.
package encoder
