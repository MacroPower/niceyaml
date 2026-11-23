package edit

import (
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/bom"
)

// misreadError is what go-yaml misreads in a text. The fix says what the
// user can write so that go-yaml reads the text as YAML does.
type misreadError struct {
	msg, fix string
}

// Error returns what go-yaml misreads.
func (e *misreadError) Error() string {
	return e.msg
}

// misreadFound is a byte order mark that go-yaml reads as text, as
// [Doc.madeMark] and [Doc.valueMisread] find it.
type misreadFound struct {
	// What go-yaml misreads, in words that name the places of the user's
	// text, and in words that name the edited text.
	msg, edited string
	// What the user can write so that go-yaml reads the text as YAML does.
	fix string
	// The site where the problem of the mark binds.
	at site
}

// madeMisread returns the problem of a call that named names, which ran,
// when the current text holds a byte order mark that the tokenizer of the
// Source drops, as [Doc.madeMark] finds it, or nil. New takes each mark
// that the tokenizer drops out of the text, so the call made the mark. The
// problem matches [ErrEngine], and the Apply records it.
func (d *Doc) madeMisread(named string) error {
	m, ok := d.madeMark()
	if !ok {
		return nil
	}

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
		at: d.offsetSite(at, len(bom.Mark)), fix: markFix,
		edited: fmt.Sprintf("go-yaml reads the byte order mark at %s as text of the document, but the tokenizer "+
			"of the Source drops it %s", d.userAt(at), inEdited),
	}, true
}

// valueMisread returns the first byte order mark of the current text, the
// YAML of a Set value as [encodedDoc] reads it, that the tokenizer of the
// Source drops, as [bom.Drop] finds it. Go-yaml reads such a mark as text
// of the value.
func (d *Doc) valueMisread() (misreadFound, bool) {
	_, marks := bom.Drop(d.cur)
	if len(marks) == 0 {
		return misreadFound{}, false
	}

	at := d.curPos(marks[0])

	return misreadFound{
		msg: fmt.Sprintf("go-yaml reads the byte order mark at %s as text of the value", at.OneBased()),
		fix: markFix,
	}, true
}

// inEdited names the edited text as the place of a misread, in the words
// of a message.
const inEdited = "in the edited text"

// markFix is the detail of a problem of a byte order mark.
const markFix = "remove the byte order mark"

// userAt returns the line and the column in the user's text of the byte at
// off of the current text, as a message names a place, such as "3:1". A
// byte order mark is no token, so a message names its place so.
func (d *Doc) userAt(off int) string {
	return d.userPos(off).OneBased()
}
