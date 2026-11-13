package edit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// HeadComment returns the comment lines above the entry or the item that
// belong to it, in the order of the text. [Node.Remove] deletes these
// lines, and [Node.Move] carries them with the entry or the item. They are
// the comment lines directly above its key or its "-" that start in the
// column of that key or "-", and a blank line ends them. Each line gives
// its text without the "#" and the one space after it. So "# moved" gives
// "moved", and "#" alone gives "".
//
// The first entry of a mapping that starts on the line of the "-" of an
// item has no comment lines of its own. The lines above that "-" belong to
// the item, so HeadComment of the entry returns the lines of the item. That
// item holds the mapping in the text. A path that reaches the mapping
// through an alias or a `<<` merge key thus reads the lines of that item.
// Comment lines that reach the top of the document belong to the document.
// So HeadComment returns none for the first entry or item of the document.
//
// HeadComment is a read, as [Node.Kind] is. It returns nil whenever
// [Node.Exists] does not return true, at the root, and at a place inside a
// flow collection.
func (n *Node) HeadComment() []string {
	p, _, ok := n.read()
	if !ok || p.root || p.flow {
		return nil
	}

	owner, err := n.d.headOwner(p)
	if err != nil {
		return nil
	}

	return n.d.headLines(owner)
}

// LineComment returns the comment at the end of the line of the key or the
// "-" of the entry or the item, or "" when the line ends without one. It
// gives the text of the comment without the "#" and the one space after
// it, as [Node.HeadComment] does. A comment after the indicator of a block
// scalar counts too. A value can start on the next line, as the scalar of
// "a: &x # note" does. A comment after its anchor or its tag counts too.
// [Node.Move] carries this comment with the entry or the item.
//
// An item can hold a mapping or a sequence that starts on the line of its
// "-". The comment at the end of that line belongs to the first entry or
// item of that collection, and LineComment of the item returns it.
//
// LineComment is a read, as [Node.Kind] is. It returns "" whenever
// [Node.Exists] does not return true, at the root, and at a place inside a
// flow collection.
func (n *Node) LineComment() string {
	p, _, ok := n.read()
	if !ok || p.root || p.flow {
		return ""
	}

	spot, ok := n.d.lineSpot(n.d.lineOwner(p))
	if !ok || spot.comment < 0 {
		return ""
	}

	return commentText(n.d.cur[spot.comment:spot.end])
}

// SetHeadComment replaces the comment lines that [Node.HeadComment] reads
// with lines. It writes one comment line for each, in the column of the
// key or the "-", as "# " and its text, or as "#" alone for "". Without
// lines, SetHeadComment removes the comment lines. Each new line ends with
// the line break of the line above it, as each line that an edit adds
// does. When lines read as the comment lines that the place holds,
// SetHeadComment leaves the text as it is.
//
// A call on the first entry of a mapping that starts on the line of the
// "-" of an item writes the lines of the item, above its "-", as
// HeadComment reads them. Such a call fails as a call on that item fails.
// So it fails with [ErrProtected] for a protected item. A call on the
// first entry or item of the document writes the lines right above it, and
// the comment lines at the top stay. The new lines then reach the top of
// the document, so they belong to the document. HeadComment thus reads
// none there after the call, as after a [Node.Move] with [First].
//
// A [Func] gives a new entry its comment lines with a Set and then a
// SetHeadComment on the same Node. SetHeadComment changes no value, so the
// model of the Apply stays as it is, and Apply checks the text after the
// call as [Doc.Apply] describes. A place that does not hold a value stays
// as it is.
//
// Go-yaml's parser refuses a comment line right after the blank lines of a
// block scalar without text, as in "a: |", "", "# c", and "b: 1". So a call
// with lines fails where its lines go right after such blank lines, and the
// document is at fault for the problem.
//
// SetHeadComment fails with [ErrUsage] at the root. It also fails so for a
// line that holds a line break, or a character that YAML does not allow in
// a text. A place inside a flow collection fails with [ErrUnsupported], as
// [ConstructFlowLayout] names it. A protected value of [WithProtected], and
// a place inside one, fail with [ErrProtected]. A call fails with
// ErrUnsupported where its path reaches a construct of the Supported YAML
// section of the package documentation, as a call of [Node.SetKey] does.
func (n *Node) SetHeadComment(lines ...string) error {
	named := "sets the head comment of " + n.path.String()
	c := n.d.modelStart(n, named, false)
	err := n.setHeadComment(lines, named)

	return n.d.modelEnd(c, err, func() bool { return true })
}

// SetLineComment replaces the comment that [Node.LineComment] reads with
// text, which it writes as "# " and text. It keeps the spaces before a
// comment that it replaces, and it writes one space before a new comment.
// With "", it removes the comment and the spaces before it. That includes
// a "#" alone, which LineComment reads as "". When another text reads as
// the comment that the line holds, SetLineComment leaves the text as it
// is.
//
// A call on an item whose mapping or sequence starts on the line of its
// "-" writes the comment of the first entry or item of that collection, as
// LineComment reads it. Such a call fails as a call on that entry or item
// fails. So it fails with [ErrProtected] for a protected first entry or
// item. It fails with [ErrUnsupported] where the path of that entry or
// item reaches a construct of the Supported YAML section.
//
// Go-yaml's lexer drops a tag that ends the text without a line break, as
// in "a: !!str", but it reads the tag before a comment. A comment after
// such a tag thus changes the value that go-yaml reads. So a call with
// another text than "" fails there, and the document is at fault for the
// problem. A call with "" that removes the comment after such a tag ends
// the line with a line break, which keeps the tag.
//
// A value whose text goes on past the line of its key or its "-" leaves no
// end of that line for a comment. A plain scalar of two lines is such a
// value. LineComment reads "" there, so a call with "" leaves the text as
// it is. A call with another text fails with [ErrUnsupported] where its
// path reaches a construct of the Supported YAML section. Otherwise the
// call fails, and the document is at fault for the problem.
//
// Otherwise SetLineComment fails as [Node.SetHeadComment] fails, and it
// changes no value either.
func (n *Node) SetLineComment(text string) error {
	named := "sets the line comment of " + n.path.String()
	c := n.d.modelStart(n, named, false)
	err := n.setLineComment(text, named)

	return n.d.modelEnd(c, err, func() bool { return true })
}

// setHeadComment writes lines as the head comment of the place of n, as
// [Node.SetHeadComment] describes. Named names the call.
func (n *Node) setHeadComment(lines []string, named string) error {
	d := n.d

	p, err := n.commentPlace(lines, named)
	if err != nil || p == nil {
		return err
	}

	owner, err := d.headOwner(p)
	if err != nil {
		// The ":" of an explicit key comes before its value on its line, and
		// the call refuses such an entry.
		refusal := n.commentReach(p, p, named)
		if refusal != nil {
			return refusal
		}

		return d.bug(n, err)
	}

	err = n.ownerProtection(p, owner, named)
	if err != nil {
		return err
	}

	if slices.Equal(d.headLines(owner), lines) {
		return nil
	}

	err = n.commentReach(p, owner, named)
	if err != nil {
		return err
	}

	start, _ := d.startOf(owner)
	keyLine := d.lineOf(start)
	first := keyLine

	if len(d.headLines(owner)) > 0 {
		first = d.blockRegion(owner).first
	}

	if d.emptyScalarAbove(first) {
		return d.fail(n, "%s %s, whose comment lines go after the blank lines of a block scalar without text, "+
			"and go-yaml's parser refuses a comment line there", d.subject(), named)
	}

	brk := d.breakBefore(first)
	pad := strings.Repeat(" ", d.column(start))

	var text strings.Builder

	for _, l := range lines {
		text.WriteString(pad + commentLine(l) + brk)
	}

	return n.writeComment(owner, named, d.lineStart(first), d.lineStart(keyLine), text.String())
}

// setLineComment writes text as the line comment of the place of n, as
// [Node.SetLineComment] describes. Named names the call.
func (n *Node) setLineComment(text, named string) error {
	d := n.d

	p, err := n.commentPlace([]string{text}, named)
	if err != nil || p == nil {
		return err
	}

	owner := d.lineOwner(p)

	err = n.ownerProtection(p, owner, named)
	if err != nil {
		return err
	}

	spot, ok := d.lineSpot(owner)
	if !ok {
		// LineComment reads "" here, so "" keeps the text.
		if text == "" {
			return nil
		}

		err = n.commentReach(p, owner, named)
		if err != nil {
			return err
		}

		return d.fail(n, "%s %s, whose value goes on past the line of its key or its \"-\", "+
			"so no comment can end that line", d.subject(), named)
	}

	// An empty text removes a comment that reads as "" too.
	kept := spot.comment < 0 && text == ""
	if spot.comment >= 0 && text != "" {
		kept = commentText(d.cur[spot.comment:spot.end]) == text
	}

	if kept {
		return nil
	}

	err = n.commentReach(p, owner, named)
	if err != nil {
		return err
	}

	switch {
	case spot.dropped:
		// Go-yaml's lexer reads a tag before a comment, so a comment there
		// changes the value that go-yaml reads.
		return d.fail(n, "%s %s, whose tag ends the text without a line break, "+
			"and go-yaml drops such a tag, so a comment after it changes the value that go-yaml reads",
			d.subject(), named)

	case text == "" && spot.bare:
		// Go-yaml's lexer reads the tag before a line break, as it reads it
		// before the comment, so the line break keeps the value.
		return n.writeComment(owner, named, spot.at, spot.end, d.breakBefore(d.lineOf(spot.at)))
	case text == "":
		return n.writeComment(owner, named, spot.at, spot.end, "")
	case spot.comment >= 0:
		return n.writeComment(owner, named, spot.comment, spot.end, commentLine(text))
	default:
		return n.writeComment(owner, named, spot.at, spot.end, " "+commentLine(text))
	}
}

// commentPlace returns the place of n for a call that writes a comment of
// texts, which named names. It returns the problem of a node that cannot
// take the call and the problem of the root. It also returns the problem of
// a text that the call cannot write, of a protected place, and of a place
// inside a flow collection. The call does nothing at a place that does not
// hold a value. CommentPlace then returns a nil place and the problem of a
// step of the path, as [Node.stepProblem] gives it.
func (n *Node) commentPlace(texts []string, named string) (*place, error) {
	d := n.d
	verb := strings.TrimSuffix(named, " "+n.path.String())

	p, err := n.place(useEdit, verb)
	if err != nil {
		return nil, err
	}

	// The arguments decide these problems whatever the text holds, so they
	// come first, also for a place that does not hold a value.
	if p.root {
		return nil, d.failAs(ErrUsage, n, "%s sets a comment of the root of the document, "+
			"whose comments belong to the document", d.subject())
	}

	for _, t := range texts {
		if strings.ContainsAny(t, "\r\n") {
			return nil, d.failAs(ErrUsage, n, "%s %s to %q, which holds a line break", d.subject(), named, t)
		}

		if what, ok := unprintableIn(t); ok {
			return nil, d.failAs(ErrUsage, n, "%s %s to %q, which holds %s and YAML does not allow that in a text",
				d.subject(), named, t, what)
		}
	}

	if !p.exists {
		return nil, n.stepProblem()
	}

	err = n.commentProtection(p, named)
	if err != nil {
		return nil, err
	}

	if p.flow {
		at := d.nodeSite(p.container, false)

		return nil, d.refusal(&instance{
			construct: ConstructFlowLayout,
			at:        at,
			reach:     "lies inside a flow collection",
			object:    "write a comment inside such a collection",
			fix:       "write the collection in block style, or set the comment of the value that holds it",
		}, named)
	}

	return p, nil
}

// commentProtection returns the problem of a call that named names, which
// writes a comment of p, when p is a protected value or lies inside one.
func (n *Node) commentProtection(p *place, named string) error {
	if why := n.d.protection(p, false); why != "" {
		return n.d.failAs(ErrProtected, n, "%s %s, which %s", n.d.subject(), named, why)
	}

	return nil
}

// ownerProtection returns the problem of a call that named names, which
// writes a comment of owner for p, the place of n, when owner is another
// place and a protected value or a place inside one. [Node.commentPlace]
// checks p itself.
func (n *Node) ownerProtection(p, owner *place, named string) error {
	if owner == p {
		return nil
	}

	if why := n.d.protection(owner, false); why != "" {
		return n.d.failAs(ErrProtected, n, "%s %s, whose comment belongs to %s, which %s",
			n.d.subject(), named, owner.path, why)
	}

	return nil
}

// commentReach returns the problem of a call through n that writes a
// comment of owner for p, the place of n, which named names, when it
// reaches a zone of the Apply, as [Doc.reachProblem] tells. The call
// reaches the path of n. When owner is another place, the call reaches
// the path of owner too, so a call through an item reaches the first entry
// whose comment it writes.
func (n *Node) commentReach(p, owner *place, named string) error {
	err := n.d.reachProblem(reachCall{kind: callComment, path: n.path, hidden: n.hidden, named: named})
	if err != nil || owner == p {
		return err
	}

	return n.d.reachProblem(reachCall{kind: callComment, path: owner.path, hidden: n.hidden, named: named})
}

// writeComment replaces the bytes [start, end) of the current text with
// text, a comment of p that the call through n, which named names, writes.
func (n *Node) writeComment(p *place, named string, start, end int, text string) error {
	d := n.d

	d.recordEdit(p, named, "")

	s := d.placeOfSite(p)
	origin := d.addNote(note{detail: d.subject(), call: named, path: s.path, at: s.at, hasPath: true, located: true})

	err := d.apply(edit{start: start, end: end, text: text, origins: written(text, origin)})
	if err != nil {
		return d.bug(n, err)
	}

	return nil
}

// headOwner returns the place that owns the comment lines above p, an
// entry or an item of a block collection. An entry or an item that starts
// on the line of the "-" of an item has no comment lines of its own. The
// item that holds its collection in the text owns them. A path can reach
// that collection through an alias or a `<<` merge key. So the parent of
// the path of p need not be that item. Besides the "-" of an item, only
// the ":" of an explicit key comes before a block collection on its line.
// A call refuses such a key. So headOwner returns a problem for a
// collection that no item of a block sequence holds. A call that reaches
// no explicit key reports that problem as a fault of the engine.
func (d *Doc) headOwner(p *place) (*place, error) {
	for d.sharesLine(p) {
		item, ok := d.holderOf(p.container)
		if !ok || item.seq == nil || item.seq.IsFlowStyle {
			return nil, fmt.Errorf("%s starts after other text on its line, "+
				"but no item of a block sequence holds its collection", p.path)
		}

		p = d.itemPlace(p, item)
	}

	return p, nil
}

// itemPlace returns the place of item, the item of a block sequence that
// holds the collection of p, as [Doc.holderOf] gives it. The parent of the
// path of p selects the item when the path reaches the collection through
// it. The place then reads as that path reads it. Otherwise the place
// takes the path of the item in the text, as [Doc.textPath] gives it.
func (d *Doc) itemPlace(p, item *place) *place {
	if parent, ok := p.path.Parent(); ok {
		q, err := d.resolveFrom(p, parent)
		if err == nil && q.exists && q.seq == item.seq && q.index == item.index {
			return q
		}
	}

	item.container, item.exists = item.seq, true
	item.path = d.textPath(item.value)

	return item
}

// emptyScalarAbove reports whether one or more blank lines lie right above
// line, below the header of a block scalar that holds no text. Go-yaml's
// parser refuses a comment line in any column right after the blank lines
// of such a scalar. It refuses "a: |", "", "# c", and "b: 1". A line of
// spaces holds text when it has more spaces than the explicit indent of its
// scalar. The scalar of "a: |1" and "  " holds text, so go-yaml accepts a
// comment line after it.
func (d *Doc) emptyScalarAbove(line int) bool {
	above := line - 1
	for above >= 0 && d.isBlankLine(above) {
		above--
	}

	return above < line-1 && d.emptyScalarAt(above)
}

// emptyScalarAt reports whether line holds the header of a block scalar
// that holds no text, as [Doc.emptyScalarAbove] reads it.
func (d *Doc) emptyScalarAt(line int) bool {
	if line < 0 {
		return false
	}

	for _, lit := range d.literals() {
		if hs, ok := d.tokenSpan(lit.Start); ok && d.lineOf(hs.start) == line {
			return lit.Value == nil || lit.Value.Value == ""
		}
	}

	return false
}

// headLines returns the text of each comment line above p that belongs to
// it, as [Doc.blockRegion] gives them, as [Node.HeadComment] reads them.
func (d *Doc) headLines(p *place) []string {
	start, _ := d.startOf(p)
	keyLine := d.lineOf(start)

	var out []string

	for l := d.blockRegion(p).first; l < keyLine; l++ {
		out = append(out, commentText(strings.TrimLeft(d.lineText(l), " \t")))
	}

	return out
}

// lineOwner returns the place that owns the comment at the end of the line
// of the key or the "-" of p, an entry or an item of a block collection.
// An item whose block collection starts on the line of its "-" gives that
// line to the first entry or item of the collection, at any depth.
func (d *Doc) lineOwner(p *place) *place {
	for p.value != nil && isBlockCollection(p.value) {
		start, ok := d.startOf(p)
		if !ok || d.lineOf(d.contentStart(p)) != d.lineOf(start) {
			return p
		}

		child, ok := d.firstChild(p)
		if !ok {
			return p
		}

		p = child
	}

	return p
}

// firstChild returns the place of the first entry or item of the block
// collection of p.
func (d *Doc) firstChild(p *place) (*place, bool) {
	switch c := astnode.Content(p.value).(type) {
	case *ast.SequenceNode:
		if len(c.Values) == 0 {
			return nil, false
		}

		return &place{
			container: p.value, seq: c, index: 0, value: c.Values[0], exists: true, path: p.path.Index(0),
		}, true

	case *ast.MappingNode:
		if len(c.Values) == 0 {
			return nil, false
		}

		return d.childEntry(p, c.Values[0]), true

	case *ast.MappingValueNode:
		return d.childEntry(p, c), true
	}

	return nil, false
}

// childEntry returns the place of e, an entry of the block mapping of p.
func (d *Doc) childEntry(p *place, e *ast.MappingValueNode) *place {
	q := entryPlace(p.value, e)
	q.path = p.path

	if name, ok := d.node.DocumentResolver().KeyName(e.Key); ok && !astnode.IsMergeKey(e.Key) {
		q.path = p.path.Child(name)
	}

	return q
}

// commentSpot is the end of the line of the key or the "-" of a place,
// where its line comment goes.
type commentSpot struct {
	// The offset past the last text before the comment.
	at int
	// The offset where the comment starts, or -1 for a line without one.
	comment int
	// The offset of the end of the line, before its line break.
	end int
	// Whether the text ends at the end of the line, and the line ends with
	// the tag of a value without content before the comment. Go-yaml's
	// lexer drops such a tag when no comment follows it, as
	// [Doc.pastDropped] tells.
	bare bool
	// Whether go-yaml's lexer dropped the text before at, a tag that ends
	// the text.
	dropped bool
}

// lineSpot returns the end of the line of the key or the "-" of p, an
// entry or an item of a block collection, as [Node.LineComment] reads it.
// The comment goes past the value when the value ends on that line. When
// the content of the value starts below that line, the comment goes past
// the anchor and the tag on the line, for a scalar and a collection alike.
// It goes past the indicator of a block scalar too. LineSpot reports false
// for a value whose text goes on past the line otherwise, such as a plain
// scalar of two lines. It also reports false for the line of a block
// collection that starts on the line of the "-" of its item.
func (d *Doc) lineSpot(p *place) (commentSpot, bool) {
	start, ok := d.startOf(p)
	if !ok {
		return commentSpot{}, false
	}

	keyLine := d.lineOf(start)
	vs, ve := d.valueStart(p), d.valueEnd(p)

	var (
		at      int
		dropped bool
	)

	switch content := astnode.Content(p.value); {
	case p.value == nil || isImplicitNull(p.value):
		// Go-yaml's lexer drops a tag that ends the text without a final
		// line break, as in "a: !!str". The value holds that text, as
		// [Doc.valueEnd] gives it, so the comment goes past it.
		at = max(d.afterProps(p), d.valueEnd(p))
		dropped = at > d.tokensEnd(p)

	case d.lineOf(d.contentStart(p)) > keyLine:
		at = d.afterProps(p)

	case isBlockCollection(p.value):
		return commentSpot{}, false

	case d.lineOf(max(vs, ve-1)) == keyLine:
		at = ve

	default:
		lit, isLit := content.(*ast.LiteralNode)
		if !isLit || lit.Start == nil {
			return commentSpot{}, false
		}

		hs, ok := d.tokenSpan(lit.Start)
		if !ok {
			return commentSpot{}, false
		}

		at = hs.end
	}

	if d.lineOf(at) != keyLine {
		return commentSpot{}, false
	}

	end := d.lineEnd(keyLine)
	spot := commentSpot{at: at, comment: d.trailingComment(at), end: end, dropped: dropped}
	spot.bare = end == len(d.cur) && at > d.indicatorEnd(p) && at == d.afterProps(p)

	if spot.comment < 0 && strings.TrimSpace(d.cur[at:end]) != "" {
		return commentSpot{}, false
	}

	return spot, true
}

// commentText returns the text of comment, a comment from its "#", without
// the "#" and the one space after it.
func commentText(comment string) string {
	text := strings.TrimPrefix(comment, "#")

	return strings.TrimPrefix(text, " ")
}

// commentLine returns the comment that writes text, as [commentText] reads
// it back.
func commentLine(text string) string {
	if text == "" {
		return "#"
	}

	return "# " + text
}
