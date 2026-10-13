package edit

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// place is the structure of the current text at one path.
type place struct {
	// The container holds the place: a mapping or a sequence, under its
	// anchors and tags, or nil for the root.
	container ast.Node
	// The entry of a mapping that holds the value, or nil.
	entry *ast.MappingValueNode
	// The sequence that holds the value as an item, or nil.
	seq *ast.SequenceNode
	// The value as the text writes it, with its anchor or tag, or nil when
	// the place holds none.
	value ast.Node
	// The first alias, or the value of the first `<<` merge key, that the
	// path went through, or nil.
	via ast.Node
	// The path of the place, from `$`.
	path paths.Path
	// The index of the item in seq.
	index int
	// The number of selectors of path that resolved, for a place that holds
	// no value.
	found int
	// The place is the root of the document.
	root bool
	// The place holds a value.
	exists bool
	// The path reached the place through an alias or a `<<` merge key.
	shared bool
	// The container is a flow collection, or lies inside one.
	flow bool
}

// errMultiple, errKeyPath, and errUnresolved are the problems of a path
// that At cannot take.
var (
	errMultiple   = errors.New("path selects more than one value")
	errKeyPath    = errors.New("path selects a key")
	errUnresolved = errors.New("path goes through an alias that does not resolve")
)

// resolve returns the place at path, a `$` path of names and indexes.
func (d *Doc) resolve(path paths.Path) (*place, error) {
	p := &place{path: path, root: path.IsRoot(), index: -1}
	res := d.node.Resolver()
	cur := d.node.AST()

	if path.IsRoot() {
		p.value, p.exists = cur, cur != nil

		return p, nil
	}

	for sel := range path.Selectors() {
		content := astnode.Content(cur)

		if _, ok := content.(*ast.AliasNode); ok {
			target, err := res.Deref(content)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", errUnresolved, err)
			}

			p.shared = true
			content = astnode.Content(target)

			if p.via == nil {
				p.via = astnode.Content(cur)
			}
		}

		p.container = content
		p.entry, p.seq, p.index = nil, nil, -1

		if isFlow(content) {
			p.flow = true
		}

		switch sel.Kind {
		case paths.SelectorChild:
			entry, merged, err := findChild(res, content, sel.Name)
			if err != nil {
				return nil, err
			}

			if entry == nil {
				return p, nil
			}

			if merged != nil {
				p.shared = true
				p.container = merged

				if p.via == nil {
					p.via = mergeValue(content)
				}
			}

			p.entry = entry
			cur = entry.Value

		case paths.SelectorIndex:
			seq, ok := content.(*ast.SequenceNode)
			if !ok || sel.Index >= len(seq.Values) {
				p.seq = seq

				if ok {
					p.index = sel.Index
				}

				return p, nil
			}

			p.seq, p.index = seq, sel.Index
			cur = seq.Values[sel.Index]

		case paths.SelectorKey:
			return nil, errKeyPath

		default:
			return nil, errMultiple
		}

		p.found++
	}

	p.value, p.exists = cur, true

	return p, nil
}

// findChild returns the entry of the mapping node whose key reads name,
// the later one when the mapping writes the key twice. Without such an
// entry, it returns the entry in a mapping that a `<<` merge key brings in,
// with that mapping.
func findChild(res *paths.Resolver, node ast.Node, name string) (*ast.MappingValueNode, ast.Node, error) {
	entries, ok := astnode.Entries(node)
	if !ok {
		return nil, nil, nil
	}

	var hit *ast.MappingValueNode

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			continue
		}

		if key, ok := res.KeyName(e.Key); ok && key == name {
			hit = e
		}
	}

	if hit != nil {
		return hit, nil, nil
	}

	sources, err := res.MergeSources(node)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errUnresolved, err)
	}

	for _, src := range slices.Backward(sources) {
		m := astnode.Content(src)

		entry, merged, err := findChild(res, m, name)
		if err != nil {
			return nil, nil, err
		}

		if entry != nil {
			if merged == nil {
				merged = m
			}

			return entry, merged, nil
		}
	}

	return nil, nil, nil
}

// mergeValue returns the value of the last `<<` merge key of the mapping
// node, or nil.
func mergeValue(node ast.Node) ast.Node {
	entries, _ := astnode.Entries(node)

	var value ast.Node

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			value = e.Value
		}
	}

	return value
}

// isFlow reports whether node is a flow mapping or a flow sequence.
func isFlow(node ast.Node) bool {
	switch n := node.(type) {
	case *ast.MappingNode:
		return n.IsFlowStyle
	case *ast.SequenceNode:
		return n.IsFlowStyle
	case *ast.MappingValueNode:
		return n.IsFlowStyle
	default:
		return false
	}
}

// isBlockCollection reports whether node holds a block mapping or a block
// sequence under its anchors and tags.
func isBlockCollection(node ast.Node) bool {
	switch n := astnode.Content(node).(type) {
	case *ast.MappingNode:
		return !n.IsFlowStyle
	case *ast.MappingValueNode:
		return !n.IsFlowStyle
	case *ast.SequenceNode:
		return !n.IsFlowStyle
	default:
		return false
	}
}

// isNull reports whether node holds a null under its anchors and tags.
func isNull(node ast.Node) bool {
	c := astnode.Content(node)

	return astnode.IsNil(c) || c.Type() == ast.NullType
}

// isImplicitNull reports whether node is the null that the parser writes
// for an entry or an item with no value.
func isImplicitNull(node ast.Node) bool {
	if astnode.IsNil(node) {
		return true
	}

	tk := node.GetToken()

	return node.Type() == ast.NullType && tk != nil && tk.Type == token.ImplicitNullType
}

// span is a range of bytes [start, end) of the current text.
type span struct {
	start int
	end   int
}

// tokenSpan returns the bytes of the content of tk, without the spaces
// around it.
func (d *Doc) tokenSpan(tk *token.Token) (span, bool) {
	ranges := d.src.Lines().ContentRanges(tk)
	if len(ranges) == 0 {
		return span{}, false
	}

	return span{start: d.offset(ranges[0].Start), end: d.offset(ranges[len(ranges)-1].End)}, true
}

// nodeSpan returns the bytes of node, from its first token through its
// last, comments left out. It reports false for a node with no token, such
// as the null of an entry with no value.
func (d *Doc) nodeSpan(node ast.Node) (span, bool) {
	var (
		s     span
		found bool
	)

	add := func(tk *token.Token) {
		if tk == nil || tk.Position == nil || tk.Type == token.CommentType || tk.Type == token.ImplicitNullType {
			return
		}

		ts, ok := d.tokenSpan(tk)
		if !ok {
			return
		}

		if !found {
			s, found = ts, true

			return
		}

		s.start = min(s.start, ts.start)
		s.end = max(s.end, ts.end)
	}

	astnode.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.CommentGroupNode); ok {
			return false
		}

		add(n.GetToken())

		switch x := n.(type) {
		case *ast.MappingNode:
			add(x.Start)
			add(x.End)

		case *ast.SequenceNode:
			add(x.Start)
			add(x.End)

			for _, e := range x.Entries {
				add(e.Start)
			}

		case *ast.MappingValueNode:
			add(x.Start)
		case *ast.AnchorNode:
			add(x.Start)
		case *ast.AliasNode:
			add(x.Start)
		case *ast.TagNode:
			add(x.Start)
		}

		return true
	})

	return s, found
}

// lineOf returns the line of the current text that holds the byte at off.
func (d *Doc) lineOf(off int) int {
	return positionIn(d.cur, d.curLines, off).Line
}

// lineStart returns the offset of line, or the length of the text past the
// last line.
func (d *Doc) lineStart(line int) int {
	if line >= len(d.curLines) {
		return len(d.cur)
	}

	return d.curLines[line]
}

// lineEnd returns the offset of the line break that ends line, or the
// length of the text for the last line without one.
func (d *Doc) lineEnd(line int) int {
	end := d.lineStart(line + 1)
	if end > 0 && end <= len(d.cur) && end > d.lineStart(line) && d.cur[end-1] == '\n' {
		end--
		if end > d.lineStart(line) && d.cur[end-1] == '\r' {
			end--
		}
	}

	return end
}

// lineText returns the text of line without its line break.
func (d *Doc) lineText(line int) string {
	if line < 0 || line >= len(d.curLines) {
		return ""
	}

	return d.cur[d.lineStart(line):d.lineEnd(line)]
}

// lineBreak returns the line break that ends line, or "" for a last line
// without one.
func (d *Doc) lineBreak(line int) string {
	return d.cur[d.lineEnd(line):d.lineStart(line+1)]
}

// isBlankLine reports whether line holds only spaces and tabs.
func (d *Doc) isBlankLine(line int) bool {
	return strings.TrimSpace(d.lineText(line)) == ""
}

// commentColumn returns the column where line starts a comment, and true,
// for a line that holds a comment alone.
func (d *Doc) commentColumn(line int) (int, bool) {
	text := d.lineText(line)
	trimmed := strings.TrimLeft(text, " \t")

	if !strings.HasPrefix(trimmed, "#") {
		return 0, false
	}

	return len(text) - len(trimmed), true
}

// indentOf returns the number of spaces that start line.
func (d *Doc) indentOf(line int) int {
	text := d.lineText(line)

	return len(text) - len(strings.TrimLeft(text, " "))
}

// column returns the column of the byte at off in its line.
func (d *Doc) column(off int) int {
	return positionIn(d.cur, d.curLines, off).Col
}

// trailingComment returns the start of the comment that ends the line of
// off, past off, or -1 when the line has no such comment.
func (d *Doc) trailingComment(off int) int {
	line := d.lineOf(off)
	end := d.lineEnd(line)

	for i := off; i < end; i++ {
		if d.cur[i] == '#' && (i == 0 || d.cur[i-1] == ' ' || d.cur[i-1] == '\t') {
			return i
		}

		if d.cur[i] != ' ' && d.cur[i] != '\t' {
			return -1
		}
	}

	return -1
}

// startOf returns the offset where the entry or the item of p starts: its
// key, its explicit key indicator, or its "-".
func (d *Doc) startOf(p *place) (int, bool) {
	switch {
	case p.entry != nil:
		s, ok := d.nodeSpan(p.entry.Key)
		if !ok {
			return 0, false
		}

		if start := d.explicitKeyStart(s.start); start >= 0 {
			return start, true
		}

		return s.start, true

	case p.seq != nil && !p.seq.IsFlowStyle && p.index < len(p.seq.Entries):
		if e := p.seq.Entries[p.index]; e.Start != nil {
			s, ok := d.tokenSpan(e.Start)

			return s.start, ok
		}
	}

	if p.value != nil {
		s, ok := d.nodeSpan(p.value)

		return s.start, ok
	}

	return 0, false
}

// explicitKeyStart returns the offset of the "?" of an explicit key that
// starts at off, or -1 when the key has none.
func (d *Doc) explicitKeyStart(off int) int {
	i := off - 1
	for i >= 0 && (d.cur[i] == ' ' || d.cur[i] == '\t') {
		i--
	}

	if i >= 0 && d.cur[i] == '?' {
		return i
	}

	return -1
}

// valueEnd returns the offset where the value of p ends, past its last
// token, or past the ":" of an entry with no value.
func (d *Doc) valueEnd(p *place) int {
	if p.value != nil && !isImplicitNull(p.value) {
		if s, ok := d.nodeSpan(p.value); ok {
			return s.end
		}
	}

	if p.entry != nil && p.entry.Start != nil {
		if s, ok := d.tokenSpan(p.entry.Start); ok {
			return s.end
		}
	}

	if p.seq != nil && p.index < len(p.seq.Entries) && p.seq.Entries[p.index].Start != nil {
		if s, ok := d.tokenSpan(p.seq.Entries[p.index].Start); ok {
			return s.end
		}
	}

	return 0
}

// valueStart returns the offset where the value of p starts. For an entry
// with no value, it returns the offset past its ":".
func (d *Doc) valueStart(p *place) int {
	if p.value != nil && !isImplicitNull(p.value) {
		if s, ok := d.nodeSpan(p.value); ok {
			return s.start
		}
	}

	return d.valueEnd(p)
}

// keySpan returns the bytes of the text of the key of the entry of p,
// without its anchors and tags.
func (d *Doc) keySpan(p *place) (span, *token.Token, bool) {
	tk := astnode.KeyToken(p.entry)
	if tk == nil {
		return span{}, nil, false
	}

	s, ok := d.tokenSpan(tk)

	return s, tk, ok
}

// keyColumn returns the column of the key or the "-" of p.
func (d *Doc) keyColumn(p *place) int {
	start, ok := d.startOf(p)
	if !ok {
		return 0
	}

	return d.column(start)
}

// positionOf returns the position in the current text of the byte at off.
func (d *Doc) positionOf(off int) position.Position {
	return positionIn(d.cur, d.curLines, off)
}
