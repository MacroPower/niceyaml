package edit

import (
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// defaultStep is the indent step of a document with no nested mapping.
const defaultStep = 2

// region is the lines of an entry or an item of a block collection, with
// the comments that belong to it.
type region struct {
	// The bytes of the lines, each with its line break.
	start int
	end   int
	// The first and the last line.
	first int
	last  int
}

// blockRegion returns the lines of p, an entry or an item of a block
// collection, with its comments. The comment lines directly above it that
// start in its column belong to it, unless they reach the top of the
// document. So does each comment line after its value that starts deeper
// than its key before any other content.
func (d *Doc) blockRegion(p *place) region {
	start, _ := d.startOf(p)
	keyLine := d.lineOf(start)
	keyCol := d.column(start)

	first := keyLine

	for l := keyLine - 1; l >= 0; l-- {
		col, ok := d.commentColumn(l)
		if !ok || col != keyCol {
			break
		}

		first = l
	}

	if first < keyLine && d.topComments(first) {
		first = keyLine
	}

	end := d.valueEnd(p)
	last := d.lineOf(max(start, end-1))
	last = max(last, keyLine)

	if d.keepsTrailingLines(p) {
		for last+1 < len(d.curLines) && d.isBlankLine(last+1) && d.lineStart(last+1) < len(d.cur) {
			last++
		}
	}

	for l := last + 1; l < len(d.curLines) && d.lineStart(l) < len(d.cur); l++ {
		if d.isBlankLine(l) {
			continue
		}

		col, ok := d.commentColumn(l)
		if !ok || col <= keyCol {
			break
		}

		last = l
	}

	return region{start: d.lineStart(first), end: d.lineStart(last + 1), first: first, last: last}
}

// topComments reports whether the comment lines from line on reach the top
// of the document, so they belong to the document and stay at its top.
func (d *Doc) topComments(line int) bool {
	for l := line - 1; l >= 0; l-- {
		text := strings.TrimSpace(d.lineText(l))

		switch {
		case text == "" || strings.HasPrefix(text, "#"):
			continue
		case text == "---" || strings.HasPrefix(text, "--- ") || strings.HasPrefix(text, "%"):
			return true
		default:
			return false
		}
	}

	return true
}

// keepsTrailingLines reports whether the value of p is a block scalar with
// keep chomping, whose string holds the blank lines after its text.
func (d *Doc) keepsTrailingLines(p *place) bool {
	if p.value == nil {
		return false
	}

	tk := astnode.Content(p.value).GetToken()

	return tk != nil && strings.Contains(tk.Value, "+") &&
		(strings.HasPrefix(tk.Value, "|") || strings.HasPrefix(tk.Value, ">"))
}

// stepOf returns the indent step of the block mapping at path: the
// distance from the key of the entry that holds it to its keys. For a
// mapping that is an item, it takes the step of the nearest mapping above
// it that is the value of an entry, and the step of the document at the
// root.
func (d *Doc) stepOf(path paths.Path) int {
	for cur := path; !cur.IsRoot(); {
		last, _ := cur.Last()
		parent, _ := cur.Parent()

		if last.Kind == paths.SelectorChild {
			if step, ok := d.entryStep(cur); ok {
				return step
			}
		}

		cur = parent
	}

	return d.docStep()
}

// entryStep returns the distance from the key of the entry at path to the
// keys of the block mapping it holds.
func (d *Doc) entryStep(path paths.Path) (int, bool) {
	p, err := d.resolve(path)
	if err != nil || !p.exists || p.entry == nil {
		return 0, false
	}

	entries, ok := astnode.Entries(astnode.Content(p.value))
	if !ok || len(entries) == 0 || isFlow(astnode.Content(p.value)) {
		return 0, false
	}

	keySpan, ok := d.nodeSpan(p.entry.Key)
	if !ok {
		return 0, false
	}

	firstSpan, ok := d.nodeSpan(entries[0].Key)
	if !ok {
		return 0, false
	}

	step := d.column(firstSpan.start) - d.column(keySpan.start)
	if step <= 0 {
		return 0, false
	}

	return step, true
}

// docStep returns the step of the first block mapping in the document that
// is the value of an entry, or two spaces when the document has none.
func (d *Doc) docStep() int {
	step := 0

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		if step > 0 {
			return false
		}

		e, ok := n.(*ast.MappingValueNode)
		if !ok {
			return true
		}

		entries, ok := astnode.Entries(astnode.Content(e.Value))
		if !ok || len(entries) == 0 || isFlow(astnode.Content(e.Value)) {
			return true
		}

		keySpan, ok1 := d.nodeSpan(e.Key)
		firstSpan, ok2 := d.nodeSpan(entries[0].Key)

		if ok1 && ok2 {
			if s := d.column(firstSpan.start) - d.column(keySpan.start); s > 0 {
				step = s
			}
		}

		return true
	})

	if step == 0 {
		return defaultStep
	}

	return step
}

// sequenceIndent reports whether a block sequence that the rewrite
// creates under a key indents its "-" one step deeper than the key, as
// the first block sequence under a key in the document does. Without one,
// it does.
func (d *Doc) sequenceIndent() bool {
	indent, found := true, false

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		if found {
			return false
		}

		e, ok := n.(*ast.MappingValueNode)
		if !ok {
			return true
		}

		seq, ok := astnode.Content(e.Value).(*ast.SequenceNode)
		if !ok || seq.IsFlowStyle || seq.Start == nil {
			return true
		}

		keySpan, ok1 := d.nodeSpan(e.Key)
		dash, ok2 := d.tokenSpan(seq.Start)

		if ok1 && ok2 {
			indent, found = d.column(dash.start) > d.column(keySpan.start), true
		}

		return true
	})

	return indent
}

// breakBefore returns the line break that a line added at the start of
// line ends with: the break of the line above it, or of the first line at
// the top. It returns "\n" where no line has a break.
func (d *Doc) breakBefore(line int) string {
	if line > 0 {
		if b := d.lineBreak(line - 1); b != "" {
			return b
		}
	}

	for l := range d.curLines {
		if b := d.lineBreak(l); b != "" {
			return b
		}
	}

	return "\n"
}

// indentLines returns lines, each after col spaces, joined with brk.
func indentLines(lines []string, col int, brk string) string {
	pad := strings.Repeat(" ", col)

	var b strings.Builder

	for i, l := range lines {
		if i > 0 {
			b.WriteString(brk)
		}

		if l != "" {
			b.WriteString(pad)
		}

		b.WriteString(l)
	}

	return b.String()
}
