package edit

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/bom"
	"go.jacobcolvin.com/niceyaml/position"
)

// misreadError is what go-yaml misreads in a text. The fix says what the
// user can write so that go-yaml reads the text as YAML does. A problem
// that the misread explains takes the fix as its detail, as
// [Doc.engineProblem] gives it. The user can rewrite the text, and the
// authors of the program cannot change go-yaml.
type misreadError struct {
	msg, fix string
}

// Error returns what go-yaml misreads.
func (e *misreadError) Error() string {
	return e.msg
}

// misreadKind is a kind of YAML that [New] refuses with [ErrMisread], as
// the list of the Supported YAML section of the package documentation names
// them, in the order of that list. Two instances at one place come in that
// order.
type misreadKind int

const (
	// An own key written before a `<<` merge key that brings in the same
	// key, which go-yaml's decoder lets the merge key override.
	misreadPrecedence misreadKind = iota
	// A key that two mappings of one `<<` merge key bring in, which
	// go-yaml's decoder reads from the later mapping.
	misreadOrder
	// A byte order mark at the start of the text of the first document of a
	// file, which go-yaml reads as text of the document.
	misreadMark
)

// misreadFound is an instance of a misread kind in the current text, as
// [Doc.misreads] finds it.
type misreadFound struct {
	// What go-yaml misreads, in words that name the places of the user's
	// text, and in words that name the edited text.
	msg, edited string
	// What the user can write so that go-yaml reads the text as YAML does.
	fix string
	// The site where the problem of the instance binds.
	at site
	// The offset in the current text of the first byte of the instance.
	start int
	kind  misreadKind
	// The offsets that order the instances of [misreadOrder] of one merge
	// key: the entries of the last and the first source of the key.
	order [2]int
}

// misreads returns each instance of a misread kind in the current text, in
// the order of the text and, at one place, in the order of the kinds.
func (d *Doc) misreads() []misreadFound {
	out := d.precedenceMisreads()

	if m, ok := d.markMisread(); ok {
		out = append(out, m)
	}

	slices.SortStableFunc(out, func(a, b misreadFound) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.kind, b.kind))
	})

	return out
}

// misreadRefusal returns the problem of New for the first misread of the
// user's text, as [Doc.firstMisread] finds it, or nil. The problem matches
// [ErrMisread] and binds at the site of the misread. New checks the
// misreads before the constructs of the subset, as the package
// documentation lists them.
func (d *Doc) misreadRefusal() error {
	m, ok := d.firstMisread()
	if !ok {
		return nil
	}

	return d.user.Bind( //nolint:wrapcheck // The binding is the problem of the document.
		d.engineMisread(&misreadError{msg: m.msg, fix: m.fix}, niceyaml.AtRange(m.at.at), niceyaml.AtPath(m.at.path)),
	)
}

// firstMisread returns the first misread of the current text, as
// [Doc.misreads] finds it, that New refuses with [ErrMisread]. A text with
// a tag that converts a collection holds none, because YAML refuses such a
// text, and New refuses it as [ConstructConvertingTag].
func (d *Doc) firstMisread() (misreadFound, bool) {
	found := d.misreads()
	if len(found) == 0 || d.convertsCollection() {
		return misreadFound{}, false
	}

	return found[0], true
}

// convertsCollection reports whether a tag of the current text converts the
// collection under it, as [Doc.tagInstance] finds it, such as !!str on a
// mapping.
func (d *Doc) convertsCollection() bool {
	return slices.ContainsFunc(d.walked().kinds().tags, func(tag *ast.TagNode) bool {
		in, ok := d.tagInstance(tag)

		return ok && in.construct == ConstructConvertingTag
	})
}

// madeMisread returns the problem of a call that named names, which ran,
// when the current text holds an instance of a misread kind, as
// [Doc.precedenceMisreads] and [Doc.madeMark] find them, or nil. The model
// of the calls holds each merge key as an entry of its mapping, not the
// entries that it brings in. So the check of the model does not see which
// entry go-yaml's decoder reads through the merge key. The text that the
// Apply found holds no such instance, because New refuses such a text and
// each call checks the text that it leaves. So the call made the instance.
// The problem matches [ErrEngine], and the Apply records it.
func (d *Doc) madeMisread(named string) error {
	found := d.precedenceMisreads()
	if m, ok := d.madeMark(); ok {
		found = append(found, m)
	}

	if len(found) == 0 {
		return nil
	}

	m := slices.MinFunc(found, func(a, b misreadFound) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(a.kind, b.kind))
	})
	cause := &misreadError{msg: m.edited + ", and " + d.subject() + " " + named, fix: m.fix}
	problem := d.bindSite(m.at, func(opts ...niceyaml.ErrorOption) error { return d.engineProblem(cause, opts...) })
	d.record(problem)

	return problem //nolint:wrapcheck // The binding is the problem of the edit.
}

// madeMark returns the first byte order mark of the current text that the
// tokenizer of the Source drops, as [bom.Drop] finds it. The tokenizer
// dropped each such mark of the user's text before the parse, as [New]
// reads the text. So a call moved the mark to such a place, as a removal of
// the lines before a mark at the start of a line does. The tokenizer then
// drops a byte that go-yaml read as text of the document before the call.
func (d *Doc) madeMark() (misreadFound, bool) {
	_, marks := bom.Drop(d.cur)
	if len(marks) == 0 {
		return misreadFound{}, false
	}

	at := marks[0]

	return misreadFound{
		kind: misreadMark, at: d.offsetSite(at, len(bom.Mark)), start: at, fix: markFix,
		edited: fmt.Sprintf("go-yaml reads the byte order mark at %s as text of the document, but the tokenizer "+
			"of the Source drops it %s", d.userAt(at), inEdited),
	}, true
}

// valueMisread returns the first byte order mark of the current text, the
// YAML of a Set value as [encodedDoc] reads it, that the tokenizer of the
// Source drops, as [bom.Drop] finds it. Go-yaml reads such a mark as text
// of the value. The finder of the value refuses a merge key, as
// [Doc.valueInstances] finds it, so the misreads of merge keys count as no
// instance there.
func (d *Doc) valueMisread() (misreadFound, bool) {
	_, marks := bom.Drop(d.cur)
	if len(marks) == 0 {
		return misreadFound{}, false
	}

	at := positionIn(d.cur, d.curLines, marks[0])

	return misreadFound{
		kind: misreadMark, start: marks[0],
		msg: fmt.Sprintf("go-yaml reads the byte order mark at %d:%d as text of the value", at.Line+1, at.Col+1),
		fix: markFix,
	}, true
}

// markMisread returns the first byte order mark of the user's text that
// go-yaml reads as text of the first document of its file, but that the
// tokenizer of the Source drops, as YAML does. Such a mark stands at the
// start of the file, or in the text of the document before its first
// token, after comment lines, directives, and empty lines, as
// [Doc.markPlaces] finds it. A mark of a later document is go-yaml's
// reading, and [Doc.Text] and [FileText] keep it. The misread binds at the
// mark.
func (d *Doc) markMisread() (misreadFound, bool) {
	if d.user.DocumentIndex() > 0 {
		return misreadFound{}, false
	}

	first := -1
	if prefix, _ := d.markPlaces(); len(prefix) > 0 {
		first = prefix[0]
	}

	if strings.HasSuffix(d.user.Source().Text()[:d.start], bom.Mark) {
		first = 0
	}

	if first < 0 {
		return misreadFound{}, false
	}

	return misreadFound{
		kind: misreadMark, at: d.offsetSite(first, 1), start: first,
		msg: fmt.Sprintf("go-yaml reads the byte order mark at %s as text of the document", d.userAt(first)),
		fix: markFix,
	}, true
}

// inEdited names the edited text as the place of a misread, in the words
// of a message.
const inEdited = "in the edited text"

// markFix is the detail of a problem of a byte order mark.
const markFix = "remove the byte order mark"

// userAt returns the line and the column in the user's text of the byte at
// off of the current text, as [placeText] names them. A byte order mark is
// no token, so a message names its place so.
func (d *Doc) userAt(off int) string {
	return placeText(d.userPos(off))
}

// placeText returns the line and the column of pos, as a message names a
// place, such as "3:1".
func placeText(pos position.Position) string {
	return fmt.Sprintf("%d:%d", pos.Line+1, pos.Col+1)
}
