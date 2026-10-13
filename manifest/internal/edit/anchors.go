package edit

import (
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// keptAnchor is an anchor that an edit takes away while an alias outside
// the edit still reads it.
type keptAnchor struct {
	// The name of the anchor.
	name string
	// The text of the anchored value on one line, for a scalar or a flow
	// collection.
	inline *run
	// The lines of a block collection, each with its indentation relative
	// to the first.
	lines []line
}

// anchorsIn returns the anchors that node defines and that an alias outside
// node reads, outermost first, with the text of each anchored value.
func (d *Doc) anchorsIn(node ast.Node) []keptAnchor {
	if node == nil {
		return nil
	}

	outer, ok := d.nodeSpan(node)
	if !ok {
		return nil
	}

	var kept []keptAnchor

	astnode.Inspect(node, func(n ast.Node) bool {
		a, ok := n.(*ast.AnchorNode)
		if !ok {
			return true
		}

		name, _ := astnode.Name(a.Name)
		if name == "" || !d.aliasOutside(name, outer) {
			return true
		}

		if k, ok := d.capture(name, a.Value); ok {
			kept = append(kept, k)
		}

		return false
	})

	return kept
}

// aliasOutside reports whether an alias of name lies outside the bytes of
// s.
func (d *Doc) aliasOutside(name string, s span) bool {
	found := false

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		a, ok := n.(*ast.AliasNode)
		if !ok || found {
			return !found
		}

		if alias, _ := astnode.Name(a.Value); alias != name {
			return true
		}

		as, ok := d.nodeSpan(a)
		if ok && (as.start < s.start || as.start >= s.end) {
			found = true
		}

		return true
	})

	return found
}

// capture returns the text of value, the value of the anchor name.
func (d *Doc) capture(name string, value ast.Node) (keptAnchor, bool) {
	s, ok := d.nodeSpan(value)
	if !ok {
		return keptAnchor{}, false
	}

	k := keptAnchor{name: name}

	// A block collection keeps its lines, even when it fits on one.
	if !isBlockCollection(value) && d.lineOf(s.start) == d.lineOf(max(s.start, s.end-1)) {
		r := d.keptRun(s.start, s.end)
		k.inline = &r

		return k, true
	}

	first := d.lineOf(s.start)
	last := d.lineOf(s.end - 1)
	base := d.column(s.start)

	for l := first; l <= last; l++ {
		start := d.lineStart(l)
		end := d.lineEnd(l)

		if l == first {
			start = s.start
		}

		if l == last {
			end = min(end, s.end)
		}

		text := d.cur[start:end]
		trimmed := strings.TrimLeft(text, " ")
		indent := d.column(start) + len(text) - len(trimmed) - base

		k.lines = append(k.lines, line{
			text:   d.keptRun(end-len(trimmed), end),
			indent: indent,
		})
	}

	return k, true
}

// keepAnchors writes the value of each anchor of kept, with the anchor, at
// the first alias that reads it. It skips each anchor that the document
// still defines before that alias. The aliases thus go on reading the
// value after an edit removes the anchor or moves it past them.
func (d *Doc) keepAnchors(h *Handle, kept []keptAnchor) error {
	for _, k := range kept {
		alias := d.firstAlias(k.name)
		if alias == nil {
			continue
		}

		as, ok := d.nodeSpan(alias)
		if !ok || d.definesBefore(k.name, as.start) {
			continue
		}

		origin := originOf(d.keptRun(as.start, as.end))
		text := newRun("&"+k.name+" ", origin)

		if k.inline != nil {
			text = appendRun(text, *k.inline)
		} else {
			text = newRun("&"+k.name, origin)

			line := d.lineOf(as.start)
			col := d.indentOf(line) + d.stepOf(d.pathAt(as.start))
			brk := d.breakBefore(line + 1)
			text = appendRun(text, prefixRun(brk, d.joinLines(k.lines, col, brk), origin))
		}

		err := d.apply(edit{start: as.start, end: as.end, text: text.text, origins: text.origins})
		if err != nil {
			return d.bug(h, err)
		}
	}

	return nil
}

// pathAt returns the path of the value at off, or the root.
func (d *Doc) pathAt(off int) paths.Path {
	path, ok := d.node.PathAt(d.positionOf(off))
	if !ok {
		return paths.Doc()
	}

	return path
}

// definesBefore reports whether the document defines the anchor name at
// an offset before off.
func (d *Doc) definesBefore(name string, off int) bool {
	found := false

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		if a, ok := n.(*ast.AnchorNode); ok {
			if got, _ := astnode.Name(a.Name); got == name {
				s, ok := d.propsSpan(a)
				found = ok && s.start < off
			}
		}

		return !found
	})

	return found
}

// firstAlias returns the first alias of name in document order, or nil.
func (d *Doc) firstAlias(name string) *ast.AliasNode {
	var (
		first *ast.AliasNode
		at    int
	)

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		a, ok := n.(*ast.AliasNode)
		if !ok {
			return true
		}

		if got, _ := astnode.Name(a.Value); got != name {
			return true
		}

		s, ok := d.nodeSpan(a)
		if ok && (first == nil || s.start < at) {
			first, at = a, s.start
		}

		return true
	})

	return first
}
