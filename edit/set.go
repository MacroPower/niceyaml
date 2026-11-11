package edit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/paths"
)

// run is text with the origin of each of its bytes.
type run struct {
	text    string
	origins []int
}

// newRun returns text whose bytes all have origin.
func newRun(text string, origin int) run {
	return run{text: text, origins: written(text, origin)}
}

// keptRun returns the bytes [start, end) of the current text with their
// origins.
func (d *Doc) keptRun(start, end int) run {
	return run{text: d.cur[start:end], origins: append([]int(nil), d.origin[start:end]...)}
}

// line is one line of a payload, with its indentation relative to the key
// or the "-" of the place it goes to.
type line struct {
	text   run
	indent int
}

// payload is a value with its comments, as an edit writes it at a place.
type payload struct {
	// The value as the model reads it, from the encode that gives the
	// text, or nil for a value that the edit takes from the text, and the
	// tag of a collection there, as [graph.placeTag] reads it.
	value *value
	tag   string
	// The text of the value in flow style, for a flow collection.
	flow run
	// The value on the line of its key or its "-", or nil for a block
	// collection.
	inline *run
	// The comment at the end of that line, or nil.
	comment *run
	// The origin of the key or the "-" that holds the value, or nil. Without
	// it, a "-" takes the origin of the value, and a key that an edit
	// creates takes the note of that edit. A move gives the key or the "-"
	// an origin that stands for the key or the "-" that held the value.
	holder *int
	// The note of the edit that wrote the key or the "-" that held the
	// value, or nil. A move then writes a key of its own that names that
	// edit as its writer, as the key that a rename writes keeps the writer
	// of the old key. A "-" takes the origin of the value.
	writer *note
	// The text of the key that held the value, or "". A move that puts an
	// entry back into its own mapping writes this text, so the key reads
	// back as it did. Any other key that an edit writes reads back as its
	// name.
	key string
	// The comment lines above the entry or the item.
	above []line
	// The lines of a block collection, or of a block scalar after its
	// indicator.
	lines []line
	// Whether the value is a mapping. An item that holds a mapping writes
	// its first line after the "-".
	mapping bool
	// Whether lines hold a block collection that starts below inline, its
	// anchor or its tag.
	collection bool
	// Whether the lines of the value end with a block scalar. The scalar
	// reads a line of spaces alone right after it as text when the line
	// holds more spaces than its lines indent by. So such lines where the
	// value goes lose their spaces.
	scalar bool
	// Whether that block scalar has keep chomping. Its string holds the
	// blank lines after its text, so the value takes the place of the blank
	// lines that follow it where it goes.
	keeps bool
	// That scalar as [Doc.scalarTail] reads it. Its lines go past a line
	// after them that would join its text, as [Doc.indentPast] writes them.
	tail tailScalar
}

// Set replaces the value with v, or creates it and the values above it that
// the document lacks. It replaces the value whole, so it drops the
// comments, the blank lines, the key order, and the quotes inside the old
// value, and its tag. It keeps the comments of the entry. [Node.Update]
// writes v and keeps the text inside the value where v keeps its value.
// Set encodes v once, as [go.jacobcolvin.com/niceyaml/encoder.Marshal]
// does, and writes that encode. A Set at an anchored value keeps
// the anchor and writes the new value after it, so each alias reads the new
// value, as the package documentation describes. A call of Set at the index
// past the last item of a sequence appends an item, and [Node.Insert] puts
// an item before another. An entry that Set creates goes after the last
// entry of its mapping, unless an option of opts places it, as [First],
// [Before], and [After] do. A Set that replaces a value ignores those
// options. A Set that appends an item fails with one of them, because the
// option places an entry. A value that replaces an
// empty value takes the place of the spaces and tabs after its ":" or "-",
// unless a comment follows them. A Set of 3 at a thus turns "{a: , b: 1}"
// into "{a: 3, b: 1}". A key of a flow mapping without a ":", as in
// "{a, b: 1}", holds null, and Set writes the ":" right after the key. The
// lines after the indicator of a block scalar hold its text. So the comment
// at the end of the line of the old value goes on the line of the
// indicator, as in "a: |- # note". The comment on the line of the indicator
// of an old block scalar stays there, or goes after a new value of one line.
// The blank lines after an old block scalar with a keep indicator, as in
// "|+", belong to its text, so they go with it.
//
// Set creates each value that its path lacks below the nearest value that
// holds content. A name there creates a mapping, and an index creates a
// sequence. A sequence that Set creates holds no items before the call, so
// it takes the index 0 alone, and another index fails. A null holds no
// content, where a decode of the place reads nil, as for "a:", "a: ~", and
// "a: !!null". So the next selector of the path turns a null on the path
// into a mapping or a sequence. The text then reads as after a Set of an
// empty collection at the null and a Set of v below it. The collection
// keeps the key, the anchor, the comment of the entry, and the comment
// lines below the entry. A path that reads the null through an alias
// creates the collection at the anchor, so each alias reads it. The scalar
// "!!str ~" reads "", not null, so Set fails below it, as below any other
// scalar or a collection of the other kind. A [Func] that must keep an
// explicit null reads the place with [Node.Exists] and [Node.Decode]
// first.
//
// Set writes a mapping or a sequence on block lines, below its key or after
// the "-" of its item, whether it creates the value or replaces one. When
// the old value sits on the line of its key, the comment at the end of
// that line stays there, as in "resources: # todo". When the old value
// sits on the line of the "-" of an item, such a comment keeps that line.
// The lines of the item then all go below it. When the old value starts
// below its key or its "-", the comment at the end of its line goes after
// the first line of the new value. Block lines indent by the indent step
// of the mapping that holds the value. A block sequence under a key
// indents its "-" as the first such sequence of the user's document does,
// or one step when the document has none.
//
// A Set of the root fails with [ErrUsage], because it replaces the root.
// In an empty document, which holds no value at its root, a Set of `$` or
// of a path below it writes the first value instead. The value goes inside
// the collections that the path gives, on block lines at the end of the
// text, as the Empty Documents section of the package documentation
// describes. [First] places the one entry of the root. [Before] and
// [After] fail there, because the root holds no other entry. Each option
// fails at a path that starts with an index, because the option places an
// entry. A Set of `$` places no entry, so it ignores those options.
//
// The value takes flow style on one line inside a flow collection, in
// place of a flow collection that holds an entry or an item, and with the
// [Flow] option. Inside the value, a `yaml:",flow"` struct tag or the YAML
// of a MarshalYAML method decides the style, so a MarshalYAML method that
// returns "{x: 1}" writes a flow mapping.
//
// An empty flow collection, such as "{}", "[]", or "{ }", gives no style.
// So the first entry or item that a call adds to one outside a flow
// collection opens it to block lines, whoever wrote it. At the root and in
// an item of a block sequence, an anchor or a tag on the line of the
// collection keeps it in flow style, as in "- &x {}". A collection on a
// line of its own gives that line to the block lines. So they go below its
// key, its "-", or an anchor or a tag on a line above it. A comment after
// the collection keeps its line, and the block lines go below the comment.
// So does the "---" of a document header before a collection at the root.
//
// Block lines below a key take the place of the blanks after its ":". When
// a comment follows the old value, the comment moves to the line of the
// key after one space, or after a tab that comes right after the ":". The
// blanks before the comment of an empty value stay as the user wrote
// them. A tab after the ":" of a plain "<<" key stays, because go-yaml
// reads that key as a merge key without it.
//
// A [Node] as v, or inside v, encodes as the value that it decodes to in
// the current text, as [Node.MarshalYAML] describes. A Set thus copies the
// value of another place. The copy does not hold the comments, the quotes,
// or the block and flow styles of that value. Of its tags, the copy holds
// only !!binary and !!timestamp.
//
// A value of a type writes by the MarshalYAML or MarshalText method of the
// type, or by the marshalers that [WithMarshalers] gives the Doc, and
// otherwise field by field. A type with a MarshalJSON method alone thus
// writes its exported fields, unless the marshalers turn on
// [go.jacobcolvin.com/niceyaml/encoder.JSONMarshalers].
//
// A `<<` merge key that reads a reference document of
// [go.jacobcolvin.com/niceyaml.WithReferences] brings in the names of the
// mapping of its anchor. A Set of a name whose entry such a merge key
// brings in fails with a problem that matches [paths.ErrReferenceAlias], as
// [Node.At] describes. A Set with [First], [Before], or [After] can put a
// new entry before such a merge key. It fails the same way when the merge
// key brings in a key that a decode reads as the name, such as 0x10 for 16,
// or a key that does not decode.
//
// A Go string keeps the quotes of the scalar that it replaces, in block
// and in flow style, even when it holds a line break. A single-quoted
// scalar has no escapes. So there, a string with a line break or a
// character that YAML does not allow in a text takes double quotes. Every
// other string takes the text of go-yaml's encoder, in flow style inside a
// flow collection. That text holds a control character of the string, such
// as U+0001, without an escape. Such a Set thus fails with [ErrEngine],
// because YAML does not allow that character in a text. It fails so before
// it checks the constructs of the text that it reaches. YAML allows
// U+0085, so a string writes that character without an escape.
//
// Each key that Set creates for a name of its path is a string key, so the
// name 404 writes the key "404". To write a key of another kind, a [Func]
// calls [Node.SetKey] after the Set on a Node at the path of that key.
// SetKey(404) on the same Node renames the last key of the path. A parent
// that the Set created takes another kind through a Node at its own path.
// After a Set of the path 8080.name below a Node n, the call
// n.At(paths.Current().Child("8080")).SetKey(8080) writes the plain key
// 8080.
//
// A path names a key by its text. Set fails before it writes when a
// mapping of v holds two keys of one text, such as the int 1 and the
// string "1" of a map[any]any. The problem matches [ErrValue]. The
// key has no text in the user's text. So the problem binds at the site of
// the place of the Set, as every problem of a call does by the Errors
// section of the package documentation. Set makes this check after the
// checks of the encode, and before it checks the constructs of the text
// that it reaches.
//
// The model of the Apply holds the node that the encoder makes for v, and
// the text holds what go-yaml's parse of the encode reads. Set fails with
// [ErrEngine] at its place when the parse reads a scalar of v as a value of
// another kind. For example, go-yaml's encoder writes the float 1000000 as
// 1e+06, and go-yaml's decoder reads that text as a string. Go-yaml's
// encoder writes some keys as the string that fmt prints for them, such as
// an array or a struct. Such a key fails the Set the same way, before the
// encode. The encoder writes that string for a key whose encode fails too,
// as for a key whose MarshalYAML method returns an error. The Set then
// fails before the encode with that error, which matches ErrValue, as
// the error of the marshaler of a value does. A text of the encode
// that go-yaml's parser or decoder refuses fails the Set with ErrEngine.
// The encoder quotes a string key that YAML reads as a << merge key or as
// an explicit key, so the parse reads such a key as the string. A panic
// during go-yaml's encode fails the Set with ErrEngine at its place too, as
// on a map key that is nil, or in a MarshalText method that the encoder
// calls. The
// message of each names go-yaml. Set calls each MarshalYAML method of v
// before the encode, and a panic of that call reaches the caller as it is.
//
// Before the encode, Set reads v as go-yaml's encoder reads it. It calls
// each MarshalYAML method of v that the encoder calls, and reads what the
// method returns. Those are the methods of [yaml.BytesMarshaler],
// [yaml.BytesMarshalerContext], [yaml.InterfaceMarshaler], and
// [yaml.InterfaceMarshalerContext], and each such method runs twice. Set
// also calls each function of the marshalers of the Doc that the encoder
// calls, and reads the value that it returns. Each such function thus
// runs twice too. Under JSON marshalers, Set calls each MarshalJSON method
// that the encoder calls. It reads the YAML of that method as the YAML of
// a MarshalYAML method, so the method runs twice as well. The encoder
// alone calls the MarshalText method of an [encoding.TextMarshaler], so
// that method runs once. Set also asks the encoder for the node of each key
// of a map of v alone, under the same marshalers. Each method or function
// of a key thus runs once more. A value that holds itself through a map, a
// slice, or a pointer fails the Set before the encoder runs, because the encoder
// writes such a value without end. So does a value whose marshaler returns
// a value that holds it, or a value equal to one that a marshaler on the
// way marshals. A chain of MarshalYAML methods and marshaler functions, in
// which each returns the value that the next marshals, holds at most 1,000
// marshalers. A longer chain fails the Set too, and its message names the
// type of the first. A value of a type that go-yaml's encoder does not
// encode, such as a channel or a function, fails the Set too, and so does
// a value that holds one. Each of these problems matches [ErrValue]. The
// problem of a marshaler that fails keeps the error of the marshaler, so
// [errors.Is] and [errors.As] find that error, also when it is the error
// of a context. The problem binds and declares its fault as ErrValue
// describes, so [errors.As] does not find a [*niceyaml.Error], a
// [*niceyaml.SourceError], or a [*niceyaml.PanicError] of the marshaler. A
// marshaler that returns a problem of the user's document, such as the
// error of a [Node] of the same Doc, fails the Set with the fault of that
// problem.
//
// The YAML text that a MarshalYAML method returns must be YAML of one
// node, so text of more than one document fails the Set. So does a mapping
// that repeats a key, as go-yaml's decoder reads each key, because YAML
// does not allow it. The problem names the path of the Set and the
// marshaler, and it matches ErrValue.
// Every other problem of the text is go-yaml's, and it fails the Set with
// ErrEngine at its place. Go-yaml's parser can refuse the text, with its
// comments or without them. It refuses some text that YAML allows, such as
// an anchor without a value at the end of the text. The encoder reads no
// value from text that holds a tag or a comment alone. Go-yaml also reads
// a byte order mark at the start of the text, or after its comment lines,
// as text of the value, which YAML drops. Go-yaml's encoder writes only the
// directive for text with a %YAML or a %TAG directive before its value,
// though go-yaml reads that text as the value after the directive. The
// message names go-yaml and the problem, with its place in that text. For
// a directive, the detail tells the author of the program to return the
// YAML without directives. Go-yaml converts the JSON of a MarshalJSON
// method to YAML, and that YAML must read as the value of the JSON. Some
// JSON converts to YAML that reads as another value, and such JSON fails
// the Set with ErrEngine. A string of one line break reads as "". A number
// in exponent form without a decimal point, such as 1e-07, reads as a
// string, and so does an integer outside the range of 64 bits. An object
// key "<<" reads as a merge key.
//
// The text is text of the program, and the Set writes it as go-yaml reads
// it, indented for its place. Go-yaml's encoder writes each scalar of the
// text again, as go-yaml's own Marshal writes it. Most scalars keep the
// form that the text gives them, so "!!int 017" stays as it is. The
// encoder writes a null as null, so ~ and Null become null, also after a
// tag, as in "!!str null". It writes a double-quoted string with its
// escapes resolved, so "\x41" becomes "A". A block scalar at a place in a
// flow collection becomes a double-quoted string. The place then reads
// what go-yaml reads from the text, also where YAML reads the text
// otherwise.
// The encoder quotes a plain string that YAML reads as another value, as
// [go.jacobcolvin.com/niceyaml/encoder.Encoder.Encode] describes. An indent
// that makes go-yaml read the text otherwise fails the Set with ErrEngine.
// The text can write the tag of a block collection alone on its first
// line, as in "!t\na: 1". Set writes such a value with the tag on the line
// of the key or the "-", and the collection below it. A tag on a
// collection of the text, as in "!t [1]", stays on the value.
//
// After those checks and the reach of the Set, Set fails with
// [ErrUnsupported] when the YAML of v holds a construct that the engine
// does not write. The YAML of each marshaler and the text of the encode
// must not hold an anchor or an alias, which [ConstructValueAlias] names,
// as the anchor and alias options of a yaml struct tag write them. Nor must
// they hold a << merge key, an explicit key, a comment inside a flow
// collection, a pair without braces in a flow sequence, a construct that
// [New] refuses, or a key that reads as another value than its text, such
// as the float key 1.0 of a map[any]any, which reads as 1. The error binds
// at the place of the Set, its InValue is true, and [niceyaml.IsInvalid]
// reports false for it, because the program wrote the construct. Such a
// construct can make go-yaml fail on the text
// of the encode, as an alias whose anchor the YAML lacks does, and the Set
// then fails with the construct. A scalar that go-yaml reads as a value of
// another kind fails the Set with ErrEngine before such a construct does.
func (n *Node) Set(ctx context.Context, v any, opts ...SetOption) error {
	var cfg setConfig

	for _, opt := range opts {
		opt(&cfg)
	}

	c := n.d.modelStart(n, "sets "+n.path.String(), true)

	mv, where, err := n.set(ctx, v, false, cfg, "sets")

	return n.d.modelEnd(c, err, func() bool { return c.set(mv, where) })
}

// SetOption configures one call of [Node.Set], or the target of one call
// of [Node.Move]. The placement options [First], [Before], and [After]
// place an entry of a mapping, so a call that creates an item of a
// sequence fails with one. [Flow] sets the style of the value of a Set.
//
// Options:
//   - [First]
//   - [Before]
//   - [After]
//   - [Flow]
type SetOption func(*setConfig)

// setConfig holds the settings that each [SetOption] gives a Set.
type setConfig struct {
	at placement
	// Whether the value goes in flow style, as [Flow] writes it.
	flow bool
}

// placeKind is where a call puts the entry that it creates in a mapping.
type placeKind uint8

const (
	// The entry goes after the last entry of the mapping.
	placeLast placeKind = iota
	// The entry goes before the first entry of the mapping.
	placeFirst
	// The entry goes before the entry of a key.
	placeBefore
	// The entry goes after the entry of a key.
	placeAfter
)

// placement is where a call puts the entry that it creates in a mapping,
// as a [SetOption] gives it: the kind of place, and the key of the entry
// that the new entry goes before or after.
type placement struct {
	key  string
	kind placeKind
}

// side returns the word that a message gives the place of p, "after" for
// [After] and "before" for [Before].
func (p placement) side() string {
	if p.kind == placeAfter {
		return "after"
	}

	return "before"
}

// words returns the place of p as a message gives it, as in "first" or
// `before "b"`.
func (p placement) words() string {
	switch p.kind {
	case placeFirst:
		return "first"
	case placeBefore, placeAfter:
		return fmt.Sprintf("%s %q", p.side(), p.key)
	case placeLast:
	}

	return "last"
}

// First is a [SetOption] that puts the entry that the call creates before
// the first entry of its mapping. The entry goes after the comment lines
// at the top of the document, and before the comment lines that belong to
// the first entry. A [Node.Move] to the path of its own entry puts that
// entry there, and the entry keeps the text of its key. A Set that replaces a value
// leaves the value where it is. A call that creates an item of a sequence
// fails with the option, because the option places an entry. When the
// call creates collections above the value too, it places the entry that
// it creates in the nearest mapping that the text holds, and the new
// collections go with it. A null that the call turns into a mapping, as
// [Node.Set] describes, holds no other entry. The last placement option of
// a call wins.
func First() SetOption {
	return func(c *setConfig) {
		c.at = placement{kind: placeFirst}
	}
}

// Before is a [SetOption] that puts the entry that the call creates before
// the entry of key in its mapping, as [First] places an entry before the
// first entry. A path names the key by its text, and the key selects the
// entry that a path of the key selects. A Set that replaces a value does
// not read key, and it leaves the value where it is, as [First] describes.
// A call that creates an item fails, as it does with First. When the call
// places an entry, it fails when the mapping does not write key, also when
// a `<<` merge key brings key in. The last placement option of a call
// wins.
func Before(key string) SetOption {
	return func(c *setConfig) {
		c.at = placement{kind: placeBefore, key: key}
	}
}

// After is a [SetOption] that puts the entry that the call creates after
// the entry of key in its mapping, after the comment lines that belong to
// that entry. The key names the entry as [Before] names it, and the call
// fails the same way when the mapping does not write key. The last
// placement option of a call wins.
func After(key string) SetOption {
	return func(c *setConfig) {
		c.at = placement{kind: placeAfter, key: key}
	}
}

// Flow is a [SetOption] that writes the value of a Set in flow style on
// one line, as in "tags: [vegan]". The line is that of the key or the "-"
// of the value, or that of the old value when the old value starts below
// them. A string with a line break goes in double quotes there. The value
// of a Set inside a flow collection takes flow style without the option.
// When the Set creates collections above the value, those collections take
// block style, and the value goes in flow style inside them. A [Node.Move]
// ignores the option and keeps the style of the value that it moves.
func Flow() SetOption {
	return func(c *setConfig) {
		c.flow = true
	}
}

// set is [Node.Set], but it also sets a protected value when protected is
// true. It writes v as cfg says, and puts an entry that it creates at the
// place that cfg gives. Verb names what the call does in its messages, as
// "sets" does for a Set. It returns the value of v as the model reads it,
// from the encode that gives the text of v, as the edge to its place holds
// it. It also returns the place of the new entry as the model reads it.
func (n *Node) set(
	ctx context.Context, v any, protected bool, cfg setConfig, verb string,
) (*edge, modelPlace, error) {
	d := n.d

	p, err := n.place(useCreate, verb)
	if err != nil {
		return nil, modelPlace{}, err
	}

	why := d.protection(p, true)
	named := verb + " " + n.path.String()

	switch {
	case p.root && p.exists:
		return nil, modelPlace{}, d.failAs(ErrUsage, n, "%s replaces the root of the document", d.subject())
	case !protected && why != "":
		return nil, modelPlace{}, d.failAs(ErrProtected, n, "%s %s, which %s", d.subject(), named, why)
	}

	// The Set checks its reach once go-yaml took the text of its value, as
	// [Doc.renderPayload] checks it.
	d.pending = &reachCall{kind: callSet, path: n.path, hidden: n.hidden, named: named}

	defer func() { d.pending = nil }()

	// The paths below the place go. They reach the place as the text stands
	// before the edit.
	s := d.spotOf(n.path, p)
	reach := d.reached(s)
	inside := d.readersInside(s, reach, p)

	var (
		mv    *edge
		where modelPlace
	)

	switch {
	case p.exists:
		mv, err = d.setExisting(ctx, n, p, v, cfg.flow, named)
	case p.bare:
		mv, err = d.setFirst(ctx, n, p, v, cfg, named)
	default:
		mv, where, err = d.setCreated(ctx, n, p, v, cfg, named)
	}

	if err != nil {
		return nil, modelPlace{}, err
	}

	reach.take(false, d.takenBy())

	for _, m := range inside {
		m.gone, m.goneBy = true, d.takenBy()
	}

	return mv, where, nil
}

// Insert inserts v as the item at the index of the path, before the item
// there, as [Node.Set] writes a value. The items from that index on take
// the index after theirs, and so do the paths of the nodes on them. The
// path of the node stays, so the node reads the new item. An Insert at the
// index past the last item appends the item, as a Set there does. Insert
// takes the path of an item, and it fails with [ErrUsage] on a path that
// ends in a name and on the root. Where the document lacks the sequence or
// a value above it, or holds null there, Insert creates them as Set does,
// so the index 0 creates the sequence. It fails on an index past the index
// after the last item too. It also fails where a Set there fails, such as
// inside a protected value, and with the problems of the value that Set
// describes.
// The new item goes above the comment lines that belong to the item that it
// goes before. In a flow sequence, it goes before that item with a comma of
// its own.
func (n *Node) Insert(ctx context.Context, v any) error {
	c := n.d.modelStart(n, "inserts "+n.path.String(), false)

	mv, err := n.insert(ctx, v)

	return n.d.modelEnd(c, err, func() bool { return c.insertItem(mv) })
}

// insert inserts v as the item at the index of the path, as [Node.Insert]
// describes. It returns the value of v as the model reads it, as
// [Node.set] does.
func (n *Node) insert(ctx context.Context, v any) (*edge, error) {
	d := n.d

	p, err := n.place(useCreate, "inserts")
	if err != nil {
		return nil, err
	}

	last, ok := n.path.Last()
	if !ok || last.Kind != paths.SelectorIndex {
		return nil, d.failAs(ErrUsage, n, "%s inserts %s, which is no path of an item", d.subject(), n.path)
	}

	holderPath, _ := n.path.Parent()

	holder, seq, why, err := d.insertHolder(n, p, holderPath)
	if err != nil {
		return nil, err
	}

	if why != "" {
		// A step of the path before the problem can reach a zone of the
		// Apply, which decides the problem first.
		refused := n.stepProblem()
		if refused != nil {
			return nil, refused
		}

		return nil, d.fail(n, "%s inserts %s, but %s", d.subject(), n.path, why)
	}

	if why := d.protection(holder, false); why != "" {
		return nil, d.failAs(
			ErrProtected,
			n,
			"%s inserts %s, but %s %s",
			d.subject(),
			n.path,
			d.describe(holderPath),
			why,
		)
	}

	named := "inserts " + n.path.String()

	// The text lacks the sequence that takes the item, or a value above it,
	// so the Insert creates them as a Set there does. It writes into the
	// value that holds the place. A protected path can read that value
	// through an alias, so the Insert fails where a Set there fails.
	if seq == nil {
		if why := d.protection(p, true); why != "" {
			return nil, d.failAs(ErrProtected, n, "%s inserts %s, which %s", d.subject(), n.path, why)
		}

		return d.insertCreated(ctx, n, p, v, named)
	}

	// The Insert reaches the zones of the text as a Set of the item after
	// the last does, because it takes no item away, as [Doc.renderPayload]
	// checks it.
	d.pending = &reachCall{kind: callSet, path: holderPath.Index(len(seq.Values)), hidden: n.hidden, named: named}

	defer func() { d.pending = nil }()

	d.recordEdit(holder, named, "")

	// The nodes on the items from the index on follow their items, and n
	// stays on its path, where it reads the new item. The place of the item
	// tells whether another path reaches the items, as for a Remove. The
	// place of the sequence does not tell. A path such as $.t, whose value
	// is an alias, ends before it reads through the alias. An Insert past
	// the last item moves no item.
	var items *reached

	if p.exists {
		items = d.reached(d.itemsOf(n.path, p))
	}

	origin := d.addNote(d.createdNote(holder, named))

	mv, err := d.insertItem(ctx, n, holderPath, holder, seq, last.Index, v, noFlow, origin)
	if err != nil {
		return nil, err
	}

	if items != nil {
		delete(items.nodes, n)
		items.shift(last.Index, 1)

		// The place of n holds the new item, which the model reads at the
		// edge of the new item.
		n.slot = nil
	}

	created, err := d.nodePlace(n)
	if err == nil && created.exists {
		d.recordEdit(created, named, "")
	}

	return mv, nil
}

// insertCreated inserts v at p, the place of n, which the text lacks
// along with the sequence that takes the item or a value above it, as
// [Node.Insert] describes. It creates them as [Node.Set] does. Named names
// the call, as in "inserts $.a[0]". It returns the value of v as the model
// reads it, as [Node.set] does.
func (d *Doc) insertCreated(ctx context.Context, n *Node, p *place, v any, named string) (*edge, error) {
	d.pending = &reachCall{kind: callSet, path: n.path, hidden: n.hidden, named: named}

	defer func() { d.pending = nil }()

	if p.bare {
		return d.setFirst(ctx, n, p, v, setConfig{}, named)
	}

	mv, _, err := d.setCreated(ctx, n, p, v, setConfig{}, named)

	return mv, err
}

// insertHolder returns the sequence that holds the item at holderPath,
// the place of n, as an Insert of n reads it, with its place. It also
// returns the reason that the Insert cannot put an item at p, the place
// of n, or "" when it can. The reason reads as the words that a message
// puts after "but", such as "s holds 2 items", as [Doc.createProblem]
// gives it. The sequence is nil where the text lacks it or a value above
// it, or where the value at holderPath reads null, and the Insert then
// creates them.
func (d *Doc) insertHolder(n *Node, p *place, holderPath paths.Path) (*place, *ast.SequenceNode, string, error) {
	seq, _ := p.container.(*ast.SequenceNode) //nolint:errcheck // Another container fails below.

	if p.exists {
		holder, err := d.resolveFrom(p, holderPath)
		if err != nil || !holder.exists {
			return nil, nil, "", d.bug(n, fmt.Errorf("%s holds no value", holderPath))
		}

		if seq == nil {
			return holder, nil, d.describe(holderPath) + " holds " + d.kindOf(holder, p.container), nil
		}

		return holder, seq, "", nil
	}

	// An empty document holds no sequence, so the Insert creates one, as a
	// Set through an index does there.
	if p.bare {
		return p, nil, d.firstProblem(n.path), nil
	}

	// The place lies past the last item of a sequence, or the document
	// lacks a value on the way, which the Insert creates as a Set does.
	found := prefixOf(n.path, p.found)

	holder, err := d.resolveFrom(p, found)
	if err != nil || !holder.exists {
		return nil, nil, "", d.bug(n, fmt.Errorf("%s holds no value", found))
	}

	sels := slices.Collect(n.path.Selectors())

	why := d.createProblem(holder, found, p.container, sels[p.found:])
	if why != "" || seq == nil || p.found != len(sels)-1 {
		return holder, nil, why, nil
	}

	return holder, seq, "", nil
}

// readersInside returns each node that reads a place inside the value of
// p, the place of a Set, through an alias or a `<<` merge key, as the text
// stands before the edit. The path of such a node does not start with the
// path of the place, so at, the nodes that reach the place, misses it. The
// Set replaces the value that the alias or the merge key reads, and the
// anchor of the value stays, so such a node loses its place too. A
// wildcard then skips it, as it skips each node whose place an earlier
// call took away. The call keeps each such node as it found it, which
// [Doc.undoCall] restores when the Set fails.
func (d *Doc) readersInside(s *spot, at *reached, p *place) []*Node {
	if !s.shared || !p.exists || astnode.IsNil(p.value) {
		return nil
	}

	var out []*Node

	for _, m := range d.nodes {
		if _, ok := at.nodes[m]; ok || m.gone {
			continue
		}

		mp, err := d.nodePlace(m)
		if err == nil && mp.exists && d.liesIn(placeNode(mp), p.value) {
			d.keepNode(m)

			out = append(out, m)
		}
	}

	return out
}

// setExisting replaces the value of p, the place of h, with v, in flow
// style when flow is true. Named names the call, as in "sets $.a". It
// returns the value of v as the model reads it, as [Node.set] does.
func (d *Doc) setExisting(ctx context.Context, h *Node, p *place, v any, flow bool, named string) (*edge, error) {
	d.recordEdit(p, named, "")

	return d.replaceValue(ctx, h, p, v, flow, named)
}

// setCreated creates the value of p, the place of h, as v, as cfg says.
// Named names the call, as in "sets $.a". It returns the value of v and the
// place of the entry as the model reads them, as [Node.set] does.
func (d *Doc) setCreated(
	ctx context.Context, h *Node, p *place, v any, cfg setConfig, named string,
) (*edge, modelPlace, error) {
	// A merge key can bring in the key that the edit creates in its source
	// over an entry on the path of h. The path then does not reach the new
	// value, so the check of the merges names the edit at the value above
	// it that the text holds.
	holderPath := prefixOf(h.path, p.found)

	holder, err := d.resolveFrom(p, holderPath)
	if err == nil && holder.exists {
		d.recordEdit(holder, named, "")
	}

	mv, where, err := d.createValue(ctx, h, p, v, cfg, named)
	if err != nil {
		return nil, modelPlace{}, err
	}

	created, err := d.nodePlace(h)
	if err == nil && created.exists {
		d.recordEdit(created, named, "")
	}

	return mv, where, nil
}

// setFirst writes v as the first value of an empty document, at p, the
// place of h. That place is the root, or a place below it, and v then goes
// inside the mappings and the sequences that the selectors of the path
// give. The value goes on block lines at the end of the text of the
// document, after its comment lines and before a "..." marker that ends
// it. Named names the call, as in "sets $.a". It returns the value that
// the call writes at the root as the model reads it, as [Node.set] does.
func (d *Doc) setFirst(ctx context.Context, h *Node, p *place, v any, cfg setConfig, named string) (*edge, error) {
	sels := slices.Collect(h.path.Selectors())

	switch why := d.firstProblem(h.path); {
	case why != "":
		return nil, d.fail(h, "%s %s, but %s", d.subject(), named, why)

	case len(sels) > 0 && sels[0].Kind == paths.SelectorIndex && cfg.at.kind != placeLast:
		// A placement puts an entry, so it does not apply to an item.
		return nil, d.fail(h, "%s %s %s, but %s is an item", d.subject(), named, cfg.at.words(), prefixOf(h.path, 1))

	case len(sels) > 0 && (cfg.at.kind == placeBefore || cfg.at.kind == placeAfter):
		return nil, d.fail(h, "%s %s %s %q, but the document holds no %s",
			d.subject(), named, cfg.at.side(), cfg.at.key, paths.Doc().Child(cfg.at.key))
	}

	// Go-yaml's parser reads no value from a tag that ends the text, as in
	// "--- !t". The first value would take the tag, so the Set writes none.
	if props, ok := d.bareProperties(); ok {
		return nil, d.fail(h, "%s %s, but the document holds %s without a value", d.subject(), named, props)
	}

	wrapped := wrapValue(v, sels)

	// The collections above v take block style, so v goes in flow style
	// inside them.
	fd := noFlow
	if cfg.flow {
		fd = flowDepth(len(sels))
	}

	step := d.docStep(nil)
	origin := d.addNote(d.createdNote(p, named))

	pl, err := d.renderPayload(ctx, h, wrapped, fd, step, origin, nil, d.cfg.marshalers)
	if err != nil {
		return nil, err
	}

	brk := d.brk
	text := appendRun(d.rootLines(pl, step, brk), newRun(brk, origin))

	err = d.applyEdit(h, d.firstEdit(text, brk, pl, origin)...)
	if err != nil {
		return nil, err
	}

	created, err := d.nodePlace(h)
	if err == nil && created.exists {
		d.recordEdit(created, named, "")
	}

	return &edge{val: pl.value, tag: pl.tag}, nil
}

// firstProblem returns the reason that a Set cannot write the first value
// of an empty document at path, or "" when it can. The reason reads as the
// words that a message puts after "but", as [Doc.createProblem] gives them.
// Each sequence that the Set creates holds no items before it, so an index
// other than 0 fails there, and the reason names that sequence.
func (d *Doc) firstProblem(path paths.Path) string {
	return missingProblem(paths.Doc(), slices.Collect(path.Selectors()))
}

// missingProblem returns the reason that an edit cannot create the values
// that sels select below at, a value that the edit creates too, or "" when
// it can. A sequence that the edit creates holds no items, so it takes the
// index 0 alone. The reason then names that sequence, as in "the document
// holds no $.a".
func missingProblem(at paths.Path, sels []paths.Selector) string {
	for i, sel := range sels {
		if sel.Kind != paths.SelectorChild && sel.Index != 0 {
			return "the document holds no " + appendSelectors(at, sels[:i]).String()
		}
	}

	return ""
}

// emptyFor returns the empty collection that sel selects into: an empty
// mapping for a name and an empty sequence for an index.
func emptyFor(sel paths.Selector) any {
	if sel.Kind == paths.SelectorIndex {
		return []any{}
	}

	return map[string]any{}
}

// wrapValue returns v inside the collections that sels select it in, the
// first selector outermost: a mapping of one entry for a name, and a
// sequence of one item for an index.
func wrapValue(v any, sels []paths.Selector) any {
	for _, sel := range slices.Backward(sels) {
		if sel.Kind == paths.SelectorIndex {
			v = []any{v}
		} else {
			v = map[string]any{sel.Name: v}
		}
	}

	return v
}

// bareProperties returns the text of a tag in the current text of an
// empty document, which go-yaml's parser reads no value from, as in "!t"
// or "--- !t". It reports false when the text holds comments, directives,
// and the "---" and "..." markers alone.
func (d *Doc) bareProperties() (string, bool) {
	for line := range d.curLines {
		text := strings.TrimSpace(d.lineText(line))

		if strings.HasPrefix(text, "%") {
			continue
		}

		for _, marker := range []string{headerMarker, "..."} {
			if rest, ok := strings.CutPrefix(text, marker); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
				text = strings.TrimSpace(rest)
			}
		}

		if text != "" && !strings.HasPrefix(text, "#") {
			return strings.Fields(text)[0], true
		}
	}

	return "", false
}

// rootLines returns the lines of pl as the root of a document, joined with
// brk. The lines of a block collection in pl indent by step below the key
// or the "-" that holds them, as a sequence does when
// [Doc.sequenceIndent] tells, and the root starts at the first column.
func (d *Doc) rootLines(pl payload, step int, brk string) run {
	col := 0
	if (pl.inline == nil || pl.collection) && (pl.mapping || d.sequenceIndent()) {
		col = -step
	}

	return d.joinValue(pl, col, brk)
}

// firstEdit returns the edits that write text, the lines of the first
// value of an empty document with pl that end with the line break brk. The
// lines go before the "..." marker that ends the document, or else at the
// end of its text, as [Doc.linesEdit] writes lines there. An empty text
// takes the lines with their line break.
func (d *Doc) firstEdit(text run, brk string, pl payload, origin int) []edit {
	for _, tk := range d.tokens() {
		if tk.Type != token.DocumentEndType || tk.Position == nil {
			continue
		}

		s, ok := d.tokenSpan(tk)
		if ok && s.start < len(d.cur) {
			at := d.lineStart(d.lineOf(s.start))

			return []edit{{start: at, end: at, text: text.text, origins: text.origins}}
		}
	}

	if d.cur == "" {
		return []edit{{start: 0, end: 0, text: text.text, origins: text.origins}}
	}

	line := len(d.curLines)
	if endsLine(d.cur) {
		line--
	}

	return d.linesEdit(line, text, brk, pl, origin)
}

// valueProblem returns the problem of a call of h whose value does not
// encode with err, such as the error of a marshaler. The problem keeps err
// as its cause and binds at the place of h, whatever err declares.
//
// The value of the call is at fault, so the problem matches [ErrValue] and
// declares no fault. A problem that d bound in the user's document, such as
// the error of a decode of a [Node] of d at a place that holds no value, is
// the exception. The values of the document decide it, so the problem
// keeps the fault that err declares and does not match ErrValue.
func (d *Doc) valueProblem(h *Node, err error) error {
	msg := fmt.Errorf("%s cannot write the value of %s: %s", d.subject(), h.path, err.Error())

	if !d.ownProblem(err) {
		return h.bindOwn(niceyaml.Unchecked(&sentinelError{sentinel: ErrValue, err: msg, cause: err}))
	}

	problem := &sentinelError{err: msg, cause: err}
	if niceyaml.IsInvalid(err) {
		return h.bindOwn(niceyaml.Invalid(problem))
	}

	return h.bindOwn(niceyaml.Unchecked(problem))
}

// ownProblem reports whether err holds a problem that is bound in the
// user's document of d.
func (d *Doc) ownProblem(err error) bool {
	bound, ok := errors.AsType[*niceyaml.SourceError](err)
	if !ok {
		return false
	}

	doc, ok := bound.Document()

	return ok && doc == d.user
}

// renderPayload returns v as a payload for a place whose block collections
// indent by step, encoded under the marshalers ms, in flow style from the
// depth fd on, as [render] writes it. The text that the payload writes takes
// origin. A Go string keeps the quotes of old, the token of the scalar
// that it replaces, or nil, as [scalarStyle] writes it. A mapping of v that
// holds two keys that a decode reads as one fails the call of h before it
// writes, as [Doc.ownRepeat] describes.
//
// The checks of a Set run in the order that [Node.Set] describes. Go-yaml's
// checks of the text of the value come first, then the reach of the Set,
// and the finder on the YAML of the value last. A character that YAML does
// not allow in a text, which go-yaml's encoder writes as it is, fails with
// those checks of go-yaml. The problem of such a character binds as
// [Doc.valueBug] binds it, and so does each problem that matches errEncoder.
func (d *Doc) renderPayload(
	ctx context.Context, h *Node, v any, fd flowDepth, step, origin int, old *token.Token, ms []encoder.Marshaler,
) (payload, error) {
	seqIndent := d.sequenceIndent()

	r, err := render(ctx, v, step, seqIndent, fd, ms)

	// A construct in the YAML of a marshaler of the value can make go-yaml
	// fail on the text of the encode, as an alias whose anchor the YAML
	// lacks does. The Set then fails with the construct, after its reach.
	// A construct does not change how go-yaml reads a scalar of the value
	// alone, so a scalar that go-yaml's decoder reads as a value of another
	// kind fails the Set first.
	failed := err
	if len(r.refused) > 0 && d.pending != nil && !errors.Is(err, errScalarKind) {
		err = nil
	}

	switch {
	case errors.Is(err, errEncoder):
		return payload{}, d.valueBug(h, origin, err)
	case err != nil:
		return payload{}, d.valueProblem(h, err)
	}

	pl := d.payloadOf(r, v, step, origin, seqIndent, old)

	if failed == nil {
		if what, ok := pl.unprintable(); ok {
			return payload{}, d.valueBug(h, origin, unprintableError(what))
		}

		err = d.ownRepeat(h, r.value, origin)
		if err != nil {
			return payload{}, err
		}
	}

	// A Set reaches the zones of the text after go-yaml's checks of the
	// text of its value, before it writes.
	if c := d.pending; c != nil {
		d.pending = nil

		err = d.reachProblem(*c)
		if err == nil {
			err = d.setRules(*c, r.mapping)
		}

		// The finder on the YAML of the value runs last, as the check of a
		// value that the program passed.
		if err == nil && len(r.refused) > 0 {
			err = d.valueRefusal(h, origin, &r.refused[0], c.named)
		}

		if err != nil {
			return payload{}, err
		}
	}

	return pl, nil
}

// valueBug returns err, a problem of go-yaml with the value that the call
// of h writes with origin, as the problem of a bug in the engine. The call
// fails before it writes the value, so the problem binds at the site that
// [Doc.setSite] gives.
func (d *Doc) valueBug(h *Node, origin int, err error) error {
	problem := d.bindSite(d.setSite(h, origin), func(opts ...niceyaml.ErrorOption) error {
		return d.engineProblem(err, opts...)
	})
	d.record(problem)

	return problem
}

// setSite returns the site where a problem of the Set of h binds before
// the Set writes the text of origin, its new value. That is the site of
// the place of h in the current text. A place whose value has a token of
// the user binds where the note of origin binds, as [Doc.noteSite] gives
// it, the site of the value that the Set replaces. Any other place has no
// token in the user's text, so it binds at the site that
// [Doc.tokenlessSite] gives. Its writer is the edit that wrote the value at
// the place of h, and not the Set, which has not written yet.
func (d *Doc) setSite(h *Node, origin int) site {
	p, err := d.nodePlace(h)
	if err == nil && p.exists {
		if v, ok := d.valueSite(p); ok {
			s := d.noteSite(d.notes[-1-origin], origin, false)
			s.writer = v.writer

			return s
		}
	}

	s, _ := d.tokenlessSite(h.path)

	return s
}

// payloadOf returns r, the text of v, as a payload whose block collections
// indent by step, as [Doc.renderPayload] describes.
func (d *Doc) payloadOf(r rendered, v any, step, origin int, seqIndent bool, old *token.Token) payload {
	pl := payload{
		value: r.value, tag: r.tag, flow: newRun(r.flow, origin), mapping: r.mapping,
		scalar: r.scalar, keeps: r.keeps, tail: r.tail,
	}

	shift := step
	if !r.mapping && !seqIndent {
		shift = 0
	}

	// The block lines of a collection under properties alone on its first
	// line start after that line.
	first := 0

	switch {
	case r.block == nil:
		inline := newRun(r.flow, origin)
		pl.inline = &inline

	case r.head:
		// The properties stay on the line of the key or the "-", and the
		// collection goes below them, as a collection goes below its key.
		inline := newRun(r.block[0], origin)
		pl.inline, pl.collection, first = &inline, true, 1

		for _, l := range r.block[1:] {
			cut := min(r.indent, len(l)-len(strings.TrimLeft(l, " ")))
			pl.lines = append(pl.lines, renderedLine(l[cut:], shift, origin))
		}

	case !r.collection:
		inline := newRun(r.block[0], origin)
		pl.inline = &inline

		for _, l := range r.block[1:] {
			pl.lines = append(pl.lines, renderedLine(l, 0, origin))
		}

	default:
		for _, l := range r.block {
			pl.lines = append(pl.lines, renderedLine(l, shift, origin))
		}
	}

	// Each block scalar keeps the string that the encode gives it, so the
	// bytes of its header take a note that holds the string, as
	// [Doc.keepStrings] reads it.
	for _, hd := range r.headers {
		o := d.scalarNote(origin, hd.str)

		switch {
		case !r.collection && hd.line == 0:
			setOrigins(pl.inline, hd.col, hd.size, o)
		case r.collection && hd.line >= first && hd.line-first < len(pl.lines):
			l := r.block[hd.line]
			lead := len(l) - len(strings.TrimLeft(l, " "))
			setOrigins(&pl.lines[hd.line-first].text, hd.col-lead, hd.size, o)
		}
	}

	// A Go string keeps the quotes of the scalar that it replaces, in block
	// and in flow style, so it writes as one quoted scalar.
	if s, ok := v.(string); ok {
		if text, ok := scalarStyle(s, old); ok {
			quoted := newRun(text, origin)
			pl = payload{value: pl.value, flow: quoted, inline: &quoted}
		}
	}

	return pl
}

// unprintable returns the first character of the text of pl that YAML
// does not allow in a text, as [printable] tells, when each form of the
// value that an edit can write holds such a character: the flow text and
// the block text. It names the character as [Doc.printableProblem] names
// it.
func (pl payload) unprintable() (string, bool) {
	var block strings.Builder

	if pl.inline != nil {
		block.WriteString(pl.inline.text)
	} else {
		block.WriteString(pl.flow.text)
	}

	for _, l := range pl.lines {
		block.WriteString("\n" + l.text.text)
	}

	what, ok := unprintableIn(block.String())
	if _, inFlow := unprintableIn(pl.flow.text); !ok || !inFlow {
		return "", false
	}

	return what, true
}

// scalarNote returns the origin of a copy of the note of origin that holds
// str, the string of a block scalar whose header an edit writes with
// origin. It returns origin for a byte of the user.
func (d *Doc) scalarNote(origin int, str string) int {
	if origin >= 0 {
		return origin
	}

	n := d.notes[-1-origin]
	n.str, n.hasStr = str, true

	return d.addNote(n)
}

// setOrigins gives the size bytes of r from at the origin o.
func setOrigins(r *run, at, size, o int) {
	if r == nil || at < 0 || at+size > len(r.origins) {
		return
	}

	for i := at; i < at+size; i++ {
		r.origins[i] = o
	}
}

// startingWith returns pl with origin for the token of its value in each
// form that an edit can write, as [valueToken] finds it. The forms are the
// flow text and the text on the line of the key or the "-". The first line
// of a block collection holds a key or a "-" of the value, so origin goes
// to its first byte.
func (pl payload) startingWith(origin int) payload {
	pl.flow = withOrigin(pl.flow, valueToken(pl.flow.text), origin)

	switch {
	case pl.inline != nil:
		inline := withOrigin(*pl.inline, valueToken(pl.inline.text), origin)
		pl.inline = &inline

	case len(pl.lines) > 0:
		pl.lines = slices.Clone(pl.lines)
		pl.lines[0].text = startingWith(pl.lines[0].text, origin)
	}

	return pl
}

// startingWith returns r with origin for its first byte.
func startingWith(r run, origin int) run {
	return withOrigin(r, 0, origin)
}

// withOrigin returns r with origin for its byte at off.
func withOrigin(r run, off, origin int) run {
	if off >= len(r.origins) {
		return r
	}

	origins := slices.Clone(r.origins)
	origins[off] = origin

	return run{text: r.text, origins: origins}
}

// valueToken returns the offset in text, the text of a value that an edit
// writes, of the token where an error at the value binds. That is its first
// token under its anchors and tags, as [Doc.contentStart] finds the token
// of a value of the current text. So the YAML of a marshaler that writes
// "!t x" binds at the x. An anchor or a tag starts with "&" or "!", which
// starts no other token, and a blank ends it. A value that holds a tag
// alone binds at the tag.
func valueToken(text string) int {
	at := 0

	for at < len(text) && (text[at] == '!' || text[at] == '&') {
		end := strings.IndexAny(text[at:], " \t")
		if end < 0 {
			return at
		}

		next := at + end
		for next < len(text) && (text[next] == ' ' || text[next] == '\t') {
			next++
		}

		if next == len(text) {
			return at
		}

		at = next
	}

	return at
}

// renderedLine returns text, one line of the encoder, as a line of a
// payload indented by shift past its own indentation. A block scalar reads
// the spaces of a line of spaces alone past its indentation, so such a
// line keeps its last space as its text and the spaces before it as its
// indentation, as [Doc.keptLine] keeps a line of the user's text.
func renderedLine(text string, shift, origin int) line {
	trimmed := strings.TrimLeft(text, " ")
	if trimmed == "" && text != "" {
		trimmed = " "
	}

	return line{text: newRun(trimmed, origin), indent: shift + len(text) - len(trimmed)}
}

// replaceValue replaces the value of p, the place of h, with v, in flow
// style when flow is true. Named names the call, as in "sets $.a". It
// returns the value of v as the model reads it, as [Node.set] does.
func (d *Doc) replaceValue(ctx context.Context, h *Node, p *place, v any, flow bool, named string) (*edge, error) {
	n := d.setNote(p, named)

	// The token of a new scalar or alias, and of a new item of a flow
	// sequence, stands for the old value. That token lies under the tags
	// that the YAML of a marshaler writes, as [valueToken] finds it. Every
	// other byte that the Set writes holds a value that the Set creates
	// inside the new value, or the text around one, as do those tags. A
	// collection elsewhere binds at its key or its "-", which the Set keeps,
	// so its first byte is the key or the "-" of a value that the Set
	// creates.
	inside := n
	inside.inside = true

	origin := d.addNote(inside)
	step := d.stepOf(p, h.path)

	fd := noFlow
	if flow {
		fd = 0
	}

	pl, err := d.renderPayload(ctx, h, v, fd, step, origin, scalarToken(p.value), d.cfg.marshalers)
	if err != nil {
		return nil, err
	}

	// An item of a flow sequence has no key or "-". The note of the first
	// byte of the new value thus holds the one that the old value stood
	// for. A move out of the sequence then writes a key or a "-" that
	// stands for it. Such a move also writes a key or a "-" that stands for
	// the item. A value that replaces the value of that key or "-" holds it
	// the same way. The other bytes of the new value do not hold it, so a
	// value inside the new value does not stand for the old one.
	start, ok := d.startOf(p)

	switch {
	case p.seq != nil && p.seq.IsFlowStyle:
		if held, ok := d.standIn(p); ok && held.detail == "" {
			held.moved = true
			n.held = d.addNote(held)
		}

	case ok && d.standsFor(start) && d.notes[-1-d.origin[start]].flowItem:
		n.held = d.origin[start]
	}

	// The token of a block scalar alone is its header, which keeps the
	// string of the scalar.
	if pl.inline != nil {
		if at := valueToken(pl.inline.text); at < len(pl.inline.origins) && pl.inline.origins[at] < 0 {
			if first := d.notes[-1-pl.inline.origins[at]]; first.hasStr {
				n.str, n.hasStr = first.str, true
			}
		}
	}

	if pl.value != nil && (pl.value.kind == vMapping || pl.value.kind == vSequence) &&
		(p.seq == nil || !p.seq.IsFlowStyle) {
		n.inside = true
	}

	pl = pl.startingWith(d.addNote(n))

	// The value keeps the anchor of the value that it replaces, with the
	// bytes and the origins of the user, so each alias of the anchor reads
	// the new value.
	if s, ok := d.keptAnchorSpan(p); ok {
		pl = pl.withAnchor(d.keptRun(s.start, s.end), origin)

		if d.model != nil {
			d.model.keepsAnchor = true
		}
	}

	return &edge{val: pl.value, tag: pl.tag}, d.writeValue(h, p, pl, origin, step)
}

// keptAnchorSpan returns the bytes of the anchor of the value of p, which
// a Set of p keeps, as [keptAnchorOf] finds it. It reports false for a
// value without an anchor.
func (d *Doc) keptAnchorSpan(p *place) (span, bool) {
	a, ok := keptAnchorOf(p.value)
	if !ok {
		return span{}, false
	}

	return d.propsSpan(a)
}

// withAnchor returns pl with anchor, the anchor that the value keeps, before
// the value in each form that an edit can write, as in "&v 2", "&v {k: 1}",
// and "&v |". A block collection goes below the anchor, which stays on the
// line of the key or the "-", so that the anchor does not land on the
// first key of a mapping item.
func (pl payload) withAnchor(anchor run, origin int) payload {
	head := appendRun(anchor, newRun(" ", origin))
	pl.flow = appendRun(head, pl.flow)

	switch {
	case pl.inline != nil:
		inline := appendRun(head, *pl.inline)
		pl.inline = &inline

	case len(pl.lines) > 0:
		inline := anchor
		pl.inline, pl.collection = &inline, true
	}

	return pl
}

// writeValue replaces the value of p with pl, as [Doc.valueEdits] does.
// While the Doc defers its edits, writeValue adds the edits of the value to
// them, and the next [Doc.apply] makes them.
func (d *Doc) writeValue(h *Node, p *place, pl payload, origin, step int) error {
	// A key of a flow mapping without a ":" holds null, as in "{a, b: 1}".
	// The value goes after a ":" that the edit writes first.
	if e, ok := d.colonEdit(p, origin); ok {
		err := d.apply(e)
		if err != nil {
			return d.bug(h, err)
		}

		p, err = d.nodePlace(h)
		if err != nil || !p.exists {
			return d.bug(h, fmt.Errorf("the entry of the key without a value does not hold %s", h.path))
		}
	}

	edits := d.valueEdits(p, pl, origin, step)

	if d.deferring {
		d.deferred = append(d.deferred, edits...)

		return nil
	}

	err := d.apply(edits...)
	if err != nil {
		return d.bug(h, err)
	}

	return nil
}

// colonEdit returns the edit that writes the ":" of p, an entry of a flow
// mapping whose key has none, as in "{a, b: 1}". The ":" follows the key
// and takes origin. It reports false for any other place.
func (d *Doc) colonEdit(p *place, origin int) (edit, bool) {
	if p.entry == nil || !p.flow {
		return edit{}, false
	}

	end, colon, ok := d.colonEnd(p.entry)
	if colon || !ok {
		return edit{}, false
	}

	return edit{start: end, end: end, text: ":", origins: written(":", origin)}, true
}

// separated returns where a value that an edit writes on the line of a key
// or a "-" goes, when at is the offset right past the ":" or the "-", and
// the separator that the value takes there. A tab that the user wrote
// there stays as the separator, and the value goes right after it.
// Otherwise the value takes a space. Go-yaml reads a "<<" key before ":"
// and a tab as a string key, but before ": " as a merge key, so the tab
// keeps the key that go-yaml reads. Go-yaml's lexer reads a tab after an
// anchor or a tag as part of its name, so a value after such a property
// takes a space.
func (d *Doc) separated(at int) (int, string) {
	if at < len(d.cur) && d.cur[at] == '\t' {
		return at + 1, ""
	}

	return at, " "
}

// tabKeepsKey reports whether a tab right past the ":" of entry keeps the
// key that go-yaml reads. Go-yaml reads a plain "<<" key before ":" and a
// tab as a string key. It reads one before ":" and a line break as a merge
// key, as [Doc.separated] describes.
func tabKeepsKey(entry *ast.MappingValueNode) bool {
	if entry == nil {
		return false
	}

	k, ok := astnode.Content(entry.Key).(*ast.StringNode)

	return ok && k.Value == "<<" && k.GetToken().Type == token.StringType
}

// valueEdits returns the edits that replace the value of p with pl, whose
// bytes that join its lines take origin. A block mapping of pl that
// replaces a value on the line of its key indents by step.
func (d *Doc) valueEdits(p *place, pl payload, origin, step int) []edit {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	keyStart, _ := d.startOf(p)
	keyCol := d.column(keyStart)

	oldBlock := !p.flow && p.value != nil && isBlockCollection(p.value)

	// A flow collection holds flow style, and so does a collection that
	// replaces a flow collection with an entry or an item, which the user
	// wrote in that style.
	if p.flow || filledFlow(p.value) && (pl.inline == nil || pl.collection) {
		pl.inline, pl.lines = &pl.flow, nil
	}

	keyLine := d.lineOf(keyStart)
	brk := d.breakBefore(keyLine + 1)

	var edits []edit

	switch {
	case !oldBlock && pl.inline == nil:
		// A block collection replaces a value that does not hold one, so its
		// lines go below the key or after the "-".
		edits = d.blockEdits(p, pl, origin, keyCol, brk)

	case !oldBlock || d.lineOf(d.contentStart(p)) == keyLine:
		var text run

		end := ve

		switch {
		case p.value == nil || isImplicitNull(p.value):
			end = d.blanksBefore(ve)

		case oldBlock:
			// The lines of an old block collection go with the comments
			// inside it.
			end = d.lineEnd(d.blockRegion(p).last)

		default:
			// The blank lines after an old block scalar with a keep indicator
			// belong to its text, so they go with it. The comment after the
			// indicator of an old block scalar goes after the new value, or
			// after its indicator.
			if lit, ok := astnode.Content(p.value).(*ast.LiteralNode); ok {
				end = d.blockScalarEnd(lit, ve)

				if c := d.headerComment(lit); c >= 0 && pl.inline != nil {
					comment := d.keptRun(c, d.lineEnd(d.lineOf(c)))
					inline := appendRun(appendRun(*pl.inline, newRun(" ", origin)), comment)
					pl.inline = &inline
				}
			}
		}

		// The lines after the indicator of a block scalar hold its text, so
		// the comment at the end of the line of the old value goes on the
		// line of the indicator. Spaces after the old value go, because
		// they end the last line of the block scalar otherwise.
		if !oldBlock && pl.inline != nil && len(pl.lines) > 0 {
			lineEnd := d.lineEnd(d.lineOf(end))

			switch c := d.trailingComment(end); {
			case c >= 0:
				inline := appendRun(appendRun(*pl.inline, newRun(" ", origin)), d.keptRun(c, lineEnd))
				pl.inline, end = &inline, lineEnd

			case strings.TrimRight(d.cur[end:lineEnd], " \t") == "":
				end = lineEnd
			}
		}

		if pl.inline == nil {
			// A block collection of an item starts on the line of its "-".
			col := d.column(vs) - pl.lines[0].indent
			text = pl.lines[0].text

			if len(pl.lines) > 1 {
				text = appendRun(
					text,
					prefixRun(brk, d.joinLines(pl.lines[1:], col, brk), origin),
				)
			}
		} else {
			text = d.joinValue(pl, headColumn(p, pl, keyCol, step), brk)
		}

		start := vs

		if p.value == nil || isImplicitNull(p.value) {
			var sep string

			start, sep = d.separated(vs)
			end = max(end, start)
			text = prefixRun(sep, text, origin)

			// A comment right after the tab that the value follows stays apart
			// from the value.
			if sep == "" && end < len(d.cur) && d.cur[end] == '#' {
				text = appendRun(text, newRun(" ", origin))
			}
		}

		text = d.indentPast(text, pl.tail, d.lineOf(end)+1)
		end, text = d.keptLinesEnd(pl, end, text, brk, origin)
		edits = append(edits, edit{start: start, end: end, text: text.text, origins: text.origins})

	default:
		// The old value starts below its key. Its lines go, and the new
		// value takes the line of the key or the lines below it. The anchor
		// and the tag of the old value go too.
		at := d.valueEnd(&place{entry: p.entry, seq: p.seq, index: p.index})
		lineEnd := d.lineEnd(keyLine)

		cut := lineEnd
		if c := d.trailingComment(d.afterProps(p)); c >= 0 {
			cut = c
		}

		var head, tail run

		// A tab after the ":" stays, so the key keeps the form that go-yaml
		// reads.
		at, sep := d.separated(at)
		cut = max(cut, at)

		if cut < lineEnd && (sep != "" || pl.inline != nil) {
			head = newRun(" ", origin)
		}

		if pl.inline != nil {
			head = prefixRun(sep, appendRun(*pl.inline, head), origin)

			if len(pl.lines) > 0 {
				tail = prefixRun(
					brk,
					d.joinLines(pl.lines, headColumn(p, pl, keyCol, step), brk),
					origin,
				)
			}
		} else {
			col := d.column(d.contentStart(p))
			if pl.mapping && col <= keyCol {
				col = keyCol + step
			}

			tail = prefixRun(brk, d.joinLines(pl.lines, col-pl.lines[0].indent, brk), origin)
		}

		// The indicator of a block scalar can be on the line of the key, so
		// the lines go past the next line together with it.
		end := d.lineEnd(d.blockRegion(p).last)
		lines := d.indentPast(appendRun(head, tail), pl.tail, d.lineOf(end)+1)
		head = run{text: lines.text[:len(head.text)], origins: lines.origins[:len(head.text)]}
		tail = run{text: lines.text[len(head.text):], origins: lines.origins[len(head.text):]}

		end, tail = d.keptLinesEnd(pl, end, tail, brk, origin)

		edits = append(edits,
			edit{start: at, end: cut, text: head.text, origins: head.origins},
			edit{start: lineEnd, end: end, text: tail.text, origins: tail.origins},
		)
	}

	return edits
}

// filledFlow reports whether node holds a flow mapping or a flow sequence
// with an entry or an item, under its anchors and tags. Flow style is the
// only style of an empty collection, so an empty one such as "{}" says
// nothing of the style of the text around it.
func filledFlow(node ast.Node) bool {
	switch n := astnode.Content(node).(type) {
	case *ast.MappingNode:
		return n.IsFlowStyle && len(n.Values) > 0
	case *ast.SequenceNode:
		return n.IsFlowStyle && len(n.Values) > 0
	default:
		return false
	}
}

// blockEdits returns the edits that replace the value of p with pl, a
// block collection, where the value of p does not hold a block collection.
// The bytes that join the lines of pl take origin, and its line breaks are
// brk. The key or the "-" of p starts at the column keyCol. The lines of an
// entry go below its key. The first line of an item goes after its "-",
// as a new item writes it. A comment at the end of the line of the old
// value, or of the indicator of an old block scalar, stays on the line of
// the key or the "-". The lines of an item then all go below it. A value
// that the user wrote below its key or its "-" leaves the lines above it
// as they are. Such a comment then goes after the first line of pl. When a
// block scalar at the end of pl reads a line after it, such as a comment
// line that indents deeper, [Doc.keepStrings] writes the scalar again past
// that line.
func (d *Doc) blockEdits(p *place, pl payload, origin, keyCol int, brk string) []edit {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	empty := p.value == nil || isImplicitNull(p.value)

	// The offset right past the ":" of the entry or the "-" of the item.
	from := d.valueEnd(&place{entry: p.entry, seq: p.seq, index: p.index})
	item := p.entry == nil

	end := ve

	var comment int

	switch lit, ok := astnode.Content(p.value).(*ast.LiteralNode); {
	case empty:
		comment = d.trailingComment(from)

	case ok:
		end = d.blockScalarEnd(lit, ve)
		comment = d.headerComment(lit)

	default:
		comment = d.trailingComment(end)
	}

	cut := d.lineEnd(d.lineOf(end))

	var commentRun *run

	if comment >= 0 {
		r := d.keptRun(comment, d.lineEnd(d.lineOf(comment)))
		commentRun = &r
	}

	// The lines of an entry indent from its key. Those of an item start at
	// the column after its "- ".
	col := keyCol
	if item {
		col = keyCol + 2 - pl.lines[0].indent
	}

	var (
		start int
		text  run
	)

	switch {
	case !empty && d.lineOf(vs) > d.lineOf(from):
		start = d.lineEnd(d.lineOf(vs) - 1)
		first := pl.lines[0].text

		if commentRun != nil {
			first = appendRun(appendRun(first, newRun(" ", origin)), *commentRun)
		}

		lines := slices.Clone(pl.lines)
		lines[0].text = first
		text = prefixRun(brk, d.joinLines(lines, col, brk), origin)

	case item && commentRun == nil:
		start = from
		text = prefixRun(" ", pl.lines[0].text, origin)

		if len(pl.lines) > 1 {
			text = appendRun(text, prefixRun(brk, d.joinLines(pl.lines[1:], col, brk), origin))
		}

	default:
		// A tab after the ":" of an entry stays before a comment, and so does
		// one that keeps the key that go-yaml reads. Otherwise the line of
		// the key ends at the ":".
		start = from

		sep := " "
		if !item && (commentRun != nil || tabKeepsKey(p.entry)) {
			start, sep = d.separated(from)
		}

		var head run

		switch {
		case commentRun != nil && empty:
			// The blanks before the comment of an empty value stay as the
			// user wrote them.
			head = d.keptRun(start, cut)
		case commentRun != nil:
			head = prefixRun(sep, *commentRun, origin)
		}

		text = appendRun(head, prefixRun(brk, d.joinLines(pl.lines, col, brk), origin))
	}

	cut, text = d.keptLinesEnd(pl, cut, text, brk, origin)

	return []edit{{start: start, end: cut, text: text.text, origins: text.origins}}
}

// headColumn returns the column from which the lines of pl indent. Here pl
// writes a block collection below its properties, which sit on the line of
// the key or the "-" of p, at the column keyCol. A block sequence can start
// at the column of a key whose value it is, as the user's text writes such
// sequences when they start there. YAML nests a collection below an item
// only past the column of its "-". So the lines of a block sequence of an
// item that start at that column indent by step more.
func headColumn(p *place, pl payload, keyCol, step int) int {
	if p.entry == nil && p.seq != nil && pl.collection && !pl.mapping && len(pl.lines) > 0 && pl.lines[0].indent <= 0 {
		return keyCol + step
	}

	return keyCol
}

// keptLinesEnd returns the end of the bytes that text, the value pl that
// replaces a value whose text ends at end, takes the place of, and the
// text it writes there. When pl writes the lines of a block scalar with
// keep chomping at its end, the text takes the place of the blank lines
// after the line of end too. The string of the scalar would hold them.
// Another block scalar takes the place of the blank lines up to the last
// that holds spaces, and writes them empty, as [Doc.spacedBlanks] tells.
func (d *Doc) keptLinesEnd(pl payload, end int, text run, brk string, origin int) (int, run) {
	if !pl.scalar || len(pl.lines) == 0 || strings.TrimSpace(d.cur[end:d.lineEnd(d.lineOf(end))]) != "" {
		return end, text
	}

	if line := d.lineOf(end); pl.keeps {
		end = d.blanksAfter(line)
	} else if spaced := d.spacedBlanks(line + 1); spaced > line {
		end = d.lineEnd(spaced)
		text = appendRun(text, newRun(strings.Repeat(brk, d.emptiedBreaks(line+1, spaced)), origin))
	}

	return end, text
}

// spacedBlanks returns the last of the blank lines from line on that holds
// spaces, or line-1 when none does. A block scalar that an edit writes
// right before such a line could read its spaces as text, so the edit
// writes the line empty, as [Doc.emptiedBreaks] tells.
func (d *Doc) spacedBlanks(line int) int {
	spaced := line - 1

	for l := line; l < len(d.curLines) && d.lineStart(l) < len(d.cur) && d.isBlankLine(l); l++ {
		if d.lineText(l) != "" {
			spaced = l
		}
	}

	return spaced
}

// emptiedBreaks returns the number of line breaks that the blank lines
// from first through last keep when an edit writes them empty. A last line
// of the document without a line break goes, so it keeps none.
func (d *Doc) emptiedBreaks(first, last int) int {
	n := last - first + 1
	if d.lineEnd(last) == len(d.cur) && !endsLine(d.cur) {
		n--
	}

	return n
}

// blockScalarEnd returns the end of the text of the block scalar lit,
// whose content ends at end. A keep indicator, as in "|+", makes the blank
// lines after the content part of the scalar, so its text ends at the line
// break of the last of those lines. Each line counts only with its line
// break, because that line break ends the scalar.
func (d *Doc) blockScalarEnd(lit *ast.LiteralNode, end int) int {
	if !keepsChomping(lit) {
		return end
	}

	for line := d.lineOf(end) + 1; line < len(d.curLines); line++ {
		e := d.lineEnd(line)
		if e == len(d.cur) || !d.isBlankLine(line) {
			break
		}

		end = e
	}

	return end
}

// headerComment returns the start of the comment after the indicator of
// the block scalar lit, or -1 when the line of the indicator ends without
// one.
func (d *Doc) headerComment(lit *ast.LiteralNode) int {
	if lit.Start == nil {
		return -1
	}

	s, ok := d.tokenSpan(lit.Start)
	if !ok {
		return -1
	}

	return d.trailingComment(s.end)
}

// blanksBefore returns the end of the spaces and tabs from off, past the
// ":" or the "-" of an empty value, when the end of their line, a ",", or
// the bracket that closes a flow collection follows them. A value that
// Set writes there takes their place, so they do not stay after it. It
// returns off when other text follows them, such as a comment, which
// needs a blank before it.
func (d *Doc) blanksBefore(off int) int {
	end := d.lineEnd(d.lineOf(off))

	i := off
	for i < end && (d.cur[i] == ' ' || d.cur[i] == '\t') {
		i++
	}

	if i == end || strings.ContainsRune(",]}", rune(d.cur[i])) {
		return i
	}

	return off
}

// setNote returns the note of the text that a Set writes over the value of
// p, where call says what the Set does, as in "sets $.a". The note binds at
// the site of the old value, under the path of that value in the user's
// text. A scalar or an alias binds at its first token under its anchors and
// tags. A collection binds at its key or its "-", and a block collection
// starts at its first key or "-", whose path lies below the collection, so
// the key or the "-" of p gives the path of the collection. The note does
// not hold the key, the "-", or the item that held the old value, which only
// the first byte of the new value can hold. The new value stands for the old
// one only where the user wrote the old one. A value that an edit created
// stays created when a Set replaces it.
func (d *Doc) setNote(p *place, call string) note {
	vs, ve := d.contentStart(p), d.valueEnd(p)

	n := d.keptNote(vs, max(vs+1, ve))

	// An empty value has no text, so the note binds past the ":" of its key
	// or past its "-", as an error at the value binds.
	if isImplicitNull(p.value) && vs > 0 {
		if empty, ok := d.emptyNote(p.path, vs-1); ok {
			n = empty
		}
	}

	created := !n.located || n.detail != "" && (!n.replaced || n.inside)

	// A block collection binds at its key or its "-", as [Doc.valueSite]
	// binds it, whatever wrote its entries. So the new value stands for it
	// where an edit did not create that key or "-". A collection under a key
	// or a "-" that an edit created is a value that an edit created, as a
	// mapping that a move created to hold a value is.
	if isCollection(p.value) && (p.seq == nil || !p.seq.IsFlowStyle) {
		s := d.holderSite(p)
		n.at, n.located, n.inside = s.at, true, false
		created = d.madeAt(d.holderToken(p))
	}

	n.detail, n.call, n.replaced, n.held = d.subject(), call, !created, 0

	// A mapping that the user wrote without braces in a flow sequence
	// starts at its first key, which gives the path of that key.
	if p.seq != nil && p.seq.IsFlowStyle {
		n = d.bareNote(n)
	}

	// A null that a move writes in flow style for an empty value takes the
	// origin or the note of its key. So does the bracket that opens a flow
	// collection that a move wrote. That note names the key, and the note
	// of the Set names the value instead, as [Doc.locate] does for an error
	// at the value. A block collection takes the path that its key or its
	// "-" gives.
	if n.hasPath && !isBlockCollection(p.value) {
		n.path = spelled(n.path, p.path)
	}

	if path, ok := d.blockPath(p); ok {
		n.path, n.hasPath = path, true
	}

	return n
}

// blockPath returns the path in the user's text of the block collection
// of p, an entry or an item, as the key or the "-" of p gives it. A block
// collection starts at its first key or "-", whose path lies below the
// collection. It reports false for any other value, and when the key or
// the "-" of p does not give a path.
func (d *Doc) blockPath(p *place) (paths.Path, bool) {
	if p.flow || !isBlockCollection(p.value) || p.entry == nil && p.seq == nil {
		return paths.Path{}, false
	}

	start, ok := d.startOf(p)
	if !ok {
		return paths.Path{}, false
	}

	held := d.keptNote(start, start+1)
	if !held.hasPath {
		return paths.Path{}, false
	}

	path := held.path
	if last, ok := path.Last(); ok && last.Kind == paths.SelectorKey {
		path, _ = path.Parent()
	}

	return path, true
}

// joinValue returns the text of pl as the value of a place whose value
// starts at column col, on the line of its key or its "-".
func (d *Doc) joinValue(pl payload, col int, brk string) run {
	var out run

	if pl.inline != nil {
		out = appendRun(out, *pl.inline)
	}

	if len(pl.lines) > 0 {
		lines := d.joinLines(pl.lines, col, brk)
		if pl.inline != nil {
			lines = prefixRun(brk, lines, -1)
		}

		out = appendRun(out, lines)
	}

	return out
}

// joinLines returns lines, each indented by col past its own indentation,
// joined with brk. A blank line has no byte that holds an origin. So the
// line break before a line takes the origin of the first line from there on
// that holds text. When no such line follows, the line break takes the
// origin of the last byte before it.
func (d *Doc) joinLines(lines []line, col int, brk string) run {
	var out runBuilder

	next := -1

	for i, l := range lines {
		if i > 0 {
			if next < i {
				next = i
				for next < len(lines) && lines[next].text.text == "" {
					next++
				}
			}

			origin := -1

			switch {
			case next < len(lines):
				origin = originOf(lines[next].text)
			case len(out.origins) > 0:
				origin = out.origins[len(out.origins)-1]
			}

			out.add(newRun(brk, origin))
		}

		if l.text.text != "" {
			out.add(newRun(strings.Repeat(" ", max(0, col+l.indent)), originOf(l.text)))
		}

		out.add(l.text)
	}

	return out.run()
}

// originOf returns the origin that the text around r takes: the origin of
// its first byte.
func originOf(r run) int {
	if len(r.origins) == 0 {
		return -1
	}

	return r.origins[0]
}

// runBuilder builds a run from the runs that it adds, in order. Each add
// copies the bytes and the origins of its run once, so a run of many parts
// costs time in proportion to its length.
type runBuilder struct {
	text    strings.Builder
	origins []int
}

// add adds r after the runs that b holds.
func (b *runBuilder) add(r run) {
	b.text.WriteString(r.text)

	b.origins = append(b.origins, r.origins...)
}

// run returns the runs that b holds as one run.
func (b *runBuilder) run() run {
	return run{text: b.text.String(), origins: b.origins}
}

// appendRun returns a with b after it.
func appendRun(a, b run) run {
	return run{text: a.text + b.text, origins: append(append([]int(nil), a.origins...), b.origins...)}
}

// prefixRun returns r after text, whose bytes take origin, or the origin
// of r for a negative origin.
func prefixRun(text string, r run, origin int) run {
	if origin == -1 {
		origin = originOf(r)
	}

	return appendRun(newRun(text, origin), r)
}

// createValue creates the value of p, the place of h, and the values above
// it that the document lacks, as cfg says. Named names the call, as in
// "sets $.a". The nearest value on the path that holds content takes the
// first of them, and a null there first becomes the empty collection that
// takes it, as [Doc.fillNull] writes it. It returns the value of v as the
// model reads it, as [Node.set] does.
func (d *Doc) createValue(
	ctx context.Context, h *Node, p *place, v any, cfg setConfig, named string,
) (*edge, modelPlace, error) {
	at := cfg.at
	sels := slices.Collect(h.path.Selectors())
	holderPath := appendSelectors(paths.Doc(), sels[:p.found])

	holder, err := d.resolveFrom(p, holderPath)

	switch {
	case err != nil:
		return nil, modelPlace{}, d.bug(h, fmt.Errorf("%s %w", holderPath, err))
	case !holder.exists:
		return nil, modelPlace{}, d.bug(h, fmt.Errorf("%s holds no value", holderPath))
	}

	// The walk of the path read the content of the holder as a decode reads
	// it, under its anchors, its tags, and its aliases.
	content := p.container
	next := sels[p.found]

	if why := d.createProblem(holder, holderPath, content, sels[p.found:]); why != "" {
		// A step of the path before the value that the Set cannot create
		// can reach a zone of the Apply, which decides the problem first.
		if c := d.pending; c != nil {
			err := d.stepProblem(*c)
			if err != nil {
				return nil, modelPlace{}, err
			}
		}

		return nil, modelPlace{}, d.fail(h, "%s %s, but %s", d.subject(), named, why)
	}

	// A placement puts an entry, so it does not apply to an item.
	if next.Kind == paths.SelectorIndex && at.kind != placeLast {
		return nil, modelPlace{}, d.itemPlacement(h, named, at, holderPath, content, next)
	}

	if !holdsCollection(content) {
		err = d.fillNull(ctx, holder, content, next, named)
		if err != nil {
			return nil, modelPlace{}, err
		}

		// The path now reads the empty collection where it read null.
		found := p.found

		p, err = d.nodePlace(h)
		if err == nil && !p.exists && p.found == found {
			holder, err = d.resolveFrom(p, holderPath)
		}

		if err != nil || p.exists || p.found != found || !holder.exists || !selectsInto(p.container, next) {
			return nil, modelPlace{}, d.bug(h, fmt.Errorf("%s holds no collection after the edit fills it", holderPath))
		}

		content = p.container
	}

	entries, _ := readEntries(content)
	seq, _ := content.(*ast.SequenceNode) //nolint:errcheck // An index past a mapping fails in createProblem.
	wrapped := wrapValue(v, sels[p.found+1:])

	// The collections above v stay in block style, so v goes in flow style
	// inside them.
	fd := noFlow
	if cfg.flow {
		fd = flowDepth(len(sels) - 1 - p.found)
	}

	var (
		mv    *edge
		where modelPlace
	)

	if next.Kind == paths.SelectorChild {
		where, err = d.placeEntry(h, named, holder, holderPath, content, at)
		if err != nil {
			return nil, modelPlace{}, err
		}

		err = d.placeRead(content, next.Name, where)
		if err != nil {
			return nil, modelPlace{}, d.readFail(h, fmt.Errorf("%s sets %s, which %w", d.subject(), h.path, err))
		}
	}

	origin := d.addNote(d.createdNote(holder, named))

	if next.Kind == paths.SelectorChild {
		mv, err = d.insertEntry(ctx, h, holderPath, holder, content, entries, next.Name, wrapped, fd, origin, where)
	} else {
		mv, err = d.insertItem(ctx, h, holderPath, holder, seq, len(seq.Values), wrapped, fd, origin)
	}

	if err != nil {
		return nil, modelPlace{}, err
	}

	// The model names the collections above v by the selectors of the
	// path, so it takes v from inside them.
	return innerEdge(mv, len(sels)-1-p.found), where, nil
}

// itemPlacement returns the problem of a call of h, which named names,
// with the placement at, at an item that the value at holderPath, whose
// content is content, takes at next. A placement puts an entry, so it
// does not apply to an item. The value holds a sequence, or null, which
// becomes a sequence.
func (d *Doc) itemPlacement(
	h *Node, named string, at placement, holderPath paths.Path, content ast.Node, next paths.Selector,
) error {
	if _, ok := content.(*ast.SequenceNode); ok {
		return d.fail(h, "%s %s %s, but %s holds a sequence", d.subject(), named, at.words(), d.describe(holderPath))
	}

	return d.fail(h, "%s %s %s, but %s is an item", d.subject(), named, at.words(), holderPath.Index(next.Index))
}

// selectsInto reports whether sel selects into content, a value as
// [Doc.readValue] reads it: a name into a mapping and an index into a
// sequence.
func selectsInto(content ast.Node, sel paths.Selector) bool {
	if sel.Kind == paths.SelectorIndex {
		_, ok := content.(*ast.SequenceNode)

		return ok
	}

	_, ok := readEntries(content)

	return ok
}

// holdsCollection reports whether content, a value as [Doc.readValue] reads
// it, holds a mapping or a sequence.
func holdsCollection(content ast.Node) bool {
	_, isMapping := readEntries(content)
	_, isSequence := content.(*ast.SequenceNode)

	return isMapping || isSequence
}

// placeEntry returns the place of the entry that the call of h creates in
// the mapping content at holderPath, the value of holder, as the model
// reads it. The placement of the call is at. What names the call as a
// message names it, such as "sets $.a". The call fails when at names a
// key that the mapping does not write.
func (d *Doc) placeEntry(
	h *Node, what string, holder *place, holderPath paths.Path, content ast.Node, at placement,
) (modelPlace, error) {
	where := modelPlace{placement: at}

	if at.kind != placeBefore && at.kind != placeAfter {
		return where, nil
	}

	anchor, err := d.resolveFrom(holder, holderPath.Child(at.key))

	switch {
	case err != nil || !anchor.exists || anchor.entry == nil:
		return modelPlace{}, d.fail(h, "%s %s %s %q, but the document holds no %s",
			d.subject(), what, at.side(), at.key, holderPath.Child(at.key))

	case anchor.merged || anchor.container != content:
		return modelPlace{}, d.fail(h, "%s %s %s %q, but a `<<` merge key brings %s into %s",
			d.subject(), what, at.side(), at.key, at.key, d.describe(holderPath))
	}

	where.entry = anchor.entry
	where.anchor = d.modelEdge(anchor.entry)

	return where, nil
}

// createProblem returns the reason that an edit cannot create a value at
// the place below holder, the value at holderPath, that rest selects, or
// "" when it can. The content of holder is content, as a decode reads it.
// The reason reads as the words that a message puts after "but", such as
// "s holds 1 item". The edit creates each value that the path lacks below
// holder. A name there selects an entry of a mapping, and an index selects
// the item past the last of a sequence. A holder that reads null, as
// [Doc.nullPlace] finds it, holds no content, so the edit creates the
// collection that rest needs there, and that collection holds no items.
// Each sequence that the edit creates below holder holds no items either,
// so it takes the index 0 alone, and the reason names that sequence.
func (d *Doc) createProblem(holder *place, holderPath paths.Path, content ast.Node, rest []paths.Selector) string {
	name := d.describe(holderPath)
	next := rest[0]

	_, isMapping := readEntries(content)
	seq, isSequence := content.(*ast.SequenceNode)

	items := 0
	if isSequence {
		items = len(seq.Values)
	}

	if _, null := d.nullPlace(holder, content); null && next.Index == 0 {
		isMapping, isSequence = next.Kind == paths.SelectorChild, next.Kind != paths.SelectorChild
	}

	switch {
	case next.Kind == paths.SelectorChild && !isMapping, next.Kind != paths.SelectorChild && !isSequence:
		return name + " holds " + d.kindOf(holder, content)
	case next.Kind != paths.SelectorChild && next.Index != items:
		return name + " holds " + countItems(items)
	}

	return missingProblem(appendSelectors(holderPath, rest[:1]), rest[1:])
}

// innerEdge returns the edge depth collections down from e, an edge to an
// encode of mappings of one entry each and sequences of one item each, or
// nil when e does not hold them.
func innerEdge(e *edge, depth int) *edge {
	for range depth {
		if e == nil || e.val == nil || e.val.kind != vMapping && e.val.kind != vSequence || len(e.val.edges) != 1 {
			return nil
		}

		e = e.val.edges[0]
	}

	return e
}

// countItems returns n items as a message counts them, as in "no items",
// "1 item", or "2 items".
func countItems(n int) string {
	switch n {
	case 0:
		return "no items"
	case 1:
		return "1 item"
	default:
		return fmt.Sprintf("%d items", n)
	}
}

// describe returns the name of the value at path as a message gives it:
// the key of its entry, or "the document" for the root.
func (d *Doc) describe(path paths.Path) string {
	last, ok := path.Last()

	switch {
	case !ok:
		return "the document"
	case last.Kind == paths.SelectorChild:
		return last.Name
	default:
		return path.String()
	}
}

// kindOf returns what the value of p holds, as a message names it. The
// content of the value is content, as [Doc.readValue] reads it. A scalar
// holds null where [Doc.readsNull] reports it.
func (d *Doc) kindOf(p *place, content ast.Node) string {
	switch {
	case holdsCollection(content):
		if _, ok := readEntries(content); ok {
			return "a mapping"
		}

		return "a sequence"

	case d.readsNull(p):
		return "null"
	}

	return "a scalar"
}

// readsNull reports whether a decode of the value of p, a scalar, reads
// null. So "!!str ~", which reads "", does not read null, and neither does
// "!t ~", which reads "~".
//
//nolint:contextcheck // The decode of a scalar reads no state that a context carries.
func (d *Doc) readsNull(p *place) bool {
	value := p.value

	target, err := d.node.DocumentResolver().Deref(value)
	if err == nil {
		value = target
	}

	n, _ := hook.Scope(d.node, p.path, value).(*niceyaml.Node) //nolint:errcheck // The hook gives a *niceyaml.Node.

	v, err := n.Decode[any](context.Background(), niceyaml.WithSelfValidation(false))

	return err == nil && v == nil
}

// nullPlace returns the place of the text that writes the null that
// holder, a place that holds a value, reads, whose content is content, as
// [Doc.readValue] reads it. Where the path of holder reads the null through
// an alias, that place is the anchored value that the alias reads, so each
// alias of the anchor reads what an edit writes there. It reports false
// where holder does not read null, as [Doc.readsNull] tells, and for an
// alias that reads null inside the value of its own anchor.
func (d *Doc) nullPlace(holder *place, content ast.Node) (*place, bool) {
	if !holder.exists || holdsCollection(content) || !d.readsNull(holder) {
		return nil, false
	}

	if _, ok := content.(*ast.AliasNode); ok {
		return nil, false
	}

	_, aliases, err := d.readValue(holder.value)

	switch {
	case err != nil:
		return nil, false
	case len(aliases) == 0:
		return holder, true
	}

	alias, ok := aliases[len(aliases)-1].(*ast.AliasNode)
	if !ok {
		return nil, false
	}

	anchor, err := d.node.DocumentResolver().Anchor(alias)
	if err != nil {
		return nil, false
	}

	at, err := d.resolve(d.textPath(anchor))
	if err != nil || !at.exists {
		return nil, false
	}

	if a, ok := keptAnchorOf(at.value); !ok || a != anchor {
		return nil, false
	}

	return at, true
}

// fillNull writes the empty collection that next selects into in place of
// the null that holder reads, whose content is content, as
// [Doc.nullPlace] finds it: an empty mapping for a name and an empty
// sequence for an index. The collection keeps the anchor of the null, its
// key, and its comments, as a Set of the collection there does. Named
// names the call that creates a value below it, as in "sets $.a.b". The
// call reaches the zones of the text before the fill writes, as
// [Doc.stepProblem] and [Doc.reachProblem] tell. So a zone that the fill
// rewrites still refuses the call. The rules of the value that the call
// creates come with that value, so the write of the collection does not
// check them.
func (d *Doc) fillNull(ctx context.Context, holder *place, content ast.Node, next paths.Selector, named string) error {
	at, ok := d.nullPlace(holder, content)
	if !ok {
		h := &Node{d: d, path: holder.path, hidden: holder.via}

		return d.bug(h, fmt.Errorf("%s does not read null", holder.path))
	}

	pending := d.pending
	if pending != nil {
		err := d.stepProblem(*pending)
		if err == nil {
			err = d.reachProblem(*pending)
		}

		if err != nil {
			return err
		}
	}

	d.pending = nil

	defer func() { d.pending = pending }()

	_, err := d.replaceValue(ctx, &Node{d: d, path: at.path, hidden: at.via}, at, emptyFor(next), false, named)

	return err
}

// createdNote returns the note of text that a call creates below holder,
// where call says what the call does, as in "sets $.a.b". The user's text
// holds no token for that text. The note holds the place where
// [Doc.placeSite] binds a problem at holder, under the path of that site.
// A token of the text binds as [Doc.writtenSite] binds a token that an
// edit created.
func (d *Doc) createdNote(holder *place, call string) note {
	s, ok := d.valueSite(holder)
	if !ok {
		s, ok = d.placeSite(holder.path)
	}

	return note{detail: d.subject(), call: call, path: s.path, at: s.at, hasPath: true, located: ok}
}

// insertEntry inserts the entry key with value into the mapping content at
// holderPath, at the place that where gives. The value goes in flow style
// from the depth fd on, as [render] writes it. It returns value as the
// model reads it, as [Node.set] does.
func (d *Doc) insertEntry(
	ctx context.Context,
	h *Node,
	holderPath paths.Path,
	holder *place,
	content ast.Node,
	entries []*ast.MappingValueNode,
	key string,
	value any,
	fd flowDepth,
	origin int,
	where modelPlace,
) (*edge, error) {
	flow := holder.flow || isFlow(content)

	keyText := renderKey(ctx, key, nil, flow)
	step := d.stepOf(holder, holderPath)

	pl, err := d.renderPayload(ctx, h, value, fd, step, origin, nil, d.cfg.marshalers)
	if err != nil {
		return nil, err
	}

	err = d.insertPayload(h, holderPath, holder, content, entries, newRun(keyText, origin), pl, origin, where)

	return &edge{val: pl.value, tag: pl.tag}, err
}

// insertPayload inserts the entry key with the payload pl into the mapping
// content at holderPath, at the place that where gives: after the last
// entry, before the first, or before or after the entry of where.
func (d *Doc) insertPayload(
	h *Node,
	holderPath paths.Path,
	holder *place,
	content ast.Node,
	entries []*ast.MappingValueNode,
	key run,
	pl payload,
	origin int,
	where modelPlace,
) error {
	// The entries give the places, not their keys, so a `<<` merge key or
	// a key that the mapping writes twice counts as the entry it is. For an
	// After, next is the entry that follows the entry of the key, or nil
	// when that entry is the last.
	var before, after, next *ast.MappingValueNode

	switch {
	case len(entries) == 0:
	case where.kind == placeFirst:
		before = entries[0]
	case where.kind == placeBefore && where.entry != nil:
		before = where.entry
	case where.kind == placeAfter && where.entry != nil:
		after = where.entry

		if i := slices.Index(entries, after); i >= 0 && i+1 < len(entries) {
			next = entries[i+1]
		}
	}

	if isFlow(content) {
		block, ok := d.blockOpening(holder, content)
		if !ok {
			text := appendRun(appendRun(key, newRun(": ", origin)), pl.flow)

			// An entry after the entry of a key goes before next. The text of
			// the entry of the key thus stays as the user wrote it, up to its
			// comma. After the last entry, the new entry goes last.
			switch {
			case before != nil:
				return d.applyEdit(h, d.flowInsertBefore(entryPlace(content, before), text, origin))
			case next != nil:
				return d.applyEdit(h, d.flowInsertBefore(entryPlace(content, next), text, origin))
			}

			return d.applyEdit(h, d.flowInsert(content, text, origin))
		}

		col := block.column(d, d.stepOf(holder, holderPath), true)
		brk := d.breakBefore(d.lineOf(block.keyStart) + 1)

		return d.applyEdit(h, d.openingEdits(block, d.entryLines(key, pl, col, brk), brk, pl, origin)...)
	}

	first := entryPlace(content, entries[0])
	col := d.keyColumn(first)

	if before != nil {
		at := entryPlace(content, before)
		brk := d.breakBefore(d.blockRegion(at).first)

		return d.insertBefore(h, at, d.entryLines(key, pl, col, brk), brk, origin)
	}

	last := entryPlace(content, entries[len(entries)-1])
	if after != nil {
		last = entryPlace(content, after)
	}

	lastLine := d.lastLineOf(last)
	brk := d.breakBefore(lastLine + 1)
	text := appendRun(d.entryLines(key, pl, col, brk), newRun(brk, origin))

	return d.applyEdit(h, d.linesEdit(lastLine+1, text, brk, pl, origin)...)
}

// insertBefore inserts lines, the lines of a new entry or item that end
// with the line break brk, before p, an entry or an item of a block
// collection, above the comment lines that belong to p. When p starts on
// the line of the "-" of the item that holds it, the first line of lines
// takes that place. Then p goes on the line after them at its column.
func (d *Doc) insertBefore(h *Node, p *place, lines run, brk string, origin int) error {
	text := appendRun(lines, newRun(brk, origin))

	if !d.sharesLine(p) {
		at := d.lineStart(d.blockRegion(p).first)

		return d.applyEdit(h, edit{start: at, end: at, text: text.text, origins: text.origins})
	}

	start, _ := d.startOf(p)
	cut := len(text.text) - len(strings.TrimLeft(text.text, " "))
	text = appendRun(run{text: text.text[cut:], origins: text.origins[cut:]},
		newRun(strings.Repeat(" ", d.column(start)), origin))

	return d.applyEdit(h, edit{start: start, end: start, text: text.text, origins: text.origins})
}

// lastLineOf returns the last line of p, an entry or an item of a block
// collection, with the comments that belong to it, as [Doc.blockRegion]
// gives them. The lines of a new entry or item go after it, so the comment
// lines that the user indented past the key or the "-" of p stay with p. A
// block scalar with keep chomping at the end of the value adds the blank
// lines that it holds. A last line of the document of spaces alone without
// a line break stays after the new lines, so that a line break does not
// end it. A last line that holds text of a block scalar at the end of the
// value is the exception. The blank lines before a last line that stays
// then hold text of a scalar without keep chomping no more, so they stay
// after the new lines too.
func (d *Doc) lastLineOf(p *place) int {
	keeps := d.keepsTrailingLines(p)
	end := d.blockRegion(p).end

	line := d.lineOf(max(0, end-1))
	start, _ := d.startOf(p)
	first := d.lineOf(start)

	if line > first && d.isBlankLine(line) && d.lineEnd(line) == len(d.cur) && !d.endsText(p.value) {
		line--

		for !keeps && line > first && d.isBlankLine(line) {
			line--
		}
	}

	return line
}

// endsText reports whether the text of node ends with a block scalar whose
// text ends the document, as [Doc.scalarEnd] tells.
func (d *Doc) endsText(node ast.Node) bool {
	lit := endScalar(node)
	if lit == nil {
		return false
	}

	s, ok := d.nodeSpan(lit)

	return ok && s.end == len(d.cur)
}

// linesEdit returns the edits that write text, the lines of a new entry or
// item with pl that end with the line break brk, at the start of line.
// Lines at the end of the text end as the text of the user ends. They lose
// brk at their end when the user's text lacks a final newline, or when
// the current text does. They take brk before them when the last line of
// the current text lacks one. A block scalar that needs the line break at
// the end of its text takes it back, as [Doc.keepStrings] writes it. A pl
// that ends with a block scalar with keep chomping takes the place of the
// blank lines from line on, which its string would hold. Another block
// scalar takes the place of the blank lines up to the last that holds
// spaces, and writes them empty, as [Doc.spacedBlanks] tells.
func (d *Doc) linesEdit(line int, text run, brk string, pl payload, origin int) []edit {
	at := d.lineStart(line)
	end := at

	switch {
	case pl.keeps:
		end = d.blanksEnd(line)

	case pl.scalar:
		if spaced := d.spacedBlanks(line); spaced >= line {
			end = d.lineStart(spaced + 1)
			text = appendRun(text, newRun(strings.Repeat(brk, d.emptiedBreaks(line, spaced)), origin))
		}
	}

	if at == len(d.cur) {
		bare := !endsLine(d.cur) || d.userBare()
		if bare && strings.HasSuffix(text.text, brk) {
			text = run{text: strings.TrimSuffix(text.text, brk), origins: text.origins[:len(text.origins)-len(brk)]}
		}

		if !endsLine(d.cur) {
			text = prefixRun(brk, text, origin)
		}
	}

	return []edit{{start: at, end: end, text: text.text, origins: text.origins}}
}

// applyEdit applies edits, and returns the problem of text the engine
// cannot parse.
func (d *Doc) applyEdit(h *Node, edits ...edit) error {
	err := d.apply(edits...)
	if err != nil {
		return d.bug(h, err)
	}

	return nil
}

// entryLines returns the lines of a block entry with key and pl, at column
// col, joined with brk.
func (d *Doc) entryLines(key run, pl payload, col int, brk string) run {
	var out runBuilder

	pad := strings.Repeat(" ", col)

	for _, l := range pl.above {
		out.add(newRun(strings.Repeat(" ", col+l.indent), originOf(l.text)))
		out.add(l.text)
		out.add(newRun(brk, originOf(l.text)))
	}

	out.add(newRun(pad, originOf(key)))
	out.add(key)
	out.add(newRun(":", originOf(key)))

	if pl.inline != nil {
		out.add(newRun(" ", originOf(*pl.inline)))
		out.add(*pl.inline)
	}

	if pl.comment != nil {
		out.add(newRun(" ", originOf(*pl.comment)))
		out.add(*pl.comment)
	}

	if len(pl.lines) > 0 {
		out.add(newRun(brk, originOf(key)))
		out.add(d.joinLines(pl.lines, col, brk))
	}

	return out.run()
}

// opening is an empty flow collection in block context. The collection
// takes block style when an edit adds to it.
type opening struct {
	// The bytes that the block lines take the place of. On the line of the
	// key or the "-", they are the collection and the blanks before it.
	// Below the key or the "-", they are the line of the collection, or
	// the collection and the blanks after it when a comment follows it.
	start int
	end   int
	// The offset where the block lines go: the end of the collection, or
	// the end of the comment after it on its line. That comment thus stays
	// on its line, above the lines.
	at int
	// The start of the key or the "-" of the place that holds it, or of the
	// collection at the root.
	keyStart int
	// Whether the collection is the root of the document, or the value of
	// an item of a block sequence.
	root bool
	item bool
	// Whether the first line goes after the "-" of an item. A comment after
	// the collection keeps the line of the "-", so the lines go below it.
	dash bool
	// Whether the lines of the root go below the line of the collection,
	// which holds a "---" before it or a comment after it.
	apart bool
}

// column returns the column of the keys or the "-" of the block lines.
// Below a key, a block mapping indents by step, and a block sequence
// indents by step when indent is true. The lines of the root start at the
// first column, and those of an item at the column after its "- ".
func (o opening) column(d *Doc, step int, indent bool) int {
	switch {
	case o.root:
		return 0
	case o.item:
		return d.column(o.keyStart) + 2
	case indent:
		return d.column(o.keyStart) + step
	default:
		return d.column(o.keyStart)
	}
}

// edits returns the edits that write lines, block lines at the column of
// o joined with brk, in place of the collection. The lines of an entry go
// below its key. The first line of an item goes after its "-", unless a
// comment after the collection keeps that line. The lines of the root take
// the place of the collection, or go below its line when o is apart. The
// bytes that join the lines to the text before them take origin.
func (o opening) edits(lines run, brk string, origin int) []edit {
	text := prefixRun(brk, lines, origin)

	switch {
	case o.root && !o.apart:
		text = lines
	case o.dash:
		trimmed := strings.TrimLeft(lines.text, " ")
		cut := len(lines.text) - len(trimmed)
		text = prefixRun(" ", run{text: trimmed, origins: lines.origins[cut:]}, origin)
	}

	return []edit{
		{start: o.start, end: o.end},
		{start: o.at, end: o.at, text: text.text, origins: text.origins},
	}
}

// openingEdits returns the edits of o that write lines, a new entry or item
// with pl, as [opening.edits] returns them. The lines go before the line
// break of the line of o. They take the place of the blank lines after it
// when pl ends with a block scalar with keep chomping, as
// [Doc.keptLinesEnd] tells.
func (d *Doc) openingEdits(o opening, lines run, brk string, pl payload, origin int) []edit {
	edits := o.edits(lines, brk, origin)
	last := &edits[len(edits)-1]
	text := d.indentPast(run{text: last.text, origins: last.origins}, pl.tail, d.lineOf(last.end)+1)

	end, text := d.keptLinesEnd(pl, last.end, text, brk, origin)
	last.end, last.text, last.origins = end, text.text, text.origins

	return edits
}

// blockOpening returns the empty flow collection content, the value of
// holder, when it lies in block context. The collection holds only blanks
// between its brackets, as in "{}" or "{ }". The holder is an entry, the
// root, or an item of a block sequence. At the root, the collection must
// start its line or follow the "---" of the document header. In an item,
// it must follow the "-" and the blanks after it. In both, an anchor or a
// tag on the line of the collection keeps it in flow style. A collection
// of an entry or an item can also start a line below the key or the "-".
// One at the root can start a line below its anchor or its tag. When
// holder reads content through an alias, the entry or the item that holds
// content in the text takes the place of holder.
func (d *Doc) blockOpening(holder *place, content ast.Node) (opening, bool) {
	if astnode.Content(holder.value) != content {
		in, ok := d.holderOf(content)
		if !ok {
			return opening{}, false
		}

		in.flow = slices.ContainsFunc(d.ancestors(content), isFlow)
		holder = in
	}

	if holder.flow {
		return opening{}, false
	}

	s, ok := d.nodeSpan(content)
	if !ok || s.end-s.start < 2 || strings.Trim(d.cur[s.start+1:s.end-1], " \t\r\n") != "" {
		return opening{}, false
	}

	o := opening{start: s.start, end: s.end, at: s.end, root: holder.root}
	if !o.root {
		o.keyStart, ok = d.startOf(holder)
		o.item = holder.entry == nil

		if !ok || o.item && (holder.seq == nil || holder.seq.IsFlowStyle) {
			return opening{}, false
		}
	}

	for o.start > 0 && d.cur[o.start-1] == ' ' {
		o.start--
	}

	c := d.trailingComment(s.end)

	// The blanks after the collection go with it, so they do not end the
	// last of the lines.
	if end := d.lineEnd(d.lineOf(s.end)); c < 0 && strings.Trim(d.cur[s.end:end], " \t") == "" {
		o.end, o.at = end, end
	}

	lineStart := d.lineStart(d.lineOf(s.start))

	// The collection starts a line below its key or its "-".
	below := !o.root && o.start == lineStart

	// The blanks between the "-" of an item and the collection go with it,
	// as a tab there does.
	if o.item && !below {
		if strings.Trim(d.cur[o.keyStart+1:o.start], " \t") != "" {
			return opening{}, false
		}

		o.start = o.keyStart + 1
	}

	switch {
	case o.root:
		return d.rootOpening(o, s, c)
	case below && c >= 0:
		// The comment keeps its line and its indent, and the lines go below
		// it.
		o.start, o.end, o.at = s.start, c, d.lineEnd(d.lineOf(c))
	case below:
		// The line of the collection goes, so the lines take its place.
		o.start = d.lineEnd(d.lineOf(s.start) - 1)
	case c >= 0:
		o.at = d.lineEnd(d.lineOf(c))
	case o.item:
		o.dash = true
	case !tabKeepsKey(holder.entry):
		// The blanks after the ":" go with the collection, so the line of the
		// key ends at the ":".
		for o.start > 0 && (d.cur[o.start-1] == ' ' || d.cur[o.start-1] == '\t') {
			o.start--
		}
	}

	return o, true
}

// rootOpening returns o, the opening of the collection at the root whose
// bytes are s, with the place of its block lines. The comment after the
// collection on its line starts at c, or c is -1. The collection must
// start its line, or follow the "---" of the document header. A comment
// after it, or the "---", keeps its line, and the lines go below it. An
// anchor or a tag on the line of the collection keeps it in flow style. A
// collection can start a line below its anchor or its tag.
func (d *Doc) rootOpening(o opening, s span, c int) (opening, bool) {
	lineStart := d.lineStart(d.lineOf(s.start))
	header := strings.TrimRight(d.cur[lineStart:o.start], " \t") == headerMarker

	switch {
	case header:
		o.start, o.apart = lineStart+len(headerMarker), true
	case o.start != lineStart:
		return opening{}, false
	case c >= 0:
		// The comment keeps its indent.
		o.start, o.end, o.apart = s.start, c, true
	}

	if c >= 0 {
		o.at = d.lineEnd(d.lineOf(c))
	}

	o.keyStart = o.start

	return o, true
}

// flowInsert returns the edit that inserts text as the last item or entry
// of the flow collection content. The text goes after the last item. When
// a comma ends that item before the closing bracket, the text goes after
// the comma and takes a comma of its own, so the collection keeps its
// trailing comma. A comment after the last item stays after the text.
func (d *Doc) flowInsert(content ast.Node, text run, origin int) edit {
	var (
		end  *token.Token
		last ast.Node
	)

	switch c := content.(type) {
	case *ast.MappingNode:
		end = c.End
		if len(c.Values) > 0 {
			last = c.Values[len(c.Values)-1]
		}

	case *ast.SequenceNode:
		end = c.End
		if len(c.Values) > 0 {
			last = c.Values[len(c.Values)-1]
		}
	}

	closing, _ := d.tokenSpan(end)

	s, ok := d.nodeSpan(last)
	if !ok {
		return edit{start: closing.start, end: closing.start, text: text.text, origins: text.origins}
	}

	if i := d.skipBlank(s.end, closing.start); i < closing.start && d.cur[i] == ',' {
		text = appendRun(prefixRun(" ", text, origin), newRun(",", origin))

		return edit{start: i + 1, end: i + 1, text: text.text, origins: text.origins}
	}

	text = prefixRun(", ", text, origin)

	return edit{start: s.end, end: s.end, text: text.text, origins: text.origins}
}

// flowInsertBefore returns the edit that inserts text as an item or an
// entry of a flow collection before p, an item or an entry of the
// collection. The text takes a comma of its own.
func (d *Doc) flowInsertBefore(p *place, text run, origin int) edit {
	start, _ := d.startOf(p)
	text = appendRun(text, newRun(", ", origin))

	return edit{start: start, end: start, text: text.text, origins: text.origins}
}

// skipBlank returns the offset of the first byte from off up to end that
// is not a space, a line break, or a comment, or end when every byte is.
func (d *Doc) skipBlank(off, end int) int {
	for off < end {
		switch {
		case strings.ContainsRune(" \t\r\n", rune(d.cur[off])):
			off++
		case d.cur[off] == '#':
			off = d.lineEnd(d.lineOf(off))
		default:
			return off
		}
	}

	return end
}

// insertItem inserts value as the item at index of the sequence seq, the
// value of holder, before the item there. An index past the last item
// appends the value. The value goes in flow style from the depth fd on,
// as [render] writes it. It returns value as the model reads it, as
// [Node.set] does.
func (d *Doc) insertItem(
	ctx context.Context,
	h *Node,
	holderPath paths.Path,
	holder *place,
	seq *ast.SequenceNode,
	index int,
	value any,
	fd flowDepth,
	origin int,
) (*edge, error) {
	step := d.stepOf(holder, holderPath)

	pl, err := d.renderPayload(ctx, h, value, fd, step, origin, nil, d.cfg.marshalers)
	if err != nil {
		return nil, err
	}

	return &edge{val: pl.value, tag: pl.tag}, d.insertPayloadItem(h, holderPath, holder, seq, index, pl, origin)
}

// insertPayloadItem inserts the payload pl as the item at index of the
// sequence seq, the value of holder, before the item there. An index past
// the last item appends the payload.
func (d *Doc) insertPayloadItem(
	h *Node,
	holderPath paths.Path,
	holder *place,
	seq *ast.SequenceNode,
	index int,
	pl payload,
	origin int,
) error {
	step := d.stepOf(holder, holderPath)

	var at *place

	if index < len(seq.Values) {
		atPath := holderPath.Index(index)

		p, err := d.resolveFrom(holder, atPath)
		if err != nil {
			return d.bug(h, fmt.Errorf("%s %w", atPath, err))
		}

		at = p
	}

	if seq.IsFlowStyle {
		block, ok := d.blockOpening(holder, seq)

		switch {
		case !ok && at != nil:
			return d.applyEdit(h, d.flowInsertBefore(at, pl.flow, origin))
		case !ok:
			return d.applyEdit(h, d.flowInsert(seq, pl.flow, origin))
		}

		col := block.column(d, step, d.sequenceIndent())
		brk := d.breakBefore(d.lineOf(block.keyStart) + 1)

		return d.applyEdit(h, d.openingEdits(block, d.itemLines(pl, col, brk), brk, pl, origin)...)
	}

	if at != nil {
		brk := d.breakBefore(d.blockRegion(at).first)

		return d.insertBefore(h, at, d.itemLines(pl, d.keyColumn(at), brk), brk, origin)
	}

	lastPath := holderPath.Index(len(seq.Values) - 1)

	last, err := d.resolveFrom(holder, lastPath)
	if err != nil {
		return d.bug(h, fmt.Errorf("%s %w", lastPath, err))
	}

	col := d.keyColumn(last)
	lastLine := d.lastLineOf(last)
	brk := d.breakBefore(lastLine + 1)
	text := appendRun(d.itemLines(pl, col, brk), newRun(brk, origin))

	return d.applyEdit(h, d.linesEdit(lastLine+1, text, brk, pl, origin)...)
}

// itemLines returns the lines of a block sequence item with pl, its "-" at
// column col, joined with brk.
func (d *Doc) itemLines(pl payload, col int, brk string) run {
	var out runBuilder

	for _, l := range pl.above {
		out.add(newRun(strings.Repeat(" ", col+l.indent), originOf(l.text)))
		out.add(l.text)
		out.add(newRun(brk, originOf(l.text)))
	}

	origin, dash := originOf(pl.flow), originOf(pl.flow)
	if pl.holder != nil {
		dash = *pl.holder
	}

	// An empty value leaves the "-" at the end of its line, or before the
	// comment of the value.
	if pl.inline == nil && len(pl.lines) == 0 {
		out.add(newRun(strings.Repeat(" ", col)+"-", dash))

		if pl.comment != nil {
			out.add(newRun(" ", origin))
			out.add(*pl.comment)
		}

		return out.run()
	}

	out.add(newRun(strings.Repeat(" ", col)+"- ", dash))

	switch {
	case pl.inline != nil:
		out.add(*pl.inline)

		if pl.comment != nil {
			out.add(newRun(" ", origin))
			out.add(*pl.comment)
		}

		// The lines keep their indentation relative to the "-", as the
		// lines of an entry do to its key. A block scalar with an
		// indentation indicator thus keeps its text. A block collection
		// goes on past the "-", even when its key held it without an
		// indent.
		if len(pl.lines) > 0 {
			at := col
			if pl.collection {
				at += max(0, 2-contentIndent(pl.lines))
			}

			out.add(newRun(brk, origin))
			out.add(d.joinLines(pl.lines, at, brk))
		}

	case len(pl.lines) > 0:
		// The first line goes after the "-", unless the comment of the
		// value takes that place.
		base := pl.lines[0].indent
		lines := pl.lines

		if pl.comment != nil {
			out.add(*pl.comment)
		} else {
			out.add(lines[0].text)

			lines = lines[1:]
		}

		for _, l := range lines {
			out.add(newRun(brk, origin))

			l.indent -= base

			out.add(d.joinLines([]line{l}, col+2, brk))
		}
	}

	return out.run()
}

// contentIndent returns the least indentation of lines, a block collection
// with its comment lines, that a line other than a comment takes.
func contentIndent(lines []line) int {
	least := -1

	for _, l := range lines {
		if l.text.text == "" || strings.HasPrefix(l.text.text, "#") {
			continue
		}

		if least < 0 || l.indent < least {
			least = l.indent
		}
	}

	return max(0, least)
}
