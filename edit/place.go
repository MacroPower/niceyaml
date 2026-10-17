package edit

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/internal/pathshook"
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
	// the place does not hold a value.
	value ast.Node
	// Each entry that a later entry of its key hides on the path to the
	// place, the outermost first, as [Doc.walk] read them, or nil. The path
	// of a place that is such an entry or lies inside one selects another
	// place.
	via []hiddenEntry
	// The path of the place, from `$`.
	path paths.Path
	// The index of the item in seq.
	index int
	// The number of selectors of path that resolved, for a place that does
	// not hold a value.
	found int
	// Whether the place is the root of the document.
	root bool
	// Whether the place holds a value.
	exists bool
	// Whether the path reached the place through an alias or a `<<` merge
	// key.
	shared bool
	// Whether the place is an entry that a `<<` merge key brings in.
	merged bool
	// Whether a value on the path, the root and the value of the place
	// included, has an anchor.
	anchored bool
	// Whether the container is a flow collection, or lies inside one.
	flow bool
	// The offset where the value ends, for a block scalar whose text the
	// parse reads past its own bytes, or 0 for the end of its text.
	end int
}

// errMultiple and errKeyPath are problems of a path that At cannot take.
// Each problem of a path reads as what the path does. A message thus puts
// it after the path or after "which", as in "which selects a key".
var (
	errMultiple = errors.New("selects more than one value")
	errKeyPath  = errors.New("selects a key")
)

// aliasError is the problem of a path through an alias that the text of
// the edit does not resolve. It reads as what the path does, as the other
// problems of a path do.
type aliasError struct {
	// The error of [paths.Resolver.Deref] for the alias.
	err error
	// The alias in the current text.
	node *ast.AliasNode
	// The alias as the text writes it, such as *base, or "" for an alias
	// without a name.
	alias string
	// The words that name the `<<` merge key whose value holds the alias,
	// for an alias of a reference document there, as [Doc.mergeWords] gives
	// them, or "".
	merge string
	// Whether the lookup of a value of the text reads the alias: the alias
	// itself, or a mapping whose `<<` merge key reads it.
	held bool
}

// unresolved returns the problem of a path through alias around err, the
// error of Deref for alias. The text of the edit does not resolve alias.
func unresolved(alias *ast.AliasNode, err error) *aliasError {
	e := &aliasError{err: err, node: alias}
	if name, ok := astnode.Name(alias.Value); ok {
		e.alias = "*" + name
	}

	return e
}

// heldBy returns err, marked as the error of the lookup of a value of the
// text that reads the alias, when err is an [*aliasError].
func heldBy(err error) error {
	if e, ok := errors.AsType[*aliasError](err); ok {
		e.held = true
	}

	return err
}

// Error names the alias once, as in "goes through *base, an alias of a
// reference document". A decode reads an alias of a reference document,
// but the text of the edit does not hold the anchor, so the problem says
// so. At a `<<` merge key, it names the merge key and the mapping whose
// names the reference document decides. It then reads as in "goes through
// <<: *base at 2:3, a merge key that reads the reference document
// base.yaml, so that document decides which names $.m reads".
func (e *aliasError) Error() string {
	switch {
	case e.merge != "":
		return "goes through " + e.merge
	case e.alias == "":
		return "goes through " + aliasKind(e.err)
	}

	return "goes through " + e.alias + ", " + aliasKind(e.err)
}

// aliasProblem returns the problem of a path through the alias that err
// names, as [unresolved] gives it for the error of [paths.Resolver.Deref]
// at the alias. Err is the error of a lookup of the current text at an
// alias that does not resolve, as paths.Resolver gives it. The resolver
// names the alias that stops the lookup, and niceyaml's root package binds
// the error of the path there. An alias of a reference document in the
// value of a `<<` merge key lets the reference document decide which names
// the mapping of the merge key reads. The problem then names the merge
// key, as [Doc.mergeWords] gives it. AliasProblem reports false for any
// other error.
func (d *Doc) aliasProblem(err error) (*aliasError, bool) {
	alias, ok := pathshook.UnresolvedAlias(err)
	if !ok || !d.inText(alias) {
		return nil, false
	}

	_, derr := d.node.Resolver().Deref(alias)
	if derr == nil {
		return nil, false
	}

	e := unresolved(alias, derr)

	if merge, ok := d.mergeOf(alias); ok && errors.Is(derr, paths.ErrReferenceAlias) {
		e.merge = d.mergeWords(merge, alias)
	}

	return e, true
}

// mergeOf returns the `<<` merge entry of the current text whose value is
// alias, or a sequence that holds alias as an item, and reports false for
// another alias.
func (d *Doc) mergeOf(alias *ast.AliasNode) (*ast.MappingValueNode, bool) {
	t := d.tree()

	up := t.parent[alias]
	if seq, ok := up.(*ast.SequenceNode); ok {
		up = t.parent[seq]
	}

	e, ok := up.(*ast.MappingValueNode)

	return e, ok && astnode.IsMergeKey(e.Key)
}

// mergeWords returns the words of a message after "goes through" that
// name e, a `<<` merge entry of the current text whose value holds alias,
// an alias of a reference document. They read as in "<<: *r at 2:3, a
// merge key that reads the reference document r.yaml, so that document
// decides which names $.m reads". The line and the column are those of the
// merge key in the user's text.
func (d *Doc) mergeWords(e *ast.MappingValueNode, alias *ast.AliasNode) string {
	pos := d.nodeSite(e.Key, true).at.Start
	name, _ := astnode.Name(alias.Value)

	return fmt.Sprintf("%s at %d:%d, a merge key that reads %s, so that document decides which names %s reads",
		mergeText(keptMerge{m: d.mappingOf(e), entry: e}), pos.Line+1, pos.Col+1, d.referenceWords(name),
		d.ownerPath(e.Key))
}

// ownerPath returns the path of node, a node of the current text, as
// [Doc.textPath] spells it, or the path of the mapping of the outermost `<<`
// merge key whose value holds node, because no path names a merge entry.
func (d *Doc) ownerPath(node ast.Node) paths.Path {
	holder := node

	for _, up := range d.ancestors(node) {
		if e, ok := up.(*ast.MappingValueNode); ok && astnode.IsMergeKey(e.Key) {
			holder = d.mappingOf(e)
		}
	}

	return d.textPath(holder)
}

// Unwrap returns the error of Deref.
func (e *aliasError) Unwrap() error {
	return e.err
}

// aliasKind returns the words that name an alias whose error is err in a
// message, such as "an alias of a reference document".
func aliasKind(err error) string {
	if errors.Is(err, paths.ErrReferenceAlias) {
		return "an alias of a reference document"
	}

	return "an alias that does not resolve"
}

// resolve returns the place at path, a `$` path of names and indexes.
func (d *Doc) resolve(path paths.Path) (*place, error) {
	return d.walk(path, nil, nil)
}

// resolveFrom returns the place at path as from, a place of the text,
// reads it. The path of from can read through an entry that a later entry
// of its key hides. A path that leads through that entry as the path of
// from does reads through it too, as [Doc.nodePlace] reads the place of a
// node there. Any other path, and any path for a nil from, resolves as
// [Doc.resolve] resolves it.
func (d *Doc) resolveFrom(from *place, path paths.Path) (*place, error) {
	if from == nil {
		return d.resolve(path)
	}

	return d.walk(path, through(from.via, from.path, path), nil)
}

// walk returns the place at path as resolve does. Each entry of hidden
// names a selector of path by its depth, and that selector selects the
// entry in place of the entry that its name selects. When visit is set,
// walk calls it at each value on the way, the root first and the value of
// the place last, with the place so far and the value as the text writes
// it. The place then holds the number of selectors of path that reach the
// value, and the entry or the item of that value. When visit returns
// false, walk stops and returns the place so far.
func (d *Doc) walk(
	path paths.Path,
	hidden []hiddenEntry,
	visit func(p *place, cur ast.Node) bool,
) (*place, error) {
	p := &place{path: path, root: path.IsRoot(), index: -1}
	res := d.node.Resolver()
	finder := res.EntryFinder()
	cur := d.node.AST()

	if path.IsRoot() {
		p.value, p.exists, p.anchored = cur, cur != nil, hasAnchor(cur)

		if visit != nil {
			visit(p, cur)
		}

		return p, nil
	}

	for sel := range path.Selectors() {
		p.anchored = p.anchored || hasAnchor(cur)

		if visit != nil && !visit(p, cur) {
			return p, nil
		}

		content, aliases, err := d.readValue(cur)
		if err != nil {
			return nil, heldBy(err)
		}

		if len(aliases) > 0 {
			p.shared = true
			p.flow = d.inFlow(content)
		}

		p.container = content
		p.entry, p.seq, p.index = nil, nil, -1

		if isFlow(content) {
			p.flow = true
		}

		switch sel.Kind {
		case paths.SelectorChild:
			var (
				found child
				err   error
			)

			if len(p.via) < len(hidden) && p.found == hidden[len(p.via)].depth-1 {
				found.entry, p.via = d.entryOfPlace(content, hidden[len(p.via)].at), hidden[:len(p.via)+1]
			} else {
				found, err = d.childOf(finder, content, sel.Name)
			}

			if err != nil {
				return nil, heldBy(err)
			}

			if found.entry == nil {
				return p, nil
			}

			p.merged = found.merged != nil
			if p.merged {
				p.shared = true
				p.container = found.merged
				p.flow = d.inFlow(found.merged)
			}

			p.entry = found.entry
			cur = found.entry.Value

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
	p.anchored = p.anchored || hasAnchor(cur)

	if visit != nil {
		visit(p, cur)
	}

	return p, nil
}

// hasAnchor reports whether node has an anchor, under its tags.
func hasAnchor(node ast.Node) bool {
	for !astnode.IsNil(node) {
		switch n := node.(type) {
		case *ast.AnchorNode:
			return true
		case *ast.TagNode:
			node = n.Value
		default:
			return false
		}
	}

	return false
}

// prefixOf returns the path of the first n selectors of path.
func prefixOf(path paths.Path, n int) paths.Path {
	return appendSelectors(paths.Doc(), slices.Collect(path.Selectors())[:n])
}

// child is the entry that a path through a name selects in a mapping.
type child struct {
	// The entry, or nil when the path selects none.
	entry *ast.MappingValueNode
	// The mapping that holds the entry when a `<<` merge key brings it in,
	// or nil for an entry of the mapping itself.
	merged ast.Node
}

// keyNamer returns the name of a key of a mapping that a lookup compares,
// and reports false for a key without one. [Doc.decodedKeys] gives the key
// that a decode reads. It does not depend on the mapping that a lookup
// reads the key in, so a key that a `<<` merge key brings in reads as at
// its own place.
type keyNamer func(key ast.Node) (string, bool)

// decodedKeys returns the [keyNamer] that names each key of the current
// text by the key that a decode reads, as [Doc.keyReader] gives it.
func (d *Doc) decodedKeys() keyNamer {
	return d.keyReader().Key
}

// keyReader returns the reader of the keys of the current text, which
// names each key as niceyaml's decode of the text reads it. The reader of
// each parse decodes each key once, and the readers of the parses of an
// Apply decode each key without an alias once, as [mapkey.Plain] keeps it.
func (d *Doc) keyReader() *mapkey.Keys {
	t := d.parseIndex()
	if t.keys == nil {
		t.keys = hook.Keys(d.node)

		if d.plain != nil {
			t.keys.Share(d.plain)
		}
	}

	return t.keys
}

// enclosedAliases returns the aliases of the current text that lie inside
// the value of the anchor that each refers to. A decode reads each of them
// as null, and the decoder refuses a `<<` merge key that merges one, so no
// read of the text follows one to its anchor.
func (d *Doc) enclosedAliases() map[*ast.AliasNode]bool {
	t := d.parseIndex()
	if t.enclosed == nil {
		t.enclosed = make(map[*ast.AliasNode]bool)

		for _, a := range aliasing.StateOf(d.node).Facts().Aliases {
			if a.Inside {
				t.enclosed[a.Node] = true
			}
		}
	}

	return t.enclosed
}

// readValue returns the content of node, a value as the text writes it, as
// a decode reads it, with each alias that it reads on the way. The content
// is the mapping, the sequence, or the scalar under the anchors, the tags,
// and the aliases of node. A decode reads a tag that converts a collection
// as a scalar, as [astnode.Converted] reads such a tag. So readValue stops
// at the tag, and [readEntries] reads no entries in it. An anchor can hold
// an alias under a tag, as in "&b !t *a", which go-yaml takes. An alias of
// b then reads the value of a. A decode reads an alias inside the value of
// its own anchor as null, so readValue returns such an alias as its
// content. That content holds no entries or items. An alias of an anchor
// that holds such an alias under a tag, as "&x !t *x" does, reads that
// null too, as the graph of the text reads it. ReadValue returns the
// problem of another alias that the text does not resolve, as
// [unresolved] gives it.
func (d *Doc) readValue(node ast.Node) (ast.Node, []ast.Node, error) {
	res := d.node.Resolver()
	content := astnode.Converted(node)

	var (
		aliases []ast.Node
		problem error
	)

	for {
		alias, ok := content.(*ast.AliasNode)
		if !ok || slices.Contains(aliases, ast.Node(alias)) {
			return content, aliases, nil
		}

		if d.enclosedAliases()[alias] {
			return alias, aliases, nil
		}

		target, err := res.Deref(alias)
		if err == nil {
			aliases = append(aliases, alias)
			content = astnode.Converted(target)

			continue
		}

		// Deref reads through the tags under the anchor of the alias, and the
		// alias reads the value of its anchor, as [graph.value] reads it. The
		// lookup fails with the problem of the first alias whose Deref failed
		// when the aliases after it do not reach a value.
		if problem == nil {
			problem = unresolved(alias, err)
		}

		anchor, err := res.Anchor(alias)
		if err != nil {
			return nil, nil, problem
		}

		aliases = append(aliases, alias)
		content = astnode.Converted(anchor)
	}
}

// readEntries returns the entries of content, a value as [Doc.readValue]
// reads it, and reports false when content does not hold a mapping. A tag
// that converts a collection holds a scalar, which has no entries.
func readEntries(content ast.Node) ([]*ast.MappingValueNode, bool) {
	switch n := content.(type) {
	case *ast.MappingNode:
		return n.Values, true
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{n}, true
	default:
		return nil, false
	}
}

// childOf returns the entry that a path through name selects in the
// mapping content, as finder selects it with the rule of
// [paths.Resolver.Entry]. A path of an edit never selects a `<<` merge key
// itself, as [Node.At] describes. For an entry that a merge key brings in,
// childOf also returns the mapping that holds the entry. A lookup that an
// alias stops, such as an alias of a reference document at a merge key,
// returns the problem of that alias, as [Doc.aliasProblem] gives it.
func (d *Doc) childOf(finder *paths.EntryFinder, content ast.Node, name string) (child, error) {
	entries, ok := readEntries(content)
	if !ok {
		return child{}, nil
	}

	// The finder reads a mapping of one entry that the user wrote without
	// braces in a flow sequence, as in "[a: 1]", as a mapping that holds it.
	mapping := content
	if e, ok := content.(*ast.MappingValueNode); ok {
		mapping = &ast.MappingNode{BaseNode: &ast.BaseNode{}, Values: []*ast.MappingValueNode{e}}
	}

	node, err := finder.Entry(mapping, name)

	switch {
	case errors.Is(err, paths.ErrNotFound):
		return child{}, nil
	case err != nil:
		if alias, ok := d.aliasProblem(err); ok {
			return child{}, alias
		}

		return child{}, err //nolint:wrapcheck // The walk binds the error.
	}

	entry, ok := node.(*ast.MappingValueNode)

	switch {
	case !ok || astnode.IsMergeKey(entry.Key):
		return child{}, nil
	case slices.Contains(entries, entry):
		return child{entry: entry}, nil
	}

	return child{entry: entry, merged: d.tree().parent[entry]}, nil
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

// inFlow reports whether node is a flow collection or lies inside one in
// the syntax tree of the current text.
func (d *Doc) inFlow(node ast.Node) bool {
	return isFlow(node) || slices.ContainsFunc(d.ancestors(node), isFlow)
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

// entryPlace returns the place of e, an entry of the mapping content.
func entryPlace(content ast.Node, e *ast.MappingValueNode) *place {
	return &place{container: content, entry: e, value: e.Value, exists: true}
}

// isCollection reports whether node holds a mapping or a sequence under its
// anchors and tags.
func isCollection(node ast.Node) bool {
	switch astnode.Content(node).(type) {
	case *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode:
		return true
	default:
		return false
	}
}

// isImplicitNull reports whether node is the null that the parser writes
// for an entry or an item without a value.
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
// last, without comments. It reports false for a node without a token,
// such as the null of an entry without a value. A node whose text ends
// with a block scalar ends where the text of the scalar ends, as
// [Doc.scalarEnd] tells. The span depends only on the node and the text,
// and the text changes only with a parse, so the index of the parse keeps
// each span that it read.
func (d *Doc) nodeSpan(node ast.Node) (span, bool) {
	if d.node == nil {
		return d.readSpan(node)
	}

	t := d.parseIndex()
	if r, ok := t.spans[node]; ok {
		return r.s, r.ok
	}

	s, ok := d.readSpan(node)

	if t.spans == nil {
		t.spans = make(map[ast.Node]spanOf)
	}

	t.spans[node] = spanOf{s: s, ok: ok}

	return s, ok
}

// valueSpan returns the bytes of n as [Doc.nodeSpan] gives them. A value
// of the current text without a token there, such as the null that
// go-yaml's parser reads for an entry without a value, holds no bytes. It
// lies where the parser places its token. So the null of "b: !!str", whose
// tag go-yaml's lexer drops at the end of a text without a line break,
// lies at the tag. A value that the syntax tree of the text does not hold,
// such as a value of a reference document, lies nowhere in the text, as
// [Doc.inText] tells.
func (d *Doc) valueSpan(n ast.Node) (span, bool) {
	if s, ok := d.nodeSpan(n); ok || astnode.IsNil(n) || !d.inText(n) {
		return s, ok
	}

	tk := n.GetToken()
	if tk == nil || tk.Position == nil {
		return span{}, false
	}

	at := d.offset(position.NewFromToken(tk))

	return span{start: at, end: at}, true
}

// readSpan reads the span of node in the current text, as [Doc.nodeSpan]
// gives it.
func (d *Doc) readSpan(node ast.Node) (span, bool) {
	first, last, ok := outerSpans(node, d.tokenSpan)
	if !ok {
		return span{}, false
	}

	s := span{start: min(first.start, last.start), end: max(first.end, last.end)}

	if lit := endScalar(node); lit != nil {
		s.end = d.scalarEnd(lit, s.end)
	}

	return s, true
}

// scalarEnd returns the end of the text of the block scalar lit, whose
// last token ends at end. Each line of the scalar after its header holds
// text through its end, so the spaces and tabs after that token on its line
// belong to the scalar too. The parser reads a line of spaces alone after
// that line as text of the scalar when the line holds more spaces than the
// lines of the scalar indent by. [Doc.scalarIndent] tells that indent. The
// text then ends with the last such line, before its line break.
func (d *Doc) scalarEnd(lit *ast.LiteralNode, end int) int {
	if end <= 0 {
		return end
	}

	last := d.lineOf(end - 1)

	if hs, ok := d.tokenSpan(lit.Start); ok && d.lineOf(hs.start) < last &&
		strings.Trim(d.cur[end:d.lineEnd(last)], " \t") == "" {
		end = d.lineEnd(last)
	}

	indent := d.scalarIndent(lit, last)
	if indent < 0 {
		return end
	}

	for l := last + 1; l < len(d.curLines) && d.lineStart(l) < len(d.cur) && d.isBlankLine(l); l++ {
		if len(d.lineText(l)) > indent {
			end = d.lineEnd(l)
		}
	}

	return end
}

// scalarIndent returns the number of spaces that the lines of the block
// scalar lit indent by, whose last token ends on the line last, or -1 when
// the text does not tell. The indentation indicator gives the indent past
// the key or the "-" that holds the scalar, and otherwise the first line
// of the scalar that is not blank gives it.
func (d *Doc) scalarIndent(lit *ast.LiteralNode, last int) int {
	if lit.Start == nil {
		return -1
	}

	hs, ok := d.tokenSpan(lit.Start)
	if !ok {
		return -1
	}

	if digit, _ := indentDigit(lit.Start.Value); digit > 0 {
		if col, ok := d.holderColumn(lit); ok {
			return col + digit
		}

		return -1
	}

	for l := d.lineOf(hs.start) + 1; l <= last; l++ {
		if !d.isBlankLine(l) {
			return d.indentOf(l)
		}
	}

	return -1
}

// holderColumn returns the column of the key or the "-" that holds node,
// past the anchors and the tags around node, or 0 for the root. It reports
// false for another holder, such as a flow collection.
func (d *Doc) holderColumn(node ast.Node) (int, bool) {
	t := d.tree()

	for {
		up, ok := t.parent[node]
		if !ok {
			return 0, true
		}

		switch x := up.(type) {
		case *ast.AnchorNode, *ast.TagNode:
			node = up

			continue

		case *ast.MappingValueNode:
			first, _, ok := outerSpans(x.Key, d.tokenSpan)
			if !ok {
				return 0, false
			}

			return d.column(first.start), true

		case *ast.SequenceNode:
			s, ok := d.tokenSpan(x.Start)
			if !ok || x.IsFlowStyle {
				return 0, false
			}

			return d.column(s.start), true
		}

		return 0, false
	}
}

// outerSpans returns the span that spanOf gives the first token of node
// that has one, and the span that it gives the last such token. The tokens
// that [eachToken] visits go in the order of their positions. Tokens do
// not overlap, so the two spans bound the spans of the tokens between
// them. The function does not read the spans of the tokens between them.
// The time that it takes thus does not grow with the length of the lines
// of node, even when spanOf reads the whole line of a token. It reports
// false when no token of node has a span.
func outerSpans[S any](node ast.Node, spanOf func(*token.Token) (S, bool)) (S, S, bool) {
	var (
		tokens      []*token.Token
		first, last S
	)

	eachToken(node, func(tk *token.Token) {
		tokens = append(tokens, tk)
	})

	slices.SortStableFunc(tokens, func(a, b *token.Token) int {
		return cmp.Or(cmp.Compare(a.Position.Line, b.Position.Line), cmp.Compare(a.Position.Column, b.Position.Column))
	})

	i := slices.IndexFunc(tokens, func(tk *token.Token) bool {
		var ok bool

		first, ok = spanOf(tk)

		return ok
	})
	if i < 0 {
		return first, last, false
	}

	for j := len(tokens) - 1; j > i; j-- {
		if s, ok := spanOf(tokens[j]); ok {
			return first, s, true
		}
	}

	return first, first, true
}

// eachToken calls fn with each token of node from its first token through
// its last, without comments and without the null of an entry without a
// value.
func eachToken(node ast.Node, fn func(*token.Token)) {
	add := func(tk *token.Token) {
		if tk == nil || tk.Position == nil || tk.Type == token.CommentType || tk.Type == token.ImplicitNullType {
			return
		}

		fn(tk)
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
	start, end := d.lineStart(line), d.lineStart(line+1)
	if end > start && isBreak(d.cur, end-1) {
		end--
		if d.cur[end] == '\n' && end > start && d.cur[end-1] == '\r' {
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
// for a line that holds only a comment.
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
// off, past off, or -1 when the line does not have such a comment.
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
// key, or its "-". The key of an explicit key starts at its "?".
func (d *Doc) startOf(p *place) (int, bool) {
	switch {
	case p.entry != nil:
		s, ok := d.nodeSpan(p.entry.Key)

		return s.start, ok

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
// starts at off, or -1 when the key does not have one.
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
// token, or past the ":" of an entry without a value, as [Doc.colonEnd]
// gives it. The text after it that go-yaml's lexer dropped belongs to the
// value, as [Doc.pastDropped] tells.
func (d *Doc) valueEnd(p *place) int {
	return d.pastDropped(d.tokensEnd(p))
}

// pastDropped returns the end of the last text of the current text when no
// token holds any of the text from end on, or else end. Go-yaml's lexer
// drops a tag that ends a text without a final line break, as in
// "a: !!str". The value whose tokens end at end holds that text, so an edit
// of the value writes over it.
func (d *Doc) pastDropped(end int) int {
	last := len(strings.TrimRight(d.cur, " \t\r\n"))

	lines := d.src.Lines()

	for i := end; i < last; i++ {
		if strings.IndexByte(" \t\r\n", d.cur[i]) < 0 && lines.TokenAt(positionIn(d.cur, d.curLines, i)) != nil {
			return end
		}
	}

	return max(end, last)
}

// tokensEnd returns the offset where the tokens of the value of p end, as
// [Doc.valueEnd] reads them.
func (d *Doc) tokensEnd(p *place) int {
	if p.end > 0 {
		return p.end
	}

	if p.value != nil && !isImplicitNull(p.value) {
		if s, ok := d.nodeSpan(p.value); ok {
			return s.end
		}
	}

	if p.entry != nil {
		if end, _, ok := d.colonEnd(p.entry); ok {
			return end
		}
	}

	if p.seq != nil && p.index < len(p.seq.Entries) && p.seq.Entries[p.index].Start != nil {
		if s, ok := d.tokenSpan(p.seq.Entries[p.index].Start); ok {
			return s.end
		}
	}

	return 0
}

// colonEnd returns the offset past the ":" of e, an entry of the current
// text. A key of a flow mapping can lack the ":", as in "{a, b: 1}", and so
// can an explicit key, as in "? Key". The entry then holds null. ColonEnd
// reports false for its second result then, and the offset lies past the
// key, where a value of the entry goes.
func (d *Doc) colonEnd(e *ast.MappingValueNode) (int, bool, bool) {
	if e.Start != nil && e.Start.Type == token.MappingValueType {
		s, ok := d.tokenSpan(e.Start)

		return s.end, true, ok
	}

	s, ok := d.nodeSpan(e.Key)

	return s.end, false, ok
}

// valueStart returns the offset where the value of p starts. For an entry
// without a value, it returns the offset past its ":".
func (d *Doc) valueStart(p *place) int {
	if p.value != nil && !isImplicitNull(p.value) {
		if s, ok := d.nodeSpan(p.value); ok {
			return s.start
		}
	}

	return d.tokensEnd(p)
}

// contentStart returns the offset where the content of the value of p
// starts, past the anchor and the tag in front of it.
func (d *Doc) contentStart(p *place) int {
	if p.value != nil && !isImplicitNull(p.value) {
		if s, ok := d.nodeSpan(astnode.Content(p.value)); ok {
			return s.start
		}
	}

	return d.valueStart(p)
}

// keySpan returns the bytes of the text of the key of the entry of p,
// without its anchors and tags.
func (d *Doc) keySpan(p *place) (span, *token.Token, bool) {
	tk := astnode.KeyToken(p.entry)
	if tk == nil {
		return span{}, nil, false
	}

	// The token of an alias is its "*", so an alias key spans the whole
	// alias. An edit of the key thus writes over its name too. The token of
	// a block scalar is its header, so such a key spans its lines too.
	switch key := astnode.Content(p.entry.Key).(type) {
	case *ast.AliasNode, *ast.LiteralNode:
		s, ok := d.nodeSpan(key)

		return s, tk, ok
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
