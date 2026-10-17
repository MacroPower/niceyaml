// Package edit changes the text of one YAML document. It keeps every byte
// that an edit does not target: the comments, the blank lines, the key
// order, the quoting, and the line endings. An error in the new text binds
// at the line that the user wrote.
//
// # Edits
//
// [New] takes a document of a [go.jacobcolvin.com/niceyaml.Source] and
// returns a [Doc]. [Doc.Apply] runs a [Func] on each place that a path
// selects, and the Func edits the document through the [Node] that it
// gets:
//
//	doc, err := source.Document()
//	if err != nil {
//		return err
//	}
//
//	d, err := edit.New(doc)
//	if err != nil {
//		return err
//	}
//
//	err = d.Apply(ctx, paths.MustParse("$.spec.extras.*"), func(ctx context.Context, n *edit.Node) error {
//		cost := n.At(paths.Current().Child("cost"))
//		if !cost.Exists() {
//			return nil
//		}
//
//		return cost.SetKey("price")
//	})
//	if err != nil {
//		return err
//	}
//
//	text, err := edit.FileText(d)
//	if err != nil {
//		return err
//	}
//
//	fmt.Print(text)
//
// A Node renames a key, sets or creates a value, moves a value to another
// path, or removes an entry or an item. Renames, sets, and moves keep the
// comments of the entry that they change. Removals delete them with the
// entry. Each value that an edit writes takes the indent step of the
// mapping that holds it. Go strings keep the quotes of the scalars that
// they replace.
//
// An edit that replaces, removes, or moves an anchored value writes the
// value at the first alias that reads it, unless the document still
// defines the anchor before that alias. The aliases thus keep reading the
// value.
//
// After the edits, a program reads the text of the document with
// [Doc.Text], the text of the whole file with [FileText], or a document to
// decode or validate with [Doc.Node].
//
// # Transactions
//
// Each call of Apply is one transaction. The calls of its Func see the
// edits of the calls before them. When a method of Node cannot make its
// edit, the Apply fails, even when the Func ignores the error. When two
// calls edit one value, and one of them reached the value through an alias
// or a `<<` merge key, the second edit fails.
//
// After the last call, Apply checks that each mapping that merges or
// repeats a key reads the same entries as before. It also checks that each
// value that a call set reads back as set. A value that does not read back
// shows a bug in the engine, and its error matches [ErrEngine].
//
// Apply returns the first problem and stops there. A failed Apply leaves
// the Doc as it found it, so the Doc does not keep any of the edits of that
// Apply. A Node works only during the Apply that gave it. After that Apply
// returns, each method of the node that edits or decodes returns an error.
//
// # Errors
//
// Every problem that Apply finds in the document binds in the user's text,
// the text of the document that New took. So does every error bound in the
// document that Doc.Node returns, such as a schema error. An error at a
// value that an edit kept binds at the line where the user wrote it, under
// the path that the user's text spells. An error at a value that an edit
// wrote binds at the value that it replaced, or at the key of the nearest
// mapping above that the user wrote. A detail of such an error names the
// edit, as in "v2 sets $.spec.price from this value".
//
// [WithLabel] gives the edits of one Apply a name for its messages, such
// as the version of a migration step. Without a label, a message names
// "the edit". [WithReason] gives a removal a reason, and [Doc.Removals]
// lists each removal with its label and its reason.
//
// # Protected Values
//
// [WithProtected] protects values, such as the type fields of a manifest,
// from every Func. A path of Apply never selects a protected value or a
// value inside one. Each edit of a Node fails on a protected value, on a
// value inside one, and on a value that holds one.
package edit
