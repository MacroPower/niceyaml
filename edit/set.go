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

// Set replaces the value with v, or creates it and the mappings above it. It
// encodes v once, as [go.jacobcolvin.com/niceyaml/encoder.Marshal] does. It
// writes that encode, keeps the comments of the entry, and drops the tag of
// the old value and the comments inside it. A Set at an anchored value keeps
// the anchor and writes the new value after it, so each alias reads the new
// value, as the package documentation describes. A call of Set at the index
// past the last item of a sequence appends an item. A value that replaces an
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
// A reference document of [go.jacobcolvin.com/niceyaml.WithReferences]
// that a `<<` merge key reads decides which names the mapping of the merge
// key reads. A Set of a name that such a mapping does not write after the
// merge key fails with a problem that matches [paths.ErrReferenceAlias], as
// [Node.At] describes.
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
// A path names a key by its text. Set fails before it writes when a
// mapping of v holds two keys of one text, such as the int 1 and the
// string "1" of a map[any]any. The problem does not match [ErrEngine]. The
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
// fails before the encode with that error, which does not match ErrEngine,
// as the error of the marshaler of a value does not. A text of the encode
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
// [yaml.InterfaceMarshalerContext], and each such method runs twice. The
// encoder alone calls the MarshalText method of an
// [encoding.TextMarshaler], so that method runs once. Set also asks the
// encoder for the node of each key of a map of v alone, so each method of a
// key runs once more. A value that holds itself through a map, a slice, or
// a pointer fails the Set before the encoder runs, because the encoder
// writes such a value without end. So does a value whose marshaler returns
// a value that holds it, or a value equal to one that a marshaler on the
// way marshals. A chain of MarshalYAML methods, in which each method
// returns the value that the next marshals, holds at most 1,000 methods. A
// longer chain fails the Set too, and its message names the type of the
// first.
//
// The YAML text that a MarshalYAML method returns must be YAML of one
// node, so text of more than one document fails the Set. So does a mapping
// that repeats a key, as go-yaml's decoder reads each key, because YAML
// does not allow it. The problem names the path of the Set and the
// marshaler, and it does not match ErrEngine.
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
// YAML without directives.
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
// collection, a pair without braces in a flow sequence, or a construct
// that [New] refuses, such as the float key 1.0 of a map[any]any, which
// reads as 1. The error binds at the place of the Set, its InValue is
// true, and [niceyaml.IsInvalid] reports false for it, because the program
// wrote the construct. Such a construct can make go-yaml fail on the text
// of the encode, as an alias whose anchor the YAML lacks does, and the Set
// then fails with the construct. A scalar that go-yaml reads as a value of
// another kind fails the Set with ErrEngine before such a construct does.
func (n *Node) Set(ctx context.Context, v any) error {
	c := n.d.modelStart(n, "sets "+n.path.String(), true)

	mv, err := n.set(ctx, v, false)

	return n.d.modelEnd(c, err, func() bool { return c.set(mv) })
}

// set is [Node.Set], but it also sets a protected value when protected is
// true. It returns the value of v as the model reads it, from the encode
// that gives the text of v, as the edge to its place holds it.
func (n *Node) set(ctx context.Context, v any, protected bool) (*edge, error) {
	d := n.d

	p, err := n.place(useCreate, "sets")
	if err != nil {
		return nil, err
	}

	why := d.protection(p, true)

	switch {
	case p.root:
		return nil, d.fail(n, "%s replaces the root of the document", d.subject())
	case !protected && why != "":
		return nil, d.fail(n, "%s sets %s, which %s", d.subject(), n.path, why)
	}

	// The Set checks its reach once go-yaml took the text of its value, as
	// [Doc.renderPayload] checks it.
	d.pending = &reachCall{kind: callSet, path: n.path, hidden: n.hidden, named: "sets " + n.path.String()}

	defer func() { d.pending = nil }()

	// The paths below the place go. They reach the place as the text stands
	// before the edit.
	s := d.spotOf(n.path, p)
	at := d.reached(s)
	inside := d.readersInside(s, at, p)

	var mv *edge

	if p.exists {
		mv, err = d.setExisting(ctx, n, p, v)
	} else {
		mv, err = d.setCreated(ctx, n, p, v)
	}

	if err != nil {
		return nil, err
	}

	at.take(false, d.takenBy())

	for _, m := range inside {
		m.gone, m.goneBy = true, d.takenBy()
	}

	return mv, nil
}

// readersInside returns each node that reads a place inside the value of
// p, the place of a Set, through an alias or a `<<` merge key, as the text
// stands before the edit. The path of such a node does not start with the
// path of the place, so at, the nodes that reach the place, misses it. The
// Set replaces the value that the alias or the merge key reads, and the
// anchor of the value stays, so such a node loses its place too. A
// wildcard then skips it, as it skips each node whose place an earlier
// call took away.
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
			out = append(out, m)
		}
	}

	return out
}

// setExisting replaces the value of p, the place of h, with v. It returns
// the value of v as the model reads it, as [Node.set] does.
func (d *Doc) setExisting(ctx context.Context, h *Node, p *place, v any) (*edge, error) {
	d.recordEdit(p, "sets "+h.path.String(), "")

	return d.replaceValue(ctx, h, p, v)
}

// setCreated creates the value of p, the place of h, as v. It returns the
// value of v as the model reads it, as [Node.set] does.
func (d *Doc) setCreated(ctx context.Context, h *Node, p *place, v any) (*edge, error) {
	// A merge key can bring in the key that the edit creates in its source
	// over an entry on the path of h. The path then does not reach the new
	// value, so the check of the merges names the edit at the value above
	// it that the text holds.
	holderPath := prefixOf(h.path, p.found)

	holder, err := d.resolveFrom(p, holderPath)
	if err == nil && holder.exists {
		d.recordEdit(holder, "sets "+h.path.String(), "")
	}

	mv, err := d.createValue(ctx, h, p, v)
	if err != nil {
		return nil, err
	}

	created, err := d.nodePlace(h)
	if err == nil && created.exists {
		d.recordEdit(created, "sets "+h.path.String(), "")
	}

	return mv, nil
}

// renderPayload returns v as a payload for a place whose block collections
// indent by step. The text that the payload writes takes
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
	ctx context.Context, h *Node, v any, step, origin int, old *token.Token,
) (payload, error) {
	seqIndent := d.sequenceIndent()

	r, err := render(ctx, v, step, seqIndent)

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
		return payload{}, d.fail(h, "%s cannot write the value of %s: %v", d.subject(), h.path, err)
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

// replaceValue replaces the value of p with v. It returns the value of v
// as the model reads it, as [Node.set] does.
func (d *Doc) replaceValue(ctx context.Context, h *Node, p *place, v any) (*edge, error) {
	n := d.setNote(p, "sets "+h.path.String())

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

	pl, err := d.renderPayload(ctx, h, v, step, origin, scalarToken(p.value))
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

	err := d.apply(d.valueEdits(p, pl, origin, step)...)
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

// valueEdits returns the edits that replace the value of p with pl, whose
// bytes that join its lines take origin. A block mapping of pl that
// replaces a value on the line of its key indents by step.
func (d *Doc) valueEdits(p *place, pl payload, origin, step int) []edit {
	vs, ve := d.valueStart(p), d.valueEnd(p)
	keyStart, _ := d.startOf(p)
	keyCol := d.column(keyStart)

	oldBlock := !p.flow && p.value != nil && isBlockCollection(p.value)

	// A flow collection holds flow style, and so does a collection that
	// replaces a scalar or a flow collection.
	if p.flow || !oldBlock && pl.inline == nil {
		pl.inline, pl.lines = &pl.flow, nil
	}

	keyLine := d.lineOf(keyStart)
	brk := d.breakBefore(keyLine + 1)

	var edits []edit

	switch {
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

// createValue creates the value of p, and the mappings above it that the
// document lacks. It returns the value of v as the model reads it, as
// [Node.set] does.
func (d *Doc) createValue(ctx context.Context, h *Node, p *place, v any) (*edge, error) {
	var sels []paths.Selector

	for sel := range h.path.Selectors() {
		sels = append(sels, sel)
	}

	holderPath := appendSelectors(paths.Doc(), sels[:p.found])

	holder, err := d.resolveFrom(p, holderPath)

	switch {
	case err != nil:
		return nil, d.bug(h, fmt.Errorf("%s %w", holderPath, err))
	case !holder.exists:
		return nil, d.bug(h, fmt.Errorf("%s holds no value", holderPath))
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
				return nil, err
			}
		}

		return nil, d.fail(h, "%s sets %s, but %s", d.subject(), h.path, why)
	}

	entries, _ := readEntries(content)
	seq, _ := content.(*ast.SequenceNode) //nolint:errcheck // An index past a mapping fails in createProblem.
	wrapped := v

	for i := len(sels) - 1; i > p.found; i-- {
		wrapped = map[string]any{sels[i].Name: wrapped}
	}

	origin := d.addNote(d.createdNote(holder, "sets "+h.path.String()))

	var mv *edge

	if next.Kind == paths.SelectorChild {
		mv, err = d.insertEntry(ctx, h, holderPath, holder, content, entries, next.Name, wrapped, origin)
	} else {
		mv, err = d.appendItem(ctx, h, holderPath, holder, seq, wrapped, origin)
	}

	if err != nil {
		return nil, err
	}

	// The model names the mappings above v by the names of the path, so it
	// takes v from inside them.
	return innerEdge(mv, len(sels)-1-p.found), nil
}

// createProblem returns the reason that an edit cannot create a value at
// the place below holder, the value at holderPath, that rest selects, or
// "" when it can. The content of holder is content, as a decode reads it.
// The reason reads as the words that a message puts after "but", such as
// "s holds 1 item". The edit appends an item at the index past the last
// item of a sequence, and it creates mappings alone below it. A path
// through an index below the first value that the document lacks thus
// fails there, and the reason names that value.
func (d *Doc) createProblem(holder *place, holderPath paths.Path, content ast.Node, rest []paths.Selector) string {
	name := d.describe(holderPath)
	next := rest[0]

	_, isMapping := readEntries(content)
	seq, isSequence := content.(*ast.SequenceNode)

	switch {
	case next.Kind == paths.SelectorChild && !isMapping, next.Kind != paths.SelectorChild && !isSequence:
		return name + " holds " + d.kindOf(holder, content)
	case next.Kind != paths.SelectorChild && (next.Index != len(seq.Values) || len(rest) > 1):
		return name + " holds " + countItems(len(seq.Values))
	}

	for _, sel := range rest[1:] {
		if sel.Kind != paths.SelectorChild {
			return "the document holds no " + holderPath.Child(next.Name).String()
		}
	}

	return ""
}

// innerEdge returns the edge depth mappings down from e, an edge to an
// encode of mappings of one entry each, or nil when e does not hold them.
func innerEdge(e *edge, depth int) *edge {
	for range depth {
		if e == nil || e.val == nil || e.val.kind != vMapping || len(e.val.edges) != 1 {
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
// holds null where a decode of the place reads null. So "!!str ~", which
// reads "", holds a scalar, and so does "!t ~", which reads "~".
//
//nolint:contextcheck // The decode of a scalar reads no state that a context carries.
func (d *Doc) kindOf(p *place, content ast.Node) string {
	if _, ok := readEntries(content); ok {
		return "a mapping"
	}

	if _, ok := content.(*ast.SequenceNode); ok {
		return "a sequence"
	}

	value := p.value

	target, err := d.node.Resolver().Deref(value)
	if err == nil {
		value = target
	}

	n, _ := hook.Scope(d.node, p.path, value).(*niceyaml.Node) //nolint:errcheck // The hook gives a *niceyaml.Node.

	v, err := n.Decode[any](context.Background(), niceyaml.WithSelfValidation(false))
	if err == nil && v == nil {
		return "null"
	}

	return "a scalar"
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
// holderPath, after its last entry. It returns value as the model reads
// it, as [Node.set] does.
func (d *Doc) insertEntry(
	ctx context.Context,
	h *Node,
	holderPath paths.Path,
	holder *place,
	content ast.Node,
	entries []*ast.MappingValueNode,
	key string,
	value any,
	origin int,
) (*edge, error) {
	flow := holder.flow || isFlow(content)

	keyText := renderKey(ctx, key, nil, flow)
	step := d.stepOf(holder, holderPath)

	pl, err := d.renderPayload(ctx, h, value, step, origin, nil)
	if err != nil {
		return nil, err
	}

	err = d.insertPayload(h, holderPath, holder, content, entries, newRun(keyText, origin), pl, origin)

	return &edge{val: pl.value, tag: pl.tag}, err
}

// insertPayload inserts the entry key with the payload pl into the mapping
// content at holderPath, after its last entry.
func (d *Doc) insertPayload(
	h *Node,
	holderPath paths.Path,
	holder *place,
	content ast.Node,
	entries []*ast.MappingValueNode,
	key run,
	pl payload,
	origin int,
) error {
	if isFlow(content) {
		block, ok := d.blockOpening(holder, content)
		if !ok {
			text := appendRun(appendRun(key, newRun(": ", origin)), pl.flow)

			return d.applyEdit(h, d.flowInsert(content, text, origin))
		}

		col := block.column(d, d.stepOf(holder, holderPath), true)
		brk := d.breakBefore(d.lineOf(block.keyStart) + 1)

		return d.applyEdit(h, d.openingEdits(block, d.entryLines(key, pl, col, brk), brk, pl, origin)...)
	}

	// The entries give the places, not their keys, so a `<<` merge key or
	// a key that the mapping writes twice counts as the entry it is.
	first := entryPlace(content, entries[0])
	last := entryPlace(content, entries[len(entries)-1])

	col := d.keyColumn(first)
	lastLine := d.lastLineOf(last)
	brk := d.breakBefore(lastLine + 1)
	text := appendRun(d.entryLines(key, pl, col, brk), newRun(brk, origin))

	return d.applyEdit(h, d.linesEdit(lastLine+1, text, brk, pl, origin)...)
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
		bare := !endsLine(d.cur) || d.orig != "" && !endsLine(d.orig)
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

// opening is an empty flow collection that an edit wrote in block
// context. The collection takes block style when a later edit adds to it.
type opening struct {
	// The bytes that the block lines take the place of: the collection and
	// the space before it.
	start int
	end   int
	// The offset where the block lines go: the end of the collection, or
	// the end of the comment after it on its line. That comment thus stays
	// on the line of the key, above the lines.
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
// the place of the collection. The bytes that join the lines to the text
// before them take origin.
func (o opening) edits(lines run, brk string, origin int) []edit {
	text := prefixRun(brk, lines, origin)

	switch {
	case o.root:
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
// holder, when an edit wrote it in block context. The holder is an entry,
// the root, or an item of a block sequence. At the root and in an item,
// the collection must stand alone after the start of its line or the "- ",
// without an anchor or a tag before it. The root also takes no comment
// after it. When holder reads content through an alias, the entry or the
// item that holds content in the text takes the place of holder.
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
	if !ok || s.end-s.start != 2 || d.origin[s.start] >= 0 {
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

	switch {
	case o.root && (o.start != d.lineStart(d.lineOf(s.start)) || c >= 0):
		return opening{}, false
	case o.root:
		o.keyStart = o.start
	case o.item && o.start != o.keyStart+1:
		return opening{}, false
	case c >= 0:
		o.at = d.lineEnd(d.lineOf(c))
	default:
		o.dash = o.item
	}

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

// appendItem appends value as the last item of the sequence seq, the
// value of holder. It returns value as the model reads it, as [Node.set]
// does.
func (d *Doc) appendItem(
	ctx context.Context,
	h *Node,
	holderPath paths.Path,
	holder *place,
	seq *ast.SequenceNode,
	value any,
	origin int,
) (*edge, error) {
	step := d.stepOf(holder, holderPath)

	pl, err := d.renderPayload(ctx, h, value, step, origin, nil)
	if err != nil {
		return nil, err
	}

	return &edge{val: pl.value, tag: pl.tag}, d.appendPayload(h, holderPath, holder, seq, pl, origin)
}

// appendPayload appends the payload pl as the last item of the sequence
// seq, the value of holder.
func (d *Doc) appendPayload(
	h *Node,
	holderPath paths.Path,
	holder *place,
	seq *ast.SequenceNode,
	pl payload,
	origin int,
) error {
	step := d.stepOf(holder, holderPath)

	if seq.IsFlowStyle {
		block, ok := d.blockOpening(holder, seq)
		if !ok {
			return d.applyEdit(h, d.flowInsert(seq, pl.flow, origin))
		}

		col := block.column(d, step, d.sequenceIndent())
		brk := d.breakBefore(d.lineOf(block.keyStart) + 1)

		return d.applyEdit(h, d.openingEdits(block, d.itemLines(pl, col, brk), brk, pl, origin)...)
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

// fieldOf returns the name of the last selector of path, for a message.
func fieldOf(path paths.Path) string {
	last, ok := path.Last()
	if !ok {
		return "$"
	}

	if last.Kind == paths.SelectorChild {
		return last.Name
	}

	return fmt.Sprint(last.Index)
}
