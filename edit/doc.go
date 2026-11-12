// Package edit changes the text of one YAML document. It keeps every byte
// that an edit does not target: the comments, the blank lines, the key
// order, the quoting, and the line endings. A line ends at a "\n" or a
// "\r\n". A line that an edit adds ends with the line break of the line
// above it. When that line does not have one, the new line takes the line
// break of another line of the file, or "\n" in a file without line
// breaks. An error in the new text binds at the line that the user wrote.
//
// The engine edits a subset of YAML, and it refuses the rest with
// [ErrUnsupported], as the Supported YAML section describes.
//
// # Edits
//
// [New] takes a document of a [go.jacobcolvin.com/niceyaml.Source] and
// returns a [Doc]. [Doc.Apply] runs a [Func] on each place that a path
// selects, and the Func edits the document through the [Node] that it
// gets:
//
//	d, err := edit.New(source.Documents()[0])
//	if err != nil {
//		return err
//	}
//
//	err = d.Apply(ctx, paths.MustParse("$.spec.extras.*"), func(ctx context.Context, n *edit.Node) error {
//		return n.At(paths.Current().Child("cost")).SetKey("price")
//	})
//	if err != nil {
//		return err
//	}
//
//	text, err := edit.FileText(source, d)
//	if err != nil {
//		return err
//	}
//
//	fmt.Print(text)
//
// SetKey leaves an extra without a cost as it finds it, as Remove and Move
// leave a place that holds no value. A Func that reads a value first
// checks [Node.Exists], which tells a place without a value from a node
// that cannot read its place:
//
//	ok, err := n.At(paths.Current().Child("cost")).Exists()
//	if err != nil || !ok {
//		return err
//	}
//
// A Node renames a key, sets or creates a value, inserts or appends an
// item, moves a value to another path, or removes an entry or an item. A
// move to an index that holds an item puts the value before that item. A
// move to the path of its own entry with [First], [Before], or [After]
// puts the entry where the option places it in the same mapping.
//
// Renames, sets, and moves keep the comments of the entry that they
// change, but a move into a flow collection drops them. Removals delete
// them with the entry.
// The comment lines after an entry or an item that the user indented past
// its key or its "-" belong to it, so a new entry or item goes after them.
// A new entry goes after the last entry of its mapping, or where [First],
// [Before], or [After] places it. Each value that an edit writes takes the
// indent step of the mapping that holds it. A Set writes a mapping or a
// sequence on block lines. It writes one in flow style inside a flow
// collection, over a flow collection that holds an entry or an item, and
// with [Flow]. Inside the value, the YAML of a MarshalYAML method or a
// flow struct tag decides the style, as [Node.Set] describes. Go strings
// keep the quotes of the scalars that they replace.
//
// The value of a Set can be a Node. A Node encodes as the value that it
// decodes to, so a Func copies the value of one place to another:
//
//	return root.At(to).Set(ctx, root.At(from))
//
// The copy is the decoded value, so it leaves the comments, the quotes,
// and the style of the value behind. It also leaves every tag behind
// except !!binary and !!timestamp, as [Node.MarshalYAML] describes.
// [Node.Set] lists the values that a Set refuses.
//
// A Set replaces its value whole, so it drops the comments inside the old
// value. [Node.Update] writes a value as a Set does, but it rewrites only
// the text that must change. A Func thus decodes a section, changes a
// field, and writes the section back with its comments:
//
//	cfg, err := n.Decode[Config](ctx)
//	if err != nil {
//		return err
//	}
//
//	cfg.Port = 9090
//
//	return n.Update(ctx, cfg)
//
// Update also writes the root of a document that holds a value of the same
// kind, where a Set fails.
//
// A Set writes a value of a type by the MarshalYAML or MarshalText method
// of the type, and otherwise field by field. [WithMarshalers] gives the
// types of the program a function or their MarshalJSON methods instead.
// A Func that decodes a value through the unmarshalers of the program,
// changes it, and sets it back then writes it the way the document holds
// it:
//
//	d, err := edit.New(doc, edit.WithMarshalers(cidr, encoder.JSONMarshalers(true)))
//
// A path selects each node of the text once. A wildcard or a recursive
// descent selects a node at its first path in the order of the text. Each
// path that reaches the node through an alias or a `<<` merge key selects
// the same node, as [Doc.Apply] describes. The nodes that [Node.At] and
// [Node.Nodes] return in different calls of a Func can still reach one
// node of the text that way. [Node.Own] returns the same path for each of
// them.
//
// A path that ends at an alias that reads a reference document of
// [go.jacobcolvin.com/niceyaml.WithReferences] selects the alias. An edit
// changes the text of its document alone, so an edit there changes the
// alias and not the anchor in the reference document. Set replaces the
// alias, Remove removes its entry or item, and Move moves the alias. The
// other aliases of the anchor keep reading the reference document.
// [Node.Decode] at the alias reads the anchor through the reference
// documents, as a decode of the whole document reads it.
//
// The engine reads a reference document only as the paths and the decode of
// go.jacobcolvin.com/niceyaml read it, so a path does not enter such an
// alias. A path below the alias fails with an error that matches
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias], which binds at the
// alias, as [go.jacobcolvin.com/niceyaml.Node.At] binds it. Its path is the
// path of the value that holds the alias in the user's text, as the Errors
// section describes. A `<<` merge key of such an alias brings in the names
// of the mapping of its anchor. A path through a name whose entry such a
// merge key brings in fails the same way, because the value lies in the
// reference document. A decode compares the keys of a mapping as it decodes
// them. The entry 0x10 that such a merge key brings in thus replaces an
// entry 16 written before the merge key. A path through a name that the
// mapping writes before such a merge key fails too when the merge key
// brings in a key that a decode reads as that name. So does such a path
// when the merge key brings in a key that does not decode. A path through
// any name of a mapping fails when its merge key reads a reference document
// that the engine cannot read. One such document merges an anchor that no
// reference document defines. These are the paths that a reference
// document decides. The error of such a path
// names the merge key and says that the reference document decides which
// names the mapping reads. A path through any other name reads the entry
// that the mapping writes, or a place without a value. A `.*`, `[*]`,
// `..name`, or `..*` selector fails where a path through a name of a place
// that it selects or passes by fails. Its error binds at the alias that
// stops the selector, as [go.jacobcolvin.com/niceyaml.Node.Nodes] binds it,
// under the path of the value that holds the alias.
//
// Each edit through such a path fails. [Node.Decode] and [Node.Exists]
// there return the error of the path, and [Node.Kind], [Node.Len], and
// [Node.Scalar] answer as at a place that does not hold a value. A Func tells
// such a place from a place without a value by the error of Exists. It reads
// the value through the alias, or through the mapping that holds the merge
// key, whose Decode reads the entries that the merge key brings in. The
// References section of the go.jacobcolvin.com/niceyaml package describes
// both. The root package binds the error of such a path as no fault of the
// document. The edit package binds it as a problem of the edit, whether a
// read, the path of an Apply, or a call meets it.
// [go.jacobcolvin.com/niceyaml.IsInvalid] thus reports true for it, as for
// a Set below a scalar.
//
// After the edits, a program reads the text of the document with
// [Doc.Text], the text of the whole file with [FileText], or a document to
// decode or validate with [Doc.Node]. [Doc.Changes] returns the edits as
// replacements of ranges of the user's text, which a program hands to an
// editor or a review as text edits.
//
// A Doc holds the lines of its document as the Documents section of
// [go.jacobcolvin.com/niceyaml] gives them, the lines of
// [go.jacobcolvin.com/niceyaml.Node.Span]. The comment lines above a "---"
// header thus belong to the document below the header, with a schema
// directive among them. For a Doc that no Apply changed, [Doc.Text] is the
// text of the lines of the span. FileText writes the text of each Doc in
// place of exactly those lines. [Doc.Node] reads the text as a reparse of
// the text of FileText reads the document. So its lines, its view, and its
// preamble match those of the document there. A program can write the
// Doc.Text of each document to a file of its own, or in place of the lines
// of the span of its document.
//
// The parser reads a document before a "---" header with the comment lines
// above the header and the header after it. So do the checks of an Apply.
// Those lines belong to the next document, so no edit writes them. The
// entry above them does not own a comment line among them, even one
// indented past its key. A removal of the entry leaves such a line, and a
// new last entry goes above it. A block scalar at the end of the document
// indents its lines past such a comment line. So the scalar does not read
// the comment line as text. That holds for a scalar that an edit writes.
// It also holds for a scalar of the user's text that an edit leaves at the
// end, such as by a removal of the entries after it. The lines of such a
// scalar move, and its string stays as it was. An indentation
// indicator grows by as many spaces as the lines. The scalar still reads
// the comment line as text in two cases. The Apply then fails with
// [ErrUnsupported], as for any edit that changes the string of a scalar of
// the user's text:
//
//   - The scalar needs an indicator past 9, the largest that the parser
//     takes.
//   - The scalar is empty, as in "a: |" with no lines of text. It has no
//     lines to move, and go-yaml's parser refuses an indicator on an empty
//     scalar above a comment line indented past its key.
//
// FileText returns the text of the whole file, which go-yaml reads as the
// edits leave it. The text of the other documents stays as it is. FileText
// also keeps each byte order mark in the prefix of a document, such as one
// at the start of the file or after the comment lines of a document. A
// program that reads that text with go-yaml directly, without the Source,
// does not drop such a mark. Go-yaml reads the mark as text, such as part
// of the key after it, or refuses the file. It refuses a file that holds a
// mark in front of a comment line or a "---" header. The edits can change
// which key the mark joins.
//
// # Empty Documents
//
// [New] takes an empty document, as
// [go.jacobcolvin.com/niceyaml.Node.IsEmpty] reports it, such as a file
// that does not exist yet or a template of comments alone. Its root holds
// no value. An Apply of `$` calls its Func once there, and the Func writes
// the first value with [Node.Set]:
//
//	err = d.Apply(ctx, paths.Doc(), func(ctx context.Context, n *edit.Node) error {
//		return n.At(paths.Doc().Child("server", "port")).Set(ctx, 8080)
//	})
//
// At the root, [Node.Exists] reports false with a nil error, [Node.Kind]
// returns [go.jacobcolvin.com/niceyaml.NodeNone], and [Node.Decode] returns the
// zero value. The root encodes as null in the value of a Set elsewhere. A Set
// of `$` writes the root. A Set of a path below it writes the mappings and the
// sequences that its names and its indexes give, with the value inside them.
// Each such sequence takes the index 0 alone, so a path with another index
// fails, as in "the edit sets $[1], but the document holds no $". A Set of `$`
// with a slice writes a root sequence. Remove, SetKey, and Move at the root
// fail, as they fail at the root of any document. A `$` path of [WithProtected]
// protects the root, so an Apply of `$` then selects nothing.
//
// The first value goes on block lines at the end of the text of the
// document. Those lines go after its comment lines and before a "..."
// marker, or before the comment lines that belong to the next document
// above its "---" header. They take the line
// break of the file, or "\n" in a file without one. A text without a final
// newline ends without one again, as for any line that an edit adds, and
// an empty text takes the lines with their final newline. So the template
// "# server:\r\n#   port: 80\r\n" becomes
// "# server:\r\n#   port: 80\r\nserver:\r\n  port: 8080\r\n".
//
// Go-yaml's parser reads no value from a tag at the end of the
// text, as in "!t", so such a document is empty too. A value after it takes
// the tag, so a Set there fails, and its message names the tag. After
// the first value, the Doc edits the document as any other, and a Set of
// `$` fails because it replaces the root.
//
// # Supported YAML
//
// The engine edits a subset of YAML 1.2. It reads each plain scalar as
// go-yaml's decoder resolves it, which differs from the core schema of
// YAML 1.2 for some texts, as go-yaml reads 010 as 8 and 1e3 as a string.
// [New] refuses a document that holds any of these, with an error that
// matches [ErrUnsupported]:
//
//   - a tag or an anchor on an alias, as in "!t *x" or "!!str *x"
//   - a tag that converts a collection, as in "!!str &x {a: 1}", the tag
//     "!", and the tags !!set, !!omap, and !!pairs, also in the verbatim
//     form of a tag, as in "!<tag:yaml.org,2002:set>"
//   - an anchor name that the document defines twice
//   - an anchor name that the document defines and aliases, and that a
//     reference document of [go.jacobcolvin.com/niceyaml.WithReferences]
//     defines too
//   - an alias inside the value of the anchor that it reads, as in
//     "a: &x [*x]"
//   - an alias as a mapping key, or an anchor or a tag on a key, as in
//     "*k : v", "&k a: v", and "&q <<: *s", except the tag !!merge on a
//     `<<` key, in its shorthand or its verbatim form
//   - an anchor on an empty value or an empty item, as in "a: &x", also
//     where go-yaml's parser nests the lines after it in the value, as it
//     nests "b: 1" after "a: &x !t"
//   - both "\n" and "\r\n" line breaks in the file of the document, or a
//     "\r" alone
//
// The error of New holds a problem for each instance of these and for each
// byte order mark that New refuses. It also holds a problem for each
// misread of go-yaml that New refuses with [ErrMisread]. A document with a
// tag that converts a collection, as in "!!str &x {a: 1}", gets no
// misread problems, because YAML refuses such a text. New lists its
// misreads after the user fixes the tag. The tag "!" and the tags !!set,
// !!omap, and !!pairs leave the misread problems in place.
//
// One problem comes alone. Several come under a heading that counts them,
// as from [go.jacobcolvin.com/niceyaml.NewSummary], and
// [go.jacobcolvin.com/niceyaml.Problems] yields them in the order of the
// text. At one site, a byte order mark comes first, then the misreads,
// then the constructs in the order of their [Construct] constants. The
// problems stop at the first anchor on an empty value under which go-yaml's
// parser nests the lines after it, inclusive. For example,
// the parser nests "b: 1" after "a: &x !t" inside the value of a. The
// paths of the places after such an anchor name values of another tree.
// An anchor on an empty value that go-yaml's parser reads as empty, as in
// "a: &x" before "b: 1", does not stop the problems. [errors.As] finds the
// first [*UnsupportedError] of the error, which can come after a misread.
//
// A call fails with ErrUnsupported when it reaches one of these:
//
//   - an entry whose key a decode reads as another value than its text,
//     such as 0x10, 3.10, True, ~, or null, except a Remove of the entry
//   - a change after which a decode reads such a key as its text, or reads
//     another key of the user's text as such a key
//   - a `<<` merge key in another form than one alias of a mapping of the
//     document without a merge key, such as "<<: [*a, *b]", "<<: {a: 1}",
//     "<<: 1", "!!merge <<", or "!<tag:yaml.org,2002:merge> <<"
//   - a mapping with two `<<` merge keys, which only a Source of
//     [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys] holds
//   - a change to the keys of a mapping that hides or reveals an entry
//     that its merge key brings in
//   - a change after which a merge key reads a value other than a mapping
//   - an entry whose key the mapping repeats, which only a Source of
//     [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys] holds
//   - an entry with an explicit key, as in "? key", except a Remove of the
//     entry
//   - a comment inside a flow collection, or a pair without braces in a
//     flow sequence, as in "[a: 1]"
//   - a Move of a block value of the user's text into a flow collection
//   - an anchor that an alias outside the value of the call reads, which a
//     Remove takes away, or which a Set takes away from inside the value
//     that it replaces
//   - a Move after which an alias comes before the anchor that it reads
//   - a Move after which the user's bytes hold a construct of the first
//     list, such as an alias inside the value of its anchor
//   - a block scalar of the user's text whose string the call changes
//   - an anchor, an alias, a merge key, or an explicit key in the YAML of a
//     value that a Set writes
//   - a construct of the first list, or a key that reads as another value
//     than its text, in the YAML of such a value
//   - a comment inside a flow collection, or a pair without braces in a
//     flow sequence, in the YAML of such a value
//
// A decode reads the key 0x10 as 16, 3.10 as 3.1, True as true, and ~ as
// the null value, so each is a key of the first item. A key whose decode
// prints as its text is not one, such as 200, true, on, or 0.5. The null
// value is no string, so the key null is one, though it prints as its
// text.
//
// [New] takes a document with such a key. Each read passes, and so does
// each call that does not reach the entry of the key. A path selects the
// entry by the text of its key, as in $.python.'3.10'. The detail of the
// error says to quote the key, which makes the key a string that a call
// edits.
//
// The place of a key can decide how a decode reads it. Go-yaml reads the
// first key of a block mapping under a tag as a string, unless YAML defines
// the tag, as it defines !!map. So the key 0x10 reads as its text right
// after "a: !t", and a call edits its entry there. It reads as 16 after
// another key of that mapping. A call that changes which of the two a
// decode reads fails with ErrUnsupported, as [ConstructKeyText] names it.
// Such a call does not reach the entry of the key. A Remove or a Move of
// the one entry before the key is such a call. So is a Set with [First]
// that writes an entry before the key.
//
// A `<<` merge key whose value is one alias of a reference document is not a
// refused form, so a call that reaches it does not fail with ErrUnsupported.
// A mapping with two such merge keys repeats the key `<<`, as
// [ConstructRepeatedKey] names it. An edit through such a merge key fails as
// an edit through such an alias fails, as the Edits section describes. A
// merge key of a mapping can read a reference document, directly or as an
// item of its sequence. A call that changes the names that such a mapping
// writes then fails with an error that matches
// [go.jacobcolvin.com/niceyaml/paths.ErrReferenceAlias]. Such a call is a
// Remove, a SetKey, or a Move of an own entry. The call fails the same way
// on a mapping whose names such a mapping reads through its merge keys.
// The engine does not check which names the reference document holds for
// such a call. So the call fails whatever it holds. Other calls add an
// entry to the mapping: a Move into the mapping, and a Set of a name that
// the mapping does not write. Such a call fails only when the reference
// document decides the name at the place of the new entry.
//
// A Set of the value of an own entry that the path reaches passes, and so
// does each edit below that value. The path reaches an own entry that the
// mapping writes after every such merge key. It also reaches an own entry
// written before such a merge key, unless the merge key brings in a key
// that a decode might read as its name. The Edits section describes such
// keys. The error of a call that changes the names of the mapping names the
// merge key and says that the reference document decides which names the
// mapping reads. A Remove, a SetKey, or a Move of an own entry out of the
// mapping binds that error at the entry. The error has a detail at the
// merge key. A Set of a name that the mapping does not write looks the
// name up through the merge key. So does a Move into the mapping, also one
// from another of its entries. A path that a reference document decides
// thus fails first, at the alias, as the Edits section describes. The
// lookup compares keys by their text. It passes when the merge key brings
// in a key that a decode reads as the name, such as 0x10 for 16. It also
// passes when the merge key brings in a key that does not decode. The call
// puts the new entry last by default, after every merge key, so the new
// entry replaces such a key. A call can put the new entry before such a
// merge key with [First], with [Before], or with [After] an entry that
// comes before the merge key. Such a call fails with the error of the path
// instead, because the entry of the reference document replaces the new
// entry there.
//
// The engine checks the path of a call step by step from the root. The
// first step that meets a construct that the subset refuses, a problem of
// the path, or an alias of a reference document decides the error of the
// call. A step that looks a name up through the merge keys of its mapping
// reads the mapping of each alias of a reference document that the lookup
// meets. That alias can be the value of a merge key, an item of its
// sequence, or the value of a merge key of a mapping that a merge key
// brings in. The step stops at the alias of the merge key that brings in
// the name from a reference document. For an entry that the mapping writes
// before the merge key, the step compares the keys as a decode reads them.
// For a name that the mapping does not write, it compares the keys by
// their text. A Set or a Move of such a name then also checks where the
// new entry goes. The step also stops at the first such alias whose
// reference document the engine cannot read.
//
// A merge key in a refused form thus refuses such a call only when an
// earlier step lies in its part of the text. "<<: [*a, *ref]" is such a
// form, and so is a merge of a mapping with a merge key of its own. The
// step into a block mapping lies in the part when the merge entry is all
// that the mapping holds, also under an anchor or a tag of the mapping.
// The braces of a flow mapping lie outside the part.
//
// A construct that the subset refuses can also meet a call at the step
// where the call changes the names of a mapping that a reference document
// decides. That construct then refuses the call first, with ErrUnsupported.
// A comment inside a flow collection on the path does the same. Under
// [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys], a SetKey onto a key
// that the mapping writes fails first too, as [ConstructRepeatedKey] names
// it.
//
// An anchored value and its aliases are one shared value. A call at the
// anchored node, through an alias, or through a `<<` merge key edits the
// shared value, so every alias and every mapping that merges it reads the
// change. A Set at the anchored node keeps the anchor and writes the new
// value after it, as in "v: &v 2". It writes null for a nil value, as in
// "&v null", and it writes the anchor before a tag of the new value, as in
// "&v !t 2". A call at an alias acts on the alias, so a Set there replaces
// the alias, and a Remove there removes it. The engine writes no anchor and
// no alias of its own, and it writes no copy of a value at an alias.
//
// [Node.Path] returns the path of the node, which can lead through an
// alias or a merge key. [Node.Own] returns the path of the place in the
// anchored value. A Func compares the two paths to tell whether the path of
// the node reads the place through an alias or a merge key. The two paths
// are equal when the path of the node leads into the anchored value through
// no alias and no merge key. Every alias and every mapping that merges the
// value still reads an edit there, and Own does not list those readers.
//
// A Move through an alias takes the value away from every reader unless its
// target lies in the anchored value too. A path below the parent of Own
// names such a target. A reference document can decide a name of that path,
// and the Move then fails, as Own describes.
//
// No path selects a `<<` merge key, so no call edits the entry of a merge
// key. A mapping with a merge key hides each entry of the merged mapping
// whose key the mapping writes itself. A call that changes the keys of the
// mapping fails with [ConstructMergeKeys] when it makes the mapping hide
// such an entry, or read one that it hid. A call that changes the keys of
// the merged mapping passes, even when the mapping then hides an entry that
// the call wrote there, or reads one that it hid. But go-yaml misreads a
// key that the mapping writes before its merge key when the merged mapping
// holds it too. So a call that writes such a key in the merged mapping
// fails with [ErrEngine]. The rule compares the
// texts of the keys. A SetKey of an entry of the mapping to a key that the
// merged mapping holds hides an entry. A SetKey or a Remove of an entry
// whose key the merged mapping holds too reveals one. So does a Move of
// such an entry out of the mapping. A Move into the mapping of an entry
// whose key the merged mapping holds hides one.
//
// A call that takes an anchor away from its aliases fails at the first
// alias that reads it, and the error lists every alias and every merge key
// that reads it. A program that removes an anchored value and its aliases
// removes the aliases first, in an earlier Apply or by earlier calls of the
// same Func. A wildcard meets an anchor before its aliases, because an
// anchor comes before them in the text. So a wildcard Remove over an anchor
// and its aliases fails at the anchor. A wildcard Set over them passes,
// because the Set at the anchor keeps it, and each Set at an alias replaces
// the alias. To remove an anchored value that its aliases still read, set
// each alias to the value that it reads first. For a merge key that reads
// the value, set the mapping of the merge key to the value that it reads,
// which writes the merged entries in place of the merge key. No path sets
// the root, so for a merge key of the root the user writes the merged
// entries in its place. A decode into any reads each key of a mapping as a
// string, so a Set of that decode writes a key such as 200 or true as a
// string. A decode into a type with such keys, such as map[int]string,
// keeps them.
//
// A merge key in a refused form, a repeated key, and an explicit key each
// hold a part of the text. So do a key that reads as another value than
// its text, a comment inside a flow collection, and a pair without braces.
// A call reaches such a construct when its place lies in that
// part. So does a call whose path steps through the part, and a call whose
// place the text lacks, where the deepest value on its path lies in the
// part. A SetKey reaches only the key
// and the values above it. A call that takes the whole part does not reach
// it, such as a Remove or a Set of a value that holds it. A Move of such a
// value reaches the part, because the Move writes the value again at its
// target. The part of a merge key in a refused form holds the key sets of
// its mapping and of each mapping that it brings in. A call reaches such a
// part when it creates, removes, renames, or moves an entry there, or when
// its path looks a key up through the merge key. A Set of the value of an
// existing key there passes. A read never reaches a refused construct, so a
// read of such a part passes.
//
// The part of a key that reads as another value than its text is its
// entry. A decode into a map names each entry by the value of its key. It
// reads the keys 3.10 and 3.1 of one mapping under one name, and it keeps
// one of the entries. The part holds each entry of the mapping that such a
// decode reads under the name of the key. The mapping itself lies outside
// the part, so a Set of another key of the mapping passes.
//
// The part also holds that name and the text of each of its keys in the
// keys of the mapping. A call reaches the part when it creates an entry of
// such a name in the mapping, as a Set of $.python.'3.1' beside the key
// 3.10 does. So does a Move of an entry to the name that a decode gives
// the key, and a SetKey of an entry to the text of the key. A SetKey to
// the name that a decode gives the key reaches the part only under
// [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys]. Without the
// option, it fails as a SetKey to the text of another key of the mapping
// does, with an error that does not match ErrUnsupported. A decode into a
// map names the key null by its text. So a SetKey to null beside that key
// reaches the part with or without the option. A Move onto the entry of
// the key fails as a Move onto any other entry does, and its error does
// not match ErrUnsupported either.
//
// A mapping that brings the mapping in through its merge keys reads the
// entry too. So a call that creates, removes, renames, or moves an entry of
// such a name in that mapping reaches the part. So does a call whose path
// looks such a name up through a merge key of the mapping of the part. A
// Remove of the one entry of a part passes. It fails when a merge key of
// the mapping brings in an entry of such a name, because a decode then
// reads that entry in its place.
//
// A call that reaches more than one such construct fails with the one that
// the first step of its path reaches. Among the constructs of one step, the
// first in the text decides. Each step looks a key up in a mapping, through
// the aliases and the merge keys on its way. So a step reaches an entry of
// that mapping and a change of the keys of that mapping alike. A Move
// reaches each construct inside the value that it moves at the last step
// of its path. The Move checks its target after its path, and a construct
// that the path reaches comes first. The Move checks its target whole, not
// step by step, so among the constructs that the target reaches, the first
// in the text decides.
//
// The error of a refusal is an [*UnsupportedError], whose Construct names
// the construct. It binds at the first token of the construct in the user's
// text, where the user changes the text so that the call passes. A
// construct of several tokens binds at the first of them. So a mapping with
// more than one `<<` merge key binds at its first merge key, whatever each
// merge key reads. Each site binds as the Errors section describes. Its
// message names the program as [WithApp] names it, and a detail says what
// to write instead. Its Fix field holds the words of that detail. Its
// Related field holds the paths of the sites of the construct, such as the
// first anchor of a name that the document defines twice. For an anchor
// that a call takes away, it holds each alias that reads the anchor, and
// the mapping of each merge key that reads it. Each site other than the
// one where the error binds comes as a detail too, before the detail of
// the fix.
//
// [go.jacobcolvin.com/niceyaml.IsInvalid] reports true for a construct of
// the user's text, so a server that answers an invalid upload with a 4xx
// status keeps that answer. A construct in the YAML of a value that a Set
// writes comes from the program. So does a key that a SetKey repeats in its
// mapping. So does a typed key of a SetKey that a decode reads as another
// value, such as the float 1.0, or as a string at its place. The error then
// binds at the place of the call, its InValue field is true, and IsInvalid
// reports false.
//
// Every other refusal of a call follows one rule. The program is at fault
// for a refusal that its arguments decide, whatever the document holds, so
// IsInvalid reports false for it. Such a refusal matches [ErrUsage] for an
// argument that the method does not take, such as a Remove of the root. It
// matches [ErrValue] for a Go value that the engine cannot write, such as a
// value that holds itself or a marshaler that fails. It matches
// [ErrProtected] for an edit of a value of [WithProtected]. The document is
// at fault for a refusal that its current values decide, so IsInvalid
// reports true for it. Such refusals include a Set below a scalar and a
// rename to a key that exists already. A Set of a [Node] of the Doc at a
// place that holds no value is one too. Each refusal of either kind binds
// at the place of the call, as the Errors section describes. The refusal
// of a marshaler that returns the error of a context is the exception. It
// binds nowhere, as ErrValue describes.
//
// IsInvalid reports false for a problem that matches [ErrEngine] or
// [go.jacobcolvin.com/niceyaml.ErrLimit]. A call through a node whose Apply
// returned fails with an error that binds nowhere, and IsInvalid reports
// false for it. An error that matches ErrUnsupported never matches
// ErrEngine.
//
// The engine reads a document as go-yaml reads it. Its reading is the tree
// of go-yaml's parser, as the Source parses it, with the values that a
// decode of niceyaml reads from that tree. Every read returns that reading,
// and every call edits it. Where go-yaml reads valid YAML otherwise than
// YAML does, the reads return go-yaml's reading, and a program that needs
// the reading of YAML fixes the text itself. A call on such a text can fail
// with [ErrEngine] where the engine cannot write go-yaml's reading back. A
// text that go-yaml's parser refuses fails New with the error of the parse.
// So does a reference document of
// [go.jacobcolvin.com/niceyaml.WithReferences] that does not parse, because
// each decode of the document fails with that error.
//
// [New] refuses a document that holds one of these, with an error that
// matches [ErrMisread] and that [go.jacobcolvin.com/niceyaml.IsInvalid]
// reports:
//
//   - an own key written before a `<<` merge key that brings in the same
//     key, as in "{a: 2, <<: *s}", where go-yaml's decoder reads the
//     merged entry
//   - a key that two mappings of one `<<` merge key bring in, as in
//     "<<: [*a, *b]", where go-yaml's decoder reads the entry of the later
//     mapping
//
// The merge spec keeps the own entry and the entry of the earlier mapping,
// and so do the rules of the calls for merge keys. New reads the tree of
// the document alone, not the reference documents of
// [go.jacobcolvin.com/niceyaml.WithReferences]. So a key that a merge key
// brings in from a reference document counts for neither kind, and a path
// through an own key before such a merge key fails, as the Edits section
// describes. The problem of each instance binds at its site.
//
// The message of each such problem names go-yaml and what it reads, and its
// detail says what to write. An alias of a reference document can come after
// the sources that hold the key. It can be an item of the sequence of the
// merge key, or the value of a later merge key. A later mapping of the
// document that the merge key or a later merge key brings in can read it
// too, through a merge key of that mapping. The engine does not read that
// document. So the message then says that go-yaml's decoder reads a later
// source than the merge spec keeps. It names only the source that the merge
// spec keeps. So does the message of a key that a later merge key brings in
// too. Such an alias can also come before the sources that hold the key, and
// the reference document can hold the key there. The message then names no
// source that the merge spec keeps.
//
// The tokenizer of the Source drops a byte order mark in the prefix of a
// document, as YAML does. Such a mark stands in one of these places:
//
//   - at the start of the file
//   - after the blank and comment lines of the prefix
//   - in front of the "---" header of the document
//   - after the "..." line that ends the document before it
//
// The engine reads the document without the mark, so New takes such a
// document, and [FileText] keeps the mark. [Doc.Text] keeps a mark after
// the first line of the document, as after a comment line. It leaves out a
// mark at the start of that line, which stands before the text of the
// document. A call can write a key in front of the line of a mark after a
// comment line, as [First] does. Text then moves the mark in front of that
// key. So the mark stays in the prefix, and the Source reads each key
// without it.
//
// New refuses a byte order mark at the start of the line after the "---"
// of a document, which the tokenizer of the Source drops and YAML does not
// allow inside a document. The tokenizer drops one mark at the start of a
// line of the prefix and reads a second mark right after it as text of the
// document. New refuses such a second mark too. Each such mark gives a
// problem of the error of New that [go.jacobcolvin.com/niceyaml.IsInvalid]
// reports. That problem matches neither ErrMisread nor ErrUnsupported. A
// mark inside the document with a second mark right after it gives one
// problem. The tokenizer keeps a mark at the start of a later line of a
// document, and the Source and go-yaml both read it as text of the key
// after it.
//
// A call can make an own key before a merge key, or a key that two
// mappings of one merge key bring in. It can also move a byte order mark of
// the user's text to the start of the text, where the tokenizer of the
// Source drops it, as a removal of the lines before the mark does. Such a
// call fails with an error that matches ErrEngine and not ErrMisread. The
// call leaves the text as it found it, and the detail of the error says
// what to write.
//
// # Transactions
//
// Each call of Apply is one transaction. The calls of its Func see the
// edits of the calls before them. Apply fails with the error that the Func
// returns. A problem of a call that matches [ErrEngine] fails the Apply
// too, whatever the Func returns.
//
// A Func of the Doc cannot call Apply, so one transaction runs one Apply.
// To edit the places of several paths at once, apply at `$` and select
// them with [Node.Nodes], which takes paths with wildcards and recursive
// descents. [Node.Append] appends an item to a sequence, and creates the
// sequence where the document lacks it:
//
//	err = d.Apply(ctx, paths.Doc(), func(ctx context.Context, root *edit.Node) error {
//		legacy, err := root.Nodes(paths.MustParse("$.legacy[*]"))
//		if err != nil {
//			return err
//		}
//
//		for _, n := range legacy {
//			err := n.Remove()
//			if err != nil {
//				return err
//			}
//		}
//
//		return root.At(paths.MustParse("$.env")).Append(ctx, "two")
//	})
//
// The two edits pass or fail together. Each node follows the removals
// before it, so the loop removes each item.
//
// A method of Node that cannot make its edit returns its problem to the
// Func. The method leaves the Doc as it found it, whichever check fails.
// So a later read of the Func reads the text before the failed edit, and
// so do [Doc.Text] and [Doc.Removals]. A problem of the method that does
// not match [ErrEngine] fails the Apply only when the Func returns it. The
// Func can skip the edit, make another edit instead, or make the edit
// again after it changes the text.
//
// For example, a Remove of an anchored value that an alias reads fails with
// an [*UnsupportedError] whose Related field names each alias. The Func
// can set each alias to the value that it reads, and the Remove then
// passes in the same Apply.
//
// [Node.At], [Node.Nodes], [Node.Exists], [Node.Decode], [Node.Scalar],
// [Node.Kind], and [Node.Len] are reads. Nodes, Exists, and Decode return
// the problem of a read that cannot answer to the Func. Scalar, Kind, and Len
// answer as they do for a place that does not hold a value. A Func that
// must tell the two apart calls Exists first. The Decode of a place that
// does not hold a value has a problem, which matches
// [go.jacobcolvin.com/niceyaml/paths.ErrNotFound]. A read through a node of
// At at a path that selects more than one value or a key has a problem, and
// so does a Nodes of a path that selects a key. So does a read at a path
// that a reference document decides, as the Edits section describes. A
// problem of a read that does not match [ErrEngine] fails the Apply only
// when the Func returns it, as for the problem of an edit.
//
// The calls of an Apply act in order. Each call edits the text that the
// calls before it left, through any path, alias, or `<<` merge key, and the
// model of the calls follows the same order. For example, in
// "r0: &x {c: 1}" and "r1: *x", a Set of $.r0.z and then a Remove of
// $.r1.z leave the text as the Set found it.
//
// Apply also checks the whole document against a model of its calls. The
// model starts as the values of the text that the Apply found. An alias
// there holds the value of its anchor, so the anchor and its aliases share
// one value. A `<<` merge key is an entry like any other, so the model
// holds the entry and its value rather than the entries that it brings in.
// A tag on a collection belongs to the place that writes it, and an alias
// reads the tag inside its anchor. A path names an entry by the text of its
// key, as [go.jacobcolvin.com/niceyaml/paths.Resolver.KeyName] gives it. A
// call finds its place by its path. Each call changes the model as the call
// describes:
//
//   - [Node.Set] replaces the value at its place, without its tag. The
//     value comes from the node that go-yaml's encoder makes, and a Set
//     whose text go-yaml reads as a value of another kind fails, as
//     [Node.Set] describes. When the place does not hold a value, the Set
//     creates the values of its path below the nearest value that holds
//     content, where its placement option puts the entry. A null there
//     first becomes the empty collection that the next selector of the
//     path selects into, at its anchor when the path reads it through an
//     alias. In an empty document, the Set replaces the root with the value
//     inside the collections of its path.
//   - [Node.Update] changes the model as the calls of Set, Insert, and
//     Remove that it makes inside the value change it.
//   - [Node.Insert] puts the value before the item at its place, or after
//     the last item of the sequence when the place lies past it. Where the
//     text lacks the place, the Insert creates it as a Set does.
//   - [Node.SetKey] gives the entry the key that the call names. A Go
//     string writes a string key, and a bool, an integer, or a float
//     writes a key of its own kind. The name of a key is its text, as a
//     path names it. When the old key has the name of the new key and a
//     path by that name selects the entry already, the SetKey leaves the
//     text and the model as they are. A typed key also needs the old
//     key to be a plain scalar of that text, which a decode reads as a
//     value of the kind of the new key at its place.
//   - [Node.Remove] removes the value.
//   - [Node.Move] takes the value from its place to the target. A target
//     that holds an item takes the value before that item. A target at the
//     path of the entry puts the entry where its placement option puts it
//     in the same mapping.
//   - The write of a protected field creates or replaces the field, first
//     in its mapping.
//
// Go-yaml's parser compares the keys of a mapping by their text, as a path
// names them. A call that writes a key that its mapping holds already fails
// before it writes, as a rename of b to "a" beside a does. The problem binds
// at the key that the call wrote, and it does not match [ErrEngine]. The
// problem shows the path of the key in the user's text, which ends in `~`. A
// key that an edit created has no such path. It binds as the Errors section
// gives for a place without a token, without a `~`. No path names a key
// inside a `<<` merge entry, so such a key shows the path of the mapping
// that holds the outermost such entry. Other problems about the key that a
// call writes bind at that key in the same way. A Move to a key that
// exists is such a problem. Only a Source of
// [go.jacobcolvin.com/niceyaml.WithAllowDuplicateKeys] lets a mapping repeat
// a key, and a rename to a key that its mapping holds then fails with
// [ConstructRepeatedKey].
//
// After each call, the text must hold the values of the model, and each
// alias must read the value of its anchor in the model. Two mappings hold
// the same values when they hold the same keys with equal values, each
// after the same number of merge keys. Their merge keys must also hold
// equal values, in order. Two scalars hold the same value when a decode
// reads equal values from them. A difference shows a bug in the engine or a
// misread of go-yaml. The first difference fails the call that made it with
// an error that matches [ErrEngine], and the call leaves the text as it
// found it. Each later call, each [Node.Decode], each [Node.Exists], and
// each [Node.Nodes] of the Apply then returns that error. [Node.Scalar],
// [Node.Kind], and [Node.Len] answer as they do for a place that does not
// hold a value, as [Node] describes.
//
// The error of a difference binds where the user's text holds the value
// that differs, as an error at that value does. A value that the call left
// in its place binds where the text that the call found holds it. It binds
// there even where go-yaml reads it as a value of another kind after the
// call. So in "a: !t", a Set of $.b fails at the tag, though go-yaml reads
// $.a as a mapping after the Set.
//
// An entry that the text holds but the model lacks binds at its key when
// the user wrote that key. When a call wrote the key, the entry binds at
// the mapping or the item that holds it in the edited text. An entry that
// the model holds but the text lacks binds at its key in the text that the
// Apply found, or at its mapping when a call created it. An error at the
// root binds at the site of the root, as the Errors section gives it.
//
// The message of a difference says what go-yaml reads at the value. The
// message names the path of the value in the edited text and the call that
// made the difference. A message names each call by the path that
// the call edits. It shows a collection as the text writes it, with each
// `<<` merge key as an entry of its mapping. It shows no more than a few
// hundred bytes of a value, however many aliases the value holds.
//
// When the model cannot take a call, the call fails with ErrEngine. The
// error names the call and binds at its place. When the text after the
// call does not hold that place, the error binds at the place in the text
// that the call found. So does any other error of a call that matches
// ErrEngine. A call that creates a place finds no text there, and the user's
// text holds no token for a value that an edit created either. The error of
// a call at such a place binds as the Errors section gives for a place
// without a token.
//
// The string of a block scalar depends on the lines around its text. It
// holds the line break that ends its text, and with keep chomping, as in
// "|+", the blank lines after it too. Those blank lines belong to the value
// that ends with the scalar, at any depth. A call that replaces, removes,
// or moves the value takes them with it.
//
// When a call writes a value that ends with such a scalar, the value takes
// the place of the blank lines that follow it where it goes. A removal of
// the lines after such a scalar takes the blank lines after them too. A
// new entry or item goes after the blank lines of the scalar.
//
// A block scalar also reads each line after its text that indents as deep as
// its lines, such as a comment line that the user indented under a value.
// When a call replaces a value with one that ends with a block scalar before
// such a line, the lines of the scalar go one space past that line. The line
// thus stays where the user wrote it. An indentation indicator of the
// scalar, as in "|2", grows by as many spaces. The parser takes an indicator
// of 9 at most.
//
// The parser reads a line of spaces alone after a block scalar as text of
// the scalar when the line holds more spaces than the lines of the scalar
// indent by. A call that puts such a line right after a scalar that did
// not read it writes the line empty. The parser refuses a file that ends
// with an empty line after a block scalar with an indentation indicator,
// as in "|+2". It also refuses such a file whose last line holds spaces
// alone without a line break. A call that leaves such a file fails with
// [ErrEngine], and the error names go-yaml's parser.
//
// At the end of a file without a final newline, a block scalar that a call
// writes keeps the line break that ends its string, so the file gains a
// final newline. When a call adds, removes, or moves the last lines of the
// text, the text ends as the user's text ends where its block scalars let
// it. So a file without a final newline ends without one again.
//
// A block scalar of the user's text keeps its bytes. A call fails with
// [ErrUnsupported] when the parse after the call reads another string from
// such a scalar, as [ConstructBlockScalar] names it. A line that a call adds
// after a scalar that ends a file without a final newline gives its last
// line a line break, which changes its string. So does a call that leaves a
// comment of the user's text inside a block scalar, as a removal of the
// entry between them can. To make such a call, a program sets the scalar to
// the string that it needs first. The user can also end the scalar with a
// line break, which adds one to its string.
//
// A block scalar that a call wrote keeps the string of that call. After each
// later call, such a scalar whose string the parse reads in another way
// takes another header, and its lines stay. The header changes the chomping
// indicator, then the indentation indicator, then the style of the scalar,
// as few of them as keep the string. The indicators keep their order in the
// header. Such a scalar that reads a line of the user's text past its own
// lines, such as a comment line, keeps it out the same way.
//
// When no header keeps the string and the lines of the scalar, the call
// writes the string again, as a Set of that string writes it at the place
// of the scalar. The lines that the scalar read past its own lines stay
// after the new text, such as that comment line. When that text does not
// keep the string either, the call fails with [ErrEngine]. The anchor and
// the tag of the scalar stay, and an error at the new text binds where the
// call wrote the scalar. A header or a text that a call wrote in place of
// the writer's lasts only while the text needs it. After each later call,
// the scalar takes the writer's text back when that text keeps the string
// again.
//
// The check compares each value of the text once, however many aliases
// read it. An Apply on a text whose aliases expand past the alias limit,
// which [go.jacobcolvin.com/niceyaml.WithAliasLimit] sets, fails before
// its calls run, with the error that a decode of the text gives. It counts
// what the aliases expand to as the decode counts it. So a call cannot
// remove the aliases of such a text, but it can when the limit does not
// apply. The decode after the last call enforces the limit on the edited
// text. That decode runs before the message of a difference, so an edited
// text whose aliases expand past the limit fails with the error of the
// decode.
//
// With the alias limit off, the checks of an Apply grow with the square of
// the length of a chain of `<<` merge keys, in which each mapping merges
// the one before it. On a chain of 800 such mappings, 27 KiB of text, one
// Set took 0.25 s and allocated 418 MiB. With the limit on, the Apply
// refuses that text before its calls run. A program that edits untrusted
// input should keep the limit on.
//
// The text after the last call must also keep the limits of the Source of
// the user's text, which [go.jacobcolvin.com/niceyaml.WithByteLimit],
// [go.jacobcolvin.com/niceyaml.WithDocumentByteLimit], and
// [go.jacobcolvin.com/niceyaml.WithDepthLimit] set. The byte limit counts
// the bytes of the whole file, with the other documents as the user wrote
// them. [FileText] checks the byte limit again on the file that the Docs
// of several documents leave. The document byte limit counts the bytes
// of the edited document as a load of the file counts them, with the
// comments below a "..." marker in the document after the marker. The
// calls read the text without the limits, so a later call can bring the
// text back within a limit that an earlier call passed. The write of a
// protected field keeps the limits too.
//
// An Apply whose last text breaks a limit fails with the message that the
// Source gives such a text, as in "file is larger than 256 KiB". The error
// matches [go.jacobcolvin.com/niceyaml.ErrLimit]. The error of either byte
// limit binds where an error at the path $ binds, as the Errors section
// describes. The error of the depth limit binds at the token that opens the
// first level past the limit. The user's text keeps the limits, so
// [go.jacobcolvin.com/niceyaml.IsInvalid] does not report the error.
//
// Apply also decodes the whole document after the last call. The text that
// the user wrote might not decode, as with a `<<` merge key of a scalar,
// as in "<<: 1". An Apply whose edits leave such a text that way fails with
// the error of that decode. That error binds in the user's text, and it
// does not match ErrEngine.
//
// When the user's text decodes and the edited text does not, go-yaml's
// decoder refuses the edited text. The error then matches ErrEngine and
// names go-yaml's decoder. It binds at the token where the decoder refused the
// text, under the path of that place, or at the root when the decoder does
// not name a token. The decoder misses the anchors of the text that
// go-yaml's parser leaves out of its tree. The error then names that text
// and the parser, as the error of a difference names them.
//
// Apply also decodes the edited text with go-yaml's decoder alone, as
// yaml.Unmarshal does, with the reference documents and the rule for
// duplicate keys of the Source. When that decoder takes the text that the
// Apply found, it must take the edited text too. When it also reads the
// text that the Apply found as the decode of niceyaml reads it, it must
// read the edited text so too. Otherwise the Apply fails with ErrEngine.
//
// An Apply that changes the text also parses the edited text with go-yaml's
// parser twice. One parse leaves out the comments, as yaml.Unmarshal does,
// and one keeps them. The edits read a parse that leaves out each comment
// that makes the parser refuse or misread valid YAML. The parser refuses
// some texts without their comments that it takes with them, such as one
// that holds an anchor without a value before a comment at its end. It
// refuses others with their comments, such as a flow collection at the root
// before a comment line. When the parser refuses or panics on the edited
// text in either parse, the Apply fails with ErrEngine. The error names
// go-yaml's parser and gives its message. It binds where the text holds the
// token that the parser names, even where go-yaml's lexer misplaces that
// token, as in a text with CRLF line breaks. The error of a panic binds at
// the root.
//
// The edited text must hold only the characters that YAML allows in a text,
// the printable characters of YAML 1.2, which include U+0085. Go-yaml's
// encoder writes a control character of a Go string, such as U+0001,
// without an escape. A call that writes such a character fails with
// ErrEngine at the value that it wrote, and the error names the encoder and
// the character. A Go string that keeps the quotes of a quoted scalar
// writes such a character as an escape in double quotes, as [Node.Set]
// describes, so that Set passes. A character of the user's text that a
// call keeps or moves stays as the user wrote it.
//
// The comment-free parse must also read the text as the parse of the edits
// reads it. Both must hold the same keys, values, tags, anchors, and
// aliases at the same places. Go-yaml misreads some texts without their
// comments. When it misreads the edited text so, the Apply fails with
// ErrEngine at the first place that the parses read otherwise. The error
// names go-yaml's parser.
//
// Go-yaml's decoder parses a text with its comments when a program asks for
// them, as with yaml.CommentToMap. The parser misreads some comments. A
// comment line after an anchor without a value can make the anchor hold the
// lines after the comment.
//
// The decoder must read the edited text alike with and without its comments
// where it read the user's text alike. It reads a text alike when it
// refuses either parse or reads one value from both. Otherwise the Apply
// fails with ErrEngine at the first place that the two parses read
// otherwise. The error names go-yaml's parser and what the decoder reads
// there with the comments and without them. When the decoder reads the
// user's text apart already, the edits follow the parse without comments.
//
// Apply returns the first problem and stops there. A failed Apply leaves
// the Doc as it found it, so the Doc does not keep any of the edits of that
// Apply. A Node works only during the Apply that gave it. After that Apply
// returns, each method of the node that edits or decodes returns
// [ErrEnded], and so do [Node.Exists] and [Node.Nodes].
//
// # Errors
//
// Every problem that Apply finds in the document binds in the user's text,
// the text of the document that New took. So does every error bound in the
// document that Doc.Node returns, such as a schema error. The parses read a
// document before a "---" header with the comment lines and the header
// after it, so a problem there binds at its line and column in the file. A
// refusal of YAML outside the subset binds as the Supported YAML section
// describes.
//
// A problem at a token binds at the first byte of the token in the user's
// text. So does each detail with a place, such as a related site of a
// refusal. A problem that the edit package finds shows the path of the
// value that holds the token in the user's text. The path climbs entries
// and items alike. So a problem in an item binds under the path of the
// item, as in $.l[1], or $[1] at a root sequence. A byte order mark or a
// line break of the user's text is no token, so a problem there binds at
// the byte.
//
// The site of a value is where a problem at the value binds. A collection
// binds at its key or its "-". A scalar or an alias binds at its first token
// under its anchors and tags. So does a collection that is an item of a flow
// sequence, which has no key or "-", unless it is a mapping without braces.
// A value that holds a tag alone, as in "a: !!null", binds at the first byte
// of the tag, the token that the user wrote. A problem at an alias that
// stops a path, such as an alias of a reference document, binds at the
// alias. It shows the path of the value that holds the alias, not the path
// of the mapping whose lookup reads it. The names that the reference
// document holds do not change the site. A problem at a value that a call
// reaches through an alias or a `<<` merge key shows the path of that value
// in the user's text too. In "base: &m {v: 1}" with "md: *m", a Set of
// $.md.v.x binds at the 1 under $.base.v, before any edit of the Apply and
// after one.
//
// A path names a key only in the mapping that holds the key. It names the
// place even where a call by that path reads another value, as for the
// earlier of two entries of one key. No path names a `<<` merge entry. So a
// problem inside one binds under the path of the mapping that holds the
// outermost such entry.
//
// Text that an edit writes in place of text of the user stands for that
// text, so a problem there binds where a problem at that text binds. A
// value that a Set writes over a value of the user stands for the value
// that it replaces, and a renamed key stands for the old key. The values
// inside the new value are values that the Set created. A collection that
// the Set writes binds at its key or its "-", and the Set keeps that key or
// "-". The first key or "-" of the collection belongs to a value that the
// Set created. The "{}" or "[]" that a removal writes for a collection that
// it empties stands for the collection.
//
// The key or the "-" that a move writes for a value stands for the key or
// the "-" that held the value in the user's text. A problem there shows the
// path of the value in the user's text. Where the user wrote a key, a
// problem at a key that the move writes shows the path of the user's key
// instead. So after a move of $.n to $.m.k, a problem at k binds at the key
// n and shows $.n~. When a move of $.n to $.m[1] writes "- p: 1", a
// problem at the "-" binds at n too and shows $.n.
//
// A node can lose its place to an earlier call, as a node at $.m.a does
// when a call removes $.m. A call through such a node fails before any
// other check of the call, also when the text holds that place again. The
// problem matches [ErrDisplaced]. It binds where a problem at the place of
// that earlier call bound before that call ran, with the path of that
// problem. Its message names the place that the call asked for, as in "the
// edit sets $.m.a through a node that lost its place to an earlier edit". A
// new node from [Node.At] reads and edits that place again.
//
// A node also loses its place when an earlier call makes its path read
// another value, as [Node] describes. A call through such a node fails with
// a problem that matches [ErrDisplaced] too. The problem binds where a
// problem at the value of the node binds. Its message names the path of the
// node, as in "the edit cannot read this value, because an earlier edit
// made $.m.a.d read another value".
//
// An item of a flow sequence has no key or "-". So when a move puts a value
// into such an item, the item keeps the place of the key or the "-" that
// held the value. The key or the "-" that a move writes for the item binds
// at that place, and a Set over the item keeps that place for the new
// value. When the user wrote the value as an item of a flow sequence, the
// key or the "-" binds at the item. So does the "{}" or "[]" that a removal
// writes for the item.
//
// An empty value has no text, so an error at it binds past the ":" of its
// key or past its "-". A problem that the edit package finds there shows
// the path of the entry or the item. When the user wrote that ":" or "-",
// the error binds past it in the user's text, as in a document that no edit
// changed. It binds there even when a move writes the collection that holds
// the value at another place, and the text that an edit writes after the
// value does not change that place. The key or the "-" that a move writes
// for an empty value stands for the value, so an error at the value binds
// where that key or "-" binds.
//
// Other places have no token in the user's text either. They include a
// value that an edit created, a field that the text lacks, a place below a
// scalar, and an item past the end of a sequence. A problem at such a place
// binds at the site of the nearest value above it whose token the user
// wrote and the edits kept. For the root, that site is the first key or
// item of the root that the user wrote and the edits kept. An item binds
// there at the first token of its value under its anchors and tags. It
// binds at its "-" when it holds no value, or when that token belongs to a
// value that an edit created, as the first key of a collection that a Set
// wrote does. When the edits kept no key or item, the site is the first
// byte of the content of the root in the user's text. That byte is the
// first "-" of a block sequence or the first key of a block mapping, and a
// flow root binds past its "{" or "[".
//
// A problem that the edit package finds at a place without a token shows
// the path of the site where it binds. Its message or a detail names the
// place that the call asked for. So in "o: 0\na: 1", a Set of $.a.b.c fails
// at the 1 under $.a with "the edit sets $.a.b.c, but a holds a scalar".
//
// Every problem of a call at a place binds at the site that this section
// gives the place in the current text, whatever the kind of the problem.
// So a value that an edit created binds by the rule for other places
// without a token, at the place where the value lies after the edits. It
// binds there also after a move took it elsewhere. A Set checks its value
// before it writes the value, as with go-yaml's encoder and for two keys
// that a decode reads as one. Each problem of those checks binds at the
// site of the place of the Set.
//
// A problem at a token that an edit created binds where a problem at the
// value of the token binds. For a key or a ":", that value is the value of
// its entry, and for a "-" the value of its item.
//
// An error that a caller binds by a path keeps that path as the caller gave
// it. Such errors include an error that a [Func] returns, an error of a
// decode through [Node.Decode] or Doc.Node, one that Doc.Node binds, and a
// schema error that Doc.Node binds. The error binds at the site of the value
// that the path selects in the current text. Where the current text does not
// hold that value, it binds at the site of the nearest value above it that
// the current text holds, by the rules of this section.
//
// After a rename of spec to q, an error at $.q.price binds where the user
// wrote the price under spec, and it shows $.q.price. No path selects a
// `<<` merge key, so an error at $.m.<< binds at the site of m. The root
// package binds such a path at the merge key, or at the alias that the
// merge key holds. Where an alias of a reference document stops the path,
// as the Edits section describes, the error binds at that alias.
//
// The details of a problem name calls by one rule. Such a detail names a
// call by the label of its Apply, what the call does, and the paths that
// the program gave the call. It reads as "v2 sets $.spec" or "v2 moves $.a
// to $.n.x". Each method of a [Node] that edits or decodes makes a call, as
// in "v2 reads $.spec" for [Node.Decode], and so does the write of a type
// field of a manifest. A related site of a problem, such as the first
// anchor of a name that the document defines twice, names only the call
// that wrote its token.
//
// Where a call wrote the token where a problem binds, a detail names that
// call. For a place without a token, that token is the token of the
// deepest value on its path that the text holds.
//
// A Set writes the token of its value, also where the value stands for a
// value of the user. A key that a rename writes keeps the writer of the old
// key. The key that a move writes keeps the writer of the key or the "-"
// that held the value. The key or the "-" that a move writes for a key or a
// "-" of the user belongs to the user. Any other "-" that a move writes
// takes the writer of the value. An item of a flow sequence has no key or
// "-". So the key or the "-" that a move writes for the item keeps the
// writer of the token of the item.
//
// While a call runs, a detail names that call too, unless the detail of
// the call that wrote the token names it already. So each problem of a
// call names the call once in its details, whatever its message says.
// These details come after the other details of the problem. The detail of
// the call that wrote the token comes first. So the details of an
// [*UnsupportedError] come in three groups: the related sites, then the
// fix that its Fix field holds, then the calls.
//
// A detail says "from this value" where the problem binds at the site of a
// value that its call replaced, as a Set replaces the value at its place.
// So in "b: 0", a Set of $.b to a mapping that holds U+0001 fails at the 0
// with the detail "the edit sets $.b from this value". After an Apply that
// v1 labels sets $.b to 5, that Set fails in an Apply that v2 labels. Its
// details are then "v1 sets $.b from this value" and "v2 sets $.b from this
// value", in that order. A construct that a call reaches binds at its own
// token, as a `<<` merge key or the "?" of an explicit key does, so its
// problem does not say "from this value", whatever path it shows. A Set
// over a value that an edit created writes a value that an edit created
// too, so it replaces no value for this rule.
//
// The checks after the last call test the edited text as a whole, as the
// Transactions section describes. They test its limits, its decodes, its
// model, and its parses with and without its comments, and their problems
// name no call. So when go-yaml's parser refuses the edited text in one of
// those parses, the problem names no call. A difference from the model
// that these checks find names the call after which the text first
// differed, in its message. [Doc.Removals] names each removed value as the
// user's text holds it.
//
// [WithLabel] gives the edits of one Apply a name for its messages, such
// as the version of a migration step. Without a label, a message names
// "the edit". [WithReason] gives a removal a reason, and [Doc.Removals]
// lists each removal with its label and its reason.
//
// # Protected Values
//
// [WithProtected] protects values, such as the type fields of a manifest,
// from every Func. The edits are what the protection refuses, not the
// selection. A path of Apply that selects more than one value, such as
// `.*` or `..name`, passes over each protected value and each value inside
// one. A sweep of the document thus leaves the type fields out. A path of
// names and indexes alone selects a protected place, or a place inside
// one, when the place holds a value. The Func then reads the value, and
// each edit there fails, as an edit through [Node.At] fails. So a mistake
// in such a path shows as an error rather than as an Apply that calls its
// Func zero times.
//
// A protected value is a node of the text, not a path. Each Apply protects
// the node that each path reaches when the Apply starts, through any alias
// or `<<` merge key, so two paths that reach one node protect it alike. The
// node keeps its protection where only a path through an alias names it,
// such as an entry of a `<<` merge source that the mapping of the merge key
// hides. A call fails when it changes the text of a protected value,
// removes it, renames its key, or writes inside it, through any path. A
// set, a move, or a removal of a value that holds a protected value fails
// too, because it takes the protected value out of its place. A rename of
// such a value passes, and the protected value keeps its text under the new
// path. The error of such a call matches [ErrProtected], and
// [go.jacobcolvin.com/niceyaml.IsInvalid] reports false for it, because
// the program declares the protected values and writes the calls.
//
// A call that sets, renames, or removes an alias or a merge key that only
// reads a protected value leaves the value in the text, so it passes. Each
// problem binds at the site of the value of the entry or the item that the
// call edits. An entry or
// an item that the call creates binds as an error at a created value binds.
// The protection follows the node, so a removal of an earlier item does not
// move it to the next item.
//
// A path that holds no value when the Apply starts protects the path as
// the Apply found it, and a removal of an earlier item does not move it. A
// call fails only when it creates a value there, through any path, with an
// error that says that the call creates the protected value. No edit
// reaches a value of a reference document, so WithProtected does not guard
// one.
//
// A path that a reference document decides, as the Edits section
// describes, reaches no node of the text. The Apply then protects the value
// whose lookup reads the alias of that document: the alias, or the mapping
// that holds the merge key. In "h: &h {<<: *ref}" with "m: *h", a path
// through $.m protects $.h, and the alias $.m only reads it. That value
// holds the protected value. A call that sets, moves, or removes it, or a
// value that holds it, fails, also after a rename of its key. Each other
// call inside it fails or passes as the Edits and the Supported YAML
// sections describe.
//
// A path that a reference document decides reads its value from that
// document when the merge key brings in an entry of its name. That entry
// replaces each earlier entry of the name. One such entry is an own entry
// that the mapping writes before the merge key. Another is an entry of a
// mapping of the document that the merge key brings in before the
// reference document, as "*a" in "<<: [*a, *ref]" does. The decoder reads
// neither, so the Apply protects only the value that holds the protected
// value.
//
// A reference document decides a path in two other cases. In one, the
// merge key brings in a key that does not decode. In the other, the merge
// key reads a reference document that the engine cannot read. The engine
// then cannot tell which entry the decoder reads. The Apply thus protects
// no other value that the path might read, such as an own entry before the
// merge key or "*a". A call through another path can change either, such
// as a Set of $.a.name or of an alias of the entry.
package edit
