// Package encoder writes Go values as YAML.
//
// [Marshal] returns one value as a YAML document:
//
//	data, err := encoder.Marshal(ctx, cfg, encoder.Pretty())
//	if err != nil {
//		return err
//	}
//
// An [Encoder] writes a document for each value to an [io.Writer], with a
// "---" separator before every document after the first. Create one with
// [New] and call [Encoder.Encode] for each value:
//
//	enc := encoder.New(os.Stdout, encoder.Pretty())
//	if err := enc.Encode(ctx, cfg); err != nil {
//		return err
//	}
//
// Each call writes one whole document to the writer, so an [Encoder] does
// not hold output to flush and does not have a Close method.
//
// A [go.jacobcolvin.com/niceyaml.Node] encodes as the value that it
// decodes to, so [Marshal] prints the nodes that a path selects:
//
//	nodes, err := doc.Nodes(paths.MustParse("$.items[*]"))
//	if err != nil {
//		return err
//	}
//
//	data, err := encoder.Marshal(ctx, nodes)
//
// [Marshal] and [New] take the same options. Each encoder setting that
// this package supports has an option of its own, such as [WithIndent] or
// [WithFlow]. The encoder rewrites parts of the go-yaml output so that the
// YAML reads back as the value it encoded, and it takes only settings
// whose effect on that output it knows. [Pretty] selects the layout that
// prettier writes. That layout indents by two spaces, indents each sequence
// below its parent key, and starts a sequence at the root of a document in
// the first column. Options apply in order, so an option after [Pretty]
// can change one of its settings, as in
// encoder.New(w, encoder.Pretty(), encoder.WithIndent(4)).
//
// [WithMarshalers] sets how the types of the program write YAML. A
// [CustomMarshaler] writes one type as another value, such as a
// [net.IPNet] as its CIDR text, and [JSONMarshalers] writes the types
// with a MarshalJSON method through that method. The edits of the edit
// package and the migration steps of the manifest package take the same
// marshalers.
//
// [WithYAMLComments] adds comments to each document by YAML path. It
// skips a path of keys and indexes that does not select a node in a
// document.
//
// Each error names its cause with a sentinel, so a caller sorts failures
// with [errors.Is] and does not name the error types of go-yaml. A value
// that does not encode matches [ErrEncode], and [ErrNoNode] narrows it to
// a value that encodes to no node. A comment that the encoder cannot
// place matches [ErrComment], and a write that the writer refused matches
// [ErrWrite]. The error of go-yaml or of the writer stays in the chain
// under the sentinel. A [*PanicError] matches no sentinel, and neither
// does the error of the context of the call after that context ended:
//
//	switch {
//	case errors.Is(err, encoder.ErrWrite):
//		// The writer refused the document.
//	case errors.Is(err, encoder.ErrComment):
//		// The encoder cannot place a comment.
//	case errors.Is(err, encoder.ErrEncode):
//		// The value does not encode.
//	}
package encoder
