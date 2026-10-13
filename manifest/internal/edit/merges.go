package edit

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// The most levels of `<<` merge keys that a view follows.
const maxMergeDepth = 32

// mergeView is what one mapping that merges or repeats keys reads, as the
// decode reads it. An entry is the place of its key in the user's text,
// which a rename keeps.
type mergeView struct {
	// The entry that each key name reads.
	reads map[string]position.Position
	// The entries that the mapping hides, with the name of each.
	hidden map[position.Position]string
	// The text of the first merge key entry, as "<<: *summer", or "" for a
	// mapping that merges nothing.
	merge string
	// The value of that merge key, as "*summer".
	via string
}

// editRecord is an edit of the step running, as a message names it.
type editRecord struct {
	// What the edit does, as "renames "closes" to "close"".
	desc string
	path paths.Path
	at   position.Range
}

// mergeViews returns the view of each mapping that merges or repeats a
// key, or that keep names. A mapping that merges goes by the entry of its
// first merge key, and any other by the entry of its first key.
func (d *Doc) mergeViews(keep map[position.Position]*mergeView) map[position.Position]*mergeView {
	views := make(map[position.Position]*mergeView)
	res := d.node.Resolver()
	inMapping := make(map[*ast.MappingValueNode]bool)

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		if m, ok := n.(*ast.MappingNode); ok {
			for _, v := range m.Values {
				inMapping[v] = true
			}
		}

		return true
	})

	astnode.Inspect(d.node.AST(), func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.MappingNode:
			d.addView(views, keep, res, x)
		case *ast.MappingValueNode:
			if !inMapping[x] {
				d.addView(views, keep, res, x)
			}
		}

		return true
	})

	return views
}

// addView adds the view of the mapping m to views when m merges or
// repeats a key, or when keep names it.
func (d *Doc) addView(views, keep map[position.Position]*mergeView, res *paths.Resolver, m ast.Node) {
	entries, _ := astnode.Entries(m)
	if len(entries) == 0 {
		return
	}

	candidates := make(map[string][]*ast.MappingValueNode)
	repeats := false

	var merge *ast.MappingValueNode

	for _, e := range entries {
		if astnode.IsMergeKey(e.Key) {
			if merge == nil {
				merge = e
			}

			continue
		}

		name, ok := res.KeyName(e.Key)
		if !ok {
			continue
		}

		repeats = repeats || len(candidates[name]) > 0
		candidates[name] = append(candidates[name], e)
	}

	// No path of a step reaches a merge key. The first merge key thus names
	// its mapping after a step removes the entries around it.
	id := d.entryID(entries[0])
	if merge != nil {
		id = d.entryID(merge)
	}

	if merge == nil && !repeats && keep[id] == nil {
		return
	}

	if merge != nil && !mergedEntries(res, m, candidates, 0) {
		return
	}

	view := &mergeView{
		reads:  make(map[string]position.Position),
		hidden: make(map[position.Position]string),
	}

	for name, list := range candidates {
		read, _, err := findChild(res, astnode.Content(m), name)
		if err != nil || read == nil {
			return
		}

		view.reads[name] = d.entryID(read)

		for _, c := range list {
			if c != read {
				view.hidden[d.entryID(c)] = name
			}
		}
	}

	if merge != nil {
		if s, ok := d.nodeSpan(merge); ok {
			view.merge = strings.TrimSpace(d.cur[s.start:s.end])
		}

		if s, ok := d.nodeSpan(merge.Value); ok {
			view.via = strings.TrimSpace(d.cur[s.start:s.end])
		}
	}

	views[id] = view
}

// mergedEntries adds the entries that the `<<` merge keys of m bring in to
// candidates, by name, and reports false when a merge key does not
// resolve.
func mergedEntries(res *paths.Resolver, m ast.Node, candidates map[string][]*ast.MappingValueNode, depth int) bool {
	if depth > maxMergeDepth {
		return false
	}

	sources, err := res.MergeSources(m)
	if err != nil {
		return false
	}

	for _, src := range sources {
		entries, _ := astnode.Entries(astnode.Content(src))

		for _, e := range entries {
			if astnode.IsMergeKey(e.Key) {
				continue
			}

			if name, ok := res.KeyName(e.Key); ok {
				candidates[name] = append(candidates[name], e)
			}
		}

		if !mergedEntries(res, astnode.Content(src), candidates, depth+1) {
			return false
		}
	}

	return true
}

// entryID returns the place in the user's text of the key of e, which a
// rename keeps. A key that a step created takes a place of its own.
func (d *Doc) entryID(e *ast.MappingValueNode) position.Position {
	s, ok := d.nodeSpan(e.Key)
	if !ok || s.start >= len(d.origin) {
		return position.New(-1, 0)
	}

	o := d.origin[s.start]
	if o >= 0 {
		return d.userPos(s.start)
	}

	// A renamed key keeps the place of the key it replaced, and a created
	// key takes the place of its note alone.
	n := d.notes[-1-o]
	if n.located && n.detail == "" {
		return n.at.Start
	}

	return position.New(-2, o)
}

// recordEdit records an edit of the step running, which a message about
// what a mapping reads names. The edit is at the place p of h, and at the
// key of its entry when it renames, removes, or moves the entry.
func (d *Doc) recordEdit(h *Handle, p *place, desc string) {
	start, ok := d.startOf(p)
	if !ok {
		start = d.valueStart(p)
	}

	end := d.valueEnd(p)
	if ks, _, ok := d.keySpan(p); ok {
		end = ks.end
	}

	at := d.userRange(start, max(start, end))
	if start < len(d.origin) && d.origin[start] < 0 {
		if n := d.notes[-1-d.origin[start]]; n.located {
			at = n.at
		}
	}

	path := h.path
	if p.entry != nil && !strings.HasPrefix(desc, "sets ") {
		path = path.Key()
	}

	d.lastEdit = &editRecord{desc: desc, path: d.userPath(path, at.Start), at: at}
}

// checkMerges returns the problem of a step after which a mapping that
// merges or repeats a key reads another entry: one that it hid before the
// step, or another one in place of an entry that it now hides.
func (d *Doc) checkMerges() error {
	before := d.merges
	d.merges = nil

	if len(before) == 0 || d.lastEdit == nil {
		return nil
	}

	after := d.mergeViews(before)

	for _, id := range slices.SortedFunc(maps.Keys(before), comparePositions) {
		b, a := before[id], after[id]
		if a == nil {
			continue
		}

		for _, name := range slices.Sorted(maps.Keys(a.reads)) {
			if _, hid := b.hidden[a.reads[name]]; hid {
				return d.mergeProblem(a, name)
			}
		}

		for _, name := range slices.Sorted(maps.Keys(b.reads)) {
			if _, hid := a.hidden[b.reads[name]]; hid {
				return d.mergeProblem(a, name)
			}
		}
	}

	return nil
}

// mergeProblem returns the problem of the last edit of the step, after
// which the mapping of view reads another entry under name.
func (d *Doc) mergeProblem(view *mergeView, name string) error {
	e := d.lastEdit

	msg := fmt.Sprintf("%s %s, but the mapping also writes %q", d.label, e.desc, name)
	detail := errors.New("remove one of the two")

	if view.merge != "" {
		msg = fmt.Sprintf("%s %s, but %s also brings in %q", d.label, e.desc, view.merge, name)
		detail = fmt.Errorf("write the value out in place of %s", view.via)
	}

	err := d.user.Bind(niceyaml.NewError(msg, niceyaml.AtPath(e.path), niceyaml.AtRange(e.at),
		niceyaml.WithDetails(detail)))
	d.record(err)

	return err
}

// comparePositions orders two positions by line, then by column.
func comparePositions(a, b position.Position) int {
	return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
}
