package layermerge

import "strings"

// Text returns the YAML text of a document that holds v below preamble,
// in block style, with one entry or element on each line. An entry that a
// layer removed takes no line. A nil v holds no content.
func Text(preamble string, v *Value) string {
	var w writer

	w.sb.WriteString(preamble)
	w.document(v)

	return w.sb.String()
}

// writer writes a [Value] as the text of a YAML document, in
// block style, with one entry or element on each line.
type writer struct {
	sb strings.Builder
}

// document writes v as the content of a document. A nil v writes nothing.
func (w *writer) document(v *Value) {
	switch {
	case v == nil:
	case !v.block():
		w.sb.WriteString(v.inline(false))
		w.sb.WriteByte('\n')

	default:
		if v.tag != "" {
			w.sb.WriteString(v.tag)
			w.sb.WriteByte('\n')
		}

		w.block(v, 0, false)
	}
}

// block writes the lines of v, a mapping or a sequence that is not empty,
// at indent. With first set, the first line goes on behind what the
// current line holds, as the first entry of a mapping goes behind the "-"
// of its element. An entry that a layer removed takes no line.
func (w *writer) block(v *Value, indent int, first bool) {
	for _, entry := range v.entries {
		if entry.removed {
			continue
		}

		w.pad(indent, first)

		first = false

		w.sb.WriteString(entry.key)
		w.sb.WriteByte(':')
		w.nested(entry.value, indent+2, false)
	}

	for _, item := range v.items {
		w.pad(indent, first)

		first = false

		w.sb.WriteByte('-')
		w.nested(item, indent+2, true)
	}
}

// pad writes the indentation of a line, unless the line holds text
// already.
func (w *writer) pad(indent int, started bool) {
	if !started {
		w.sb.WriteString(strings.Repeat(" ", indent))
	}
}

// nested writes v behind the ":" of its key or the "-" of its element,
// and ends the line. A block goes on the lines below at indent. With
// compact set, v is the element of a sequence, and a block with no tag
// starts on the line itself. Any other v stands under a key, where a
// null with no text leaves the line as it is, as [Value.inline]
// describes.
func (w *writer) nested(v *Value, indent int, compact bool) {
	switch {
	case !v.block():
		if text := v.inline(!compact); text != "" {
			w.sb.WriteByte(' ')
			w.sb.WriteString(text)
		}

		w.sb.WriteByte('\n')

	case compact && v.tag == "":
		w.sb.WriteByte(' ')
		w.block(v, indent, true)

	default:
		if v.tag != "" {
			w.sb.WriteByte(' ')
			w.sb.WriteString(v.tag)
		}

		w.sb.WriteByte('\n')
		w.block(v, indent, false)
	}
}
