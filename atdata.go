package niceyaml

import (
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/yamlfield"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// AtData is an [ErrorOption] that locates an error at the value the names
// lead to in the data that the binding [Node] decodes to. The names read
// from that Node, as an `@` path does. A check of decoded values names
// what it finds by the member names and the indices of that data. It does
// not know how the document spells a key. A path matches a key by its
// source text instead. [AtPath] of those names thus misses a key that the
// decoder respells, such as the key 0x10 of the member 16. Binding writes
// the path as the document spells it:
//
//	niceyaml.NewError("name is empty", niceyaml.AtData("ports", strconv.Itoa(k), "name"))
//	// cfg.yaml:3:11: $.ports.0x10.name: name is empty
//
// An empty list of names leads to the whole of that data. AtData replaces
// a path that an earlier option set, and a later AtPath replaces the
// names.
//
// The names are those of the data from Decode[any] of the Node. A string
// key gives its text. Any other key gives its Go value as [fmt.Sprint]
// prints it, so 0x10 is 16, 3.10 is 3.1, and True is true. An element has
// its index in decimal digits, as [strconv.Itoa] writes it. A check that
// reports JSON Pointers splits each pointer into its names and unescapes
// `~1` and `~0` in each.
//
// The self-validation step of a decode reads the names from the typed
// value that the check saw instead. That step runs each [SelfValidator],
// each [SelfValidatorContext], and each function of [WithValueCheck] or
// [WithNodeCheck]. In that step, the names are the field names that the
// decode reads, the keys of each map as [fmt.Sprint] prints them, and the
// indices. The two forms agree for most keys. They differ where a typed
// key prints another way than the Decode[any] name of the same key. For
// example, a decode into map[int]string reads the key 1e3 as 1000, but
// Decode[any] names it 1e3. Some names do not lead to one value of the
// typed value, such as a name that several keys of a map print. Others
// lead to a value that the document does not hold. From the first such
// name on, the names read as Decode[any] of the document gives them, and
// the error binds as it binds outside the step.
//
// A value that decodes itself, such as a struct whose UnmarshalYAML method
// sets defaults, need not mirror the document below it. In that step, the
// names from such a value on read as Decode[any] of its node gives them.
// Every error below a slice, an array, or a map that decodes itself binds
// at that value. So the names that a check of such a value or of a value
// below it writes keep the form the check wrote.
// An `@` base that [Rebase] puts in front of the names reads through the
// typed value in that step too. It matches a key by its source text, as
// a path does. The names under a `$` base read as Decode[any] of the
// document gives them. [SelfValidateValue] keeps an `@` base of `.name`
// and `[n]` selectors as such a path between the names of the value it
// walks and the names of the error.
//
// Before a Node binds the error, [Error.Path] reports the names as an `@`
// path. The text that [BindValue] writes shows the same names as a `$`
// path from the value. [Rebase] puts its
// base in front of that path, and the names then read from the value at
// the base. A [Node.Bind] translates the names of every problem of the
// error through one read of the document. A program that binds many errors
// joins them first, so each mapping decodes its keys once for all of them.
//
// When several keys of a mapping decode to one name, the names lead to the
// last of them, whose value the decode keeps. When another key spells the
// same text, a path cannot select the value. The binding then binds the
// error at the position of the value, and the path names the member as the
// decoder does. A position or a range that an option of the error sets
// wins over that position.
//
// Names that lead past the data keep the form the caller wrote from there
// on. The error binds where that path binds, as [SourceError.Nearest]
// describes. A member that a mapping lacks binds at the key of the
// mapping, and an index past the end of a sequence binds at the key of the
// sequence. A name below a scalar binds at the scalar. A name that the
// content of an anchor lacks directly below its alias binds at the alias.
// A name that the content lacks deeper down binds inside the anchor, at
// the nearest value that the content holds.
//
// A name can lead behind an alias that the document cannot follow, such
// as an alias to an anchor of a document from [WithReferences]. So can a
// name in a mapping whose `<<` merge key holds such an alias. That merge
// key brings in the entry of the name from that document, as the paths
// package describes. Such a name keeps its form too, and so does
// each name below it. The error binds where that path binds. A name such
// as 3.10 there binds where a path through the key 3.10 binds. Elsewhere,
// a name that does not match a member but whose text a path selects, such
// as 3.10 for the member 3.1, binds at the last value the names lead to.
func AtData(names ...string) ErrorOption {
	return newDataNames(names, false).option()
}

// AtDataKey is an [ErrorOption] that locates an error at the key of the
// mapping entry the names lead to. It suits an error about the key itself,
// such as an unknown member. It reads the names as [AtData] does, and the
// path it sets ends in `~`, as a path from [paths.Path.Key] does:
//
//	niceyaml.NewError("unknown port", niceyaml.AtDataKey("ports", "16"))
//	// cfg.yaml:2:3: $.ports.0x10~: unknown port
//
// An element of a sequence and the data as a whole do not have a key, so
// the error binds at the value there. A member that the data lacks does
// not have a key either, so the binding locates it as AtData does. A name
// behind an alias that the document cannot follow keeps its form, as
// AtData describes, and the path still ends in `~`.
func AtDataKey(names ...string) ErrorOption {
	return newDataNames(names, true).option()
}

// dataNames is a location in decoded data: the names that lead to a
// value, and whether the location is the key of the entry there.
type dataNames struct {
	// The names of decoded data, from the value the location reads from.
	names []string
	// The path selectors that stand in place of some names, at the index
	// of each, or nil. A step through such a selector matches a key by its
	// source text, as a path does, and not by its decoded name. The zero
	// [paths.Selector] leaves its name as it is, and so does a short list.
	selectors []paths.Selector
	// The `@` path that an unbound error shows for the names, with one
	// selector for each name and without the `~` of key.
	shown paths.Path
	// Whether the location is the key of the entry the names lead to.
	key bool
}

// newDataNames creates a new [*dataNames] that holds a copy of names, so a
// caller can reuse its slice. It shows each name as a `.name` selector.
func newDataNames(names []string, key bool) *dataNames {
	return &dataNames{shown: paths.Current().Child(names...), names: slices.Clone(names), key: key}
}

// path returns the path that an unbound error shows for d, which ends in
// `~` when the location is a key.
func (d *dataNames) path() paths.Path {
	if d.key {
		return d.shown.Key()
	}

	return d.shown
}

// option returns the [ErrorOption] that locates an error at d, as [AtData]
// describes.
func (d *dataNames) option() ErrorOption {
	return func(c *errorConfig) {
		c.path, c.hasPath, c.data = d.path(), true, d
	}
}

// locus returns the [dataLocus] of an error at d, which reads from the
// value that the error binds at, or nil for a nil d.
func (d *dataNames) locus() *dataLocus {
	if d == nil {
		return nil
	}

	return &dataLocus{base: paths.Current(), dataNames: *d, below: paths.Current()}
}

// dataLocus is where an error that [AtData] or [AtDataKey] located lies,
// before a binding translates its names. The `~` of a key ends the names,
// and below is then empty.
type dataLocus struct {
	// The path to the value that the names read from. It starts at `@`,
	// the value the error binds at, until a scope or a `$` base moves it.
	base paths.Path
	// The path below the value that the names lead to, as the document
	// spells it.
	below paths.Path
	dataNames
}

// over returns the [dataLocus] of l below the value at d, for a move whose
// base d names. The path of l is an `@` path. Names that l reads from that
// value follow the names of d. A base of `.name` and `[n]` selectors can
// lie between them, such as the base of a [Rebase] in a check. Each such
// selector then follows the names of d as a step that matches a key by its
// source text, as the base does. Any other path of l reads below the value
// at d. A dataLocus does not hold names on both sides of any other path,
// so l then reads as the path it shows. Below the key that d locates, l
// reads as the path it shows too. A key is a scalar, so the data lacks
// every value below it. The path of a key that l names there by
// [AtDataKey] loses its `~`, but a path that [AtPath] writes keeps it.
func (d *dataLocus) over(l locus) *dataLocus {
	if l.data != nil && !d.key && d.below.Len() == 0 && spellable(l.data.base) {
		names := slices.Clone(d.names)
		selectors := slices.Concat(d.selectors, make([]paths.Selector, len(d.names)-len(d.selectors)))

		for sel := range l.data.base.Selectors() {
			names = append(names, selectorName(sel))
			selectors = append(selectors, sel)
		}

		selectors = append(selectors, l.data.selectors...)

		return &dataLocus{
			base:      paths.Current(),
			shown:     d.shown.Join(l.data.base, l.data.shown),
			names:     append(names, l.data.names...),
			selectors: selectors,
			key:       l.data.key,
			below:     l.data.below,
		}
	}

	over := *d

	below := l.path
	if d.key && l.data != nil && l.data.key {
		below = valueOf(below)
	}

	over.below = d.below.Join(below)
	if d.key && over.below.Len() > 0 {
		over.key = false
		over.below = paths.Current().Key().Join(over.below)
	}

	return &over
}

// translate returns l with the names of decoded data it holds written as
// a `$` path that the document of b spells, and reports whether it wrote
// one. The names read from the value at the base of l, as the Decode[any]
// of that value names its data. Every translation of one bind call reads
// the mappings through one [datapath.Index]. The path below the names
// follows the result. Where the names lead to a value that the path does
// not select, l also takes the position of that value. A position or a
// range that l holds already wins.
//
// The result is l as it is when l does not hold names, when its path is
// ambiguous, and when b binds outside any document. It is l as it is too
// when the base does not resolve in a document that parsed. The path of
// l then names the data as the names do, and it binds as [AtPath] of that
// path binds. Where the data lacks the value that such a locus names, the
// path of a key loses its `~`, because that value does not have a key.
func (b binder) translate(l locus) (locus, bool) {
	d := l.data
	if d == nil || !l.hasPath || l.ambiguous || b.unplaced || !d.base.IsAbsolute() {
		return l, false
	}

	node := b.node
	if node == nil && b.route {
		doc, err := b.src.single()
		if err != nil {
			return l, false
		}

		node = doc
	}

	if node == nil || node.doc.err != nil {
		return l, false
	}

	resolver := node.DocumentResolver()

	start, err := resolver.Node(d.base)
	if err != nil {
		return b.unkeyed(node, l)
	}

	if astnode.IsNil(start) {
		return l, false
	}

	// The holder of the start lets a name that the start lacks bind where
	// it binds when the names lead to the start from the root.
	from := datapath.Target{Path: d.base, Node: start, Parent: datapath.ParentAt(resolver, d.base)}

	path, tk, n := b.dataIndex(node).Reach(from, d.names, d.selectors, d.key)
	path = path.Join(selectorsFrom(d.shown, n), d.below)

	l.path, l.data = path, nil

	if l.loc == nil && tk != nil && tk.Position != nil {
		l.loc = position.NewFromToken(tk)
	}

	return l, true
}

// unkeyed returns l, whose base does not resolve in the document of n,
// without the `~` of a key where the data lacks the value at the base. It
// reports whether it dropped one. The data lacks that value where the value
// above the first selector of the base that does not resolve lacks the
// name of that selector, as [datapath.Index.Lacks] reports. The data of a
// document with no content is null. So it lacks every value that a name or
// a selector of the base leads to, and only a key of the whole data keeps
// its `~`. Where the binding cannot tell, such as behind an alias that does
// not resolve, the result is l as it is.
func (b binder) unkeyed(n *Node, l locus) (locus, bool) {
	if !l.data.key {
		return l, false
	}

	resolver := n.DocumentResolver()

	above, err := resolver.Node(prefixOf(l.data.base, 0))
	if err != nil {
		// The root of a document resolves unless the document has no
		// content. The data of such a document is null, so it lacks every
		// member and element. Only the whole data keeps its key.
		if l.data.base.Len() == 0 && len(l.data.names) == 0 {
			return l, false
		}

		l.path, l.data = valueOf(l.path), nil

		return l, true
	}

	var (
		next  paths.Selector
		depth int
	)

	for sel := range l.data.base.Selectors() {
		depth++

		node, err := resolver.Node(prefixOf(l.data.base, depth))
		if err != nil {
			next = sel

			break
		}

		above = node
	}

	var name string

	switch next.Kind {
	case paths.SelectorChild:
		name = next.Name
	case paths.SelectorIndex:
		name = strconv.Itoa(next.Index)
	default:
		return l, false
	}

	if !b.dataIndex(n).Lacks(above, name) {
		return l, false
	}

	l.path, l.data = valueOf(l.path), nil

	return l, true
}

// valueOf returns p without the `~` selector that ends it, or p as it is
// when it does not end in `~`.
func valueOf(p paths.Path) paths.Path {
	if sel, ok := p.Last(); ok && sel.Kind == paths.SelectorKey {
		p, _ = p.Parent()
	}

	return p
}

// prefixOf returns the path of the first n selectors of p. The prefix
// starts where p does.
func prefixOf(p paths.Path, n int) paths.Path {
	for p.Len() > n {
		p, _ = p.Parent()
	}

	return p
}

// dataIndex returns the [datapath.Index] of the document of n that b
// translates names of decoded data through. The bind call that b belongs
// to creates it on its first translation in that document.
func (b binder) dataIndex(n *Node) *datapath.Index {
	if idx, ok := b.indexes[n.doc]; ok {
		return idx
	}

	idx := datapath.NewIndex(n.DocumentResolver(), n.keyReader())
	if b.indexes != nil {
		b.indexes[n.doc] = idx
	}

	return idx
}

// typedNames translates the names of decoded data below one value of the
// self-validation walk of a document into a path, as [selfWalker.dataPath]
// reads them from the typed value. It keeps each result. Until the walk
// releases it, only the goroutine of the walk reads it, because each new
// translation writes its result. After the release, it reads only those
// results. So it does not keep the value, the walk, or the parse alive, and
// any number of goroutines can read it.
type typedNames struct {
	// The walk that translates the names, or nil after the release.
	walker *selfWalker
	// The place of value in the walk, or the zero place after the release.
	at place
	// The result of each translation, by [dataID].
	reads map[string]typedRead
	// The value that the check saw, which the names read from.
	value reflect.Value
	// Whether value lies below a slice, an array, or a map that decodes
	// itself. Every error below such a value binds at it.
	sealed bool
}

// typedRead is the result of [selfWalker.dataPath] for one location in
// decoded data.
type typedRead struct {
	// The names that the binding reads from the value at path, as the
	// Decode[any] of that value names its data, or nil where path holds
	// every name.
	rest *dataNames
	// The `@` path from the value of the check, as the document spells it.
	path paths.Path
	// Whether the walk translated the location. A location that it did not
	// translate stays as it is, and the binding reads its names.
	ok bool
}

// translate returns l with the names of decoded data that it reads from
// the value of t written as an `@` path from that value. Names below a
// value that decodes itself stay names of decoded data under that path,
// as [selfWalker.dataPath] describes. It returns any other l as it is,
// such as one whose names read from a `$` base.
func (t *typedNames) translate(l locus) locus {
	if t == nil || l.data == nil || l.data.base.IsAbsolute() {
		return l
	}

	r := t.read(l.data)
	if !r.ok {
		return l
	}

	if r.rest == nil {
		l.path, l.data = r.path.Join(l.data.below), nil

		return l
	}

	data := &dataLocus{base: r.path, dataNames: *r.rest, below: l.data.below}
	l.path, l.data = r.path.Join(r.rest.path(), data.below), data

	return l
}

// read returns the translation of d. After the walk releases t, a
// location that the walk did not translate stays as it is.
func (t *typedNames) read(d *dataLocus) typedRead {
	id := dataID(d)
	if r, ok := t.reads[id]; ok {
		return r
	}

	if t.walker == nil {
		return typedRead{}
	}

	r := t.walker.dataPath(t.value, t.at, d, t.sealed)

	if t.reads == nil {
		t.reads = map[string]typedRead{}
	}

	t.reads[id] = r

	return r
}

// release drops the value and the walk that t reads from, and the place of
// that value, whose steps hold nodes of the parse.
func (t *typedNames) release() {
	t.walker, t.value, t.at = nil, reflect.Value{}, place{}
}

// dataID returns a text that the base and the names of d alone give.
func dataID(d *dataLocus) string {
	var sb strings.Builder

	sb.WriteString(strconv.FormatBool(d.key) + d.base.String() + "|" + d.shown.String())

	for i, name := range d.names {
		if sel := d.selectorAt(i); sel.Kind != 0 {
			sb.WriteString(":" + selectorPath(sel).String())

			continue
		}

		sb.WriteString(":" + strconv.Itoa(len(name)) + ":" + name)
	}

	return sb.String()
}

// data returns the location in decoded data of the value at p, for a walk
// of a value outside any document. The first n steps of p become names. A
// field gives its name, an element its index, and a map entry the text of
// its key, as the path of p spells them. The steps past them stay a path
// below the names. It returns nil when n is 0, and when a `~` lies among
// the names before the last of them.
func (p place) data(n int) *dataLocus {
	var steps []*step

	for s := p.last; s != nil; s = s.prev {
		steps = append(steps, s)
	}

	slices.Reverse(steps)

	n = min(n, len(steps))
	if n == 0 {
		return nil
	}

	d := &dataLocus{base: paths.Current(), below: paths.Current(), shown: paths.Current()}

	for i, s := range steps[:n] {
		sel, _ := s.selector.Last()

		switch sel.Kind {
		case paths.SelectorIndex:
			d.names = append(d.names, strconv.Itoa(sel.Index))
		case paths.SelectorKey:
			if i != n-1 {
				return nil
			}

			d.key = true

			continue

		default:
			// Every other step holds a `.name` selector, as [place.child]
			// writes it.
			d.names = append(d.names, sel.Name)
		}

		d.shown = d.shown.Join(s.selector)
	}

	for _, s := range steps[n:] {
		d.below = d.below.Join(s.selector)
	}

	if d.key && d.below.Len() > 0 {
		d.key = false
		d.below = paths.Current().Key().Join(d.below)
	}

	return d
}

// depth returns the number of steps from the start of the walk to p.
func (p place) depth() int {
	n := 0
	for s := p.last; s != nil; s = s.prev {
		n++
	}

	return n
}

// dataPath translates d, which reads from v, a value at the place at, into
// the `@` path from v that the names of d lead to, with a `~` at its end
// for a key. The base of d reads first, as a path of the document, through
// the steps of [selfWalker.pathStep]. Each name then reads from the typed
// value, as [selfWalker.dataStep] reads it, and takes the spelling of the
// document. A path selector that stands in place of a name reads as a step
// of the base does. The result does not translate d where the base does
// not lead to a value of v.
//
// The names below a value that decodes itself stay names of decoded data.
// The binding reads them from the document under the path of that value,
// because the typed value need not mirror the data there. So does every
// name where the document does not hold v. So do the names from the first
// one that the walk cannot read, and from the first one whose value the
// document does not hold. The binding then locates them as it locates them
// outside the decode. A name that the data lacks thus loses the `~` of a
// key. With sealed set, v lies below a slice, an array, or a map that
// decodes itself, and every error binds at that value. The keys and
// indices below it need not name its entries in the document, so the names
// there keep the selectors that d shows for them.
func (w *selfWalker) dataPath(v reflect.Value, at place, d *dataLocus, sealed bool) typedRead {
	for sel := range d.base.Selectors() {
		var ok bool

		v, at, ok = w.pathStep(v, at, sel)
		if !ok {
			return typedRead{}
		}
	}

	rel := d.base

	if !sealed && w.nodeOf(at.last) == nil {
		return typedRead{path: rel, rest: d.from(0), ok: true}
	}

	for i, name := range d.names {
		if !sealed && w.decodesItself(v) {
			return typedRead{path: rel, rest: d.from(i), ok: true}
		}

		var (
			next  reflect.Value
			below place
			ok    bool
		)

		if sel := d.selectorAt(i); sel.Kind != 0 {
			next, below, ok = w.pathStep(v, at, sel)
		} else {
			next, below, ok = w.dataStep(v, at, name, false)
		}

		switch {
		case sealed && !ok:
			rel = rel.Join(selectorsFrom(d.shown, i))

			return typedRead{path: d.keyed(rel), ok: true}

		case !sealed && (!ok || w.nodeOf(below.last) == nil):
			return typedRead{path: rel, rest: d.from(i), ok: true}
		}

		v, at = next, below
		rel = rel.Join(at.last.selector)
	}

	return typedRead{path: d.keyed(rel), ok: true}
}

// keyed returns p with a `~` at its end when d locates a key, or p as it
// is.
func (d *dataNames) keyed(p paths.Path) paths.Path {
	if d.key {
		return p.Key()
	}

	return p
}

// pathStep returns the value that the one selector sel selects below v,
// and its place below at. A child selector names a field by the name that
// the decode reads, or a map entry by the text of its key in the document.
// The walk spells the path of that entry with the same text. An index
// selector names an element. It reports false for any other selector, and
// where [selfWalker.dataStep] reports false.
func (w *selfWalker) pathStep(v reflect.Value, at place, sel paths.Selector) (reflect.Value, place, bool) {
	switch sel.Kind {
	case paths.SelectorChild:
		return w.dataStep(v, at, sel.Name, true)
	case paths.SelectorIndex:
		return w.dataStep(v, at, strconv.Itoa(sel.Index), true)
	default:
		return reflect.Value{}, at, false
	}
}

// indirect returns v past its pointers and interfaces, or the zero
// [reflect.Value] where one of them is nil.
func indirect(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}

		v = v.Elem()
	}

	return v
}

// decodesItself reports whether v, past its pointers and interfaces,
// decodes itself, as the unmarshalers of the walk find it.
func (w *selfWalker) decodesItself(v reflect.Value) bool {
	v = indirect(v)
	if !v.IsValid() {
		return false
	}

	_, whole := w.cfg.unmarshalers.Whole(v.Type())

	return whole
}

// from returns the names of d from the one at index i on, with the
// selectors and the `~` that d holds for them.
func (d *dataNames) from(i int) *dataNames {
	rest := dataNames{names: d.names[i:], shown: selectorsFrom(d.shown, i), key: d.key}
	if i < len(d.selectors) {
		rest.selectors = d.selectors[i:]
	}

	return &rest
}

// selectorAt returns the path selector that stands in place of name i of
// d, or the zero [paths.Selector] where the name stands as it is.
func (d *dataNames) selectorAt(i int) paths.Selector {
	if i < len(d.selectors) {
		return d.selectors[i]
	}

	return paths.Selector{}
}

// spellable reports whether p, an `@` path, holds `.name` and `[n]`
// selectors alone, which steps of a [dataLocus] can hold.
func spellable(p paths.Path) bool {
	for sel := range p.Selectors() {
		if sel.Kind != paths.SelectorChild && sel.Kind != paths.SelectorIndex {
			return false
		}
	}

	return true
}

// selectorName returns the name of decoded data that the `.name` or `[n]`
// selector sel shows: its name, or its index in decimal digits.
func selectorName(sel paths.Selector) string {
	if sel.Kind == paths.SelectorIndex {
		return strconv.Itoa(sel.Index)
	}

	return sel.Name
}

// selectorsFrom returns the `@` path of the selectors of p from the one at
// index i on.
func selectorsFrom(p paths.Path, i int) paths.Path {
	rest := paths.Current()

	for sel := range p.Selectors() {
		if i > 0 {
			i--

			continue
		}

		rest = rest.Join(selectorPath(sel))
	}

	return rest
}

// selectorPath returns the `@` path of the one selector sel, a `.name` or
// an `[n]` selector, as the path that a [dataNames] shows holds them.
func selectorPath(sel paths.Selector) paths.Path {
	if sel.Kind == paths.SelectorIndex {
		return paths.Current().Index(sel.Index)
	}

	return paths.Current().Child(sel.Name)
}

// dataStep returns the value one name below v and its place below at. The
// name is a field name that the decode reads for a struct and an index for
// a slice or an array. For a map, the name is the printed key of an entry,
// or with spelled set, the text of that key in the document. A map entry
// takes that text in its place either way, as [selfWalker.entriesOf] finds
// it. It reports false where v does not hold the name and where several
// keys of a map match the name. It also reports false where v decodes
// itself, because the values below such a v need not mirror the document.
func (w *selfWalker) dataStep(v reflect.Value, at place, name string, spelled bool) (reflect.Value, place, bool) {
	v = indirect(v)
	if !v.IsValid() || w.decodesItself(v) {
		return reflect.Value{}, at, false
	}

	switch v.Kind() {
	case reflect.Struct:
		return w.fieldNamed(addressable(v), at, name, spelled)

	case reflect.Map:
		return w.entryNamed(v, at, name, spelled)

	case reflect.Slice, reflect.Array:
		i, err := strconv.Atoi(name)
		if err != nil || i < 0 || i >= v.Len() || strconv.Itoa(i) != name {
			return reflect.Value{}, at, false
		}

		return v.Index(i), at.index(i), true

	default:
		return reflect.Value{}, at, false
	}
}

// fieldNamed returns the field of v, a struct at the place at, that the
// decode reads under name, and the place of that field. A field of the
// struct itself wins over a field of an inline struct, as in the decode.
// An inline map holds the entries that no field names, which
// [selfWalker.entryNamed] finds by name and spelled.
func (w *selfWalker) fieldNamed(v reflect.Value, at place, name string, spelled bool) (reflect.Value, place, bool) {
	var inline []reflect.Value

	for i := range v.NumField() {
		f := v.Type().Field(i)

		fieldName, isInline, skip := yamlfield.Name(f)
		if skip {
			continue
		}

		if isInline {
			inline = append(inline, exposed(v.Field(i)))

			continue
		}

		if fieldName == name {
			return exposed(v.Field(i)), at.child(name), true
		}
	}

	for _, field := range inline {
		for field.IsValid() && field.Kind() == reflect.Pointer {
			if field.IsNil() {
				field = reflect.Value{}

				break
			}

			field = field.Elem()
		}

		switch {
		case !field.IsValid():
		case field.Kind() == reflect.Struct:
			if found, below, ok := w.fieldNamed(addressable(field), at, name, spelled); ok {
				return found, below, true
			}

		case field.Kind() == reflect.Map:
			if found, below, ok := w.entryNamed(field, at, name, spelled); ok {
				return found, below, true
			}
		}
	}

	return reflect.Value{}, at, false
}

// entryNamed returns the value of the entry of v, a map at the place at,
// whose key prints as name, and the place of that value. With spelled set,
// it finds the entry whose key the document spells as name instead. The
// place spells the key as the document does. It reports false where no
// key or several keys match name.
func (w *selfWalker) entryNamed(v reflect.Value, at place, name string, spelled bool) (reflect.Value, place, bool) {
	idx := w.entriesOf(v, at)

	byName := idx.byName
	if spelled {
		byName = idx.bySeg
	}

	e, ok := byName[name]
	if !ok || e.shared {
		return reflect.Value{}, at, false
	}

	return e.value, at.child(e.seg), true
}

// entryIndex finds the entries of one map of the walk by the printed text
// of each key and by the text of each key in the document.
type entryIndex struct {
	// The entry of each printed key, as [keyName] prints it.
	byName map[string]indexedEntry
	// The entry of each path segment, as [datapath.MapGroups] spells it.
	bySeg map[string]indexedEntry
}

// indexedEntry is an entry of an [entryIndex].
type indexedEntry struct {
	// The value of the entry.
	value reflect.Value
	// The path segment of the entry, which spells its key as the document
	// does.
	seg string
	// Whether several entries match the text that finds this one.
	shared bool
}

// entriesID names a map that the walk indexed and the node of the mapping
// it decoded from.
//
//nolint:unused // The fields tell the keys of the walker's map apart.
type entriesID struct {
	node ast.Node
	m    visit
}

// entriesOf returns the [*entryIndex] of v, a map at the place at. It
// spells each key as [datapath.MapGroups] does for the walk of the map.
// The walk builds the index of each map and its mapping once, so the
// translations of many errors below one map read it in time that grows
// with the size of the map once, and not for each error.
func (w *selfWalker) entriesOf(v reflect.Value, at place) *entryIndex {
	node := w.nodeOf(at.last)

	id := entriesID{node: node, m: visitOf(v)}
	id.m.len = v.Len()

	if idx, ok := w.entries[id]; ok {
		return idx
	}

	idx := &entryIndex{byName: map[string]indexedEntry{}, bySeg: map[string]indexedEntry{}}
	add := func(by map[string]indexedEntry, text string, e indexedEntry) {
		if _, ok := by[text]; ok {
			e.shared = true
		}

		by[text] = e
	}

	names := maps.Clone(w.keyNames(node, v.Type().Key()))

	for _, group := range datapath.MapGroups(v, names) {
		for _, entry := range group.Entries {
			e := indexedEntry{value: entry.Value, seg: group.Seg}

			add(idx.byName, keyName(entry.Key), e)
			add(idx.bySeg, group.Seg, e)
		}
	}

	if w.entries == nil {
		w.entries = map[entriesID]*entryIndex{}
	}

	w.entries[id] = idx

	return idx
}

// keyName returns the name of a key of a decoded map, as
// [datapath.MemberName] gives the value it holds. A pointer key names the
// value it points to, as the decode reads it.
func keyName(key reflect.Value) string {
	for key.Kind() == reflect.Interface || key.Kind() == reflect.Pointer {
		if key.IsNil() {
			return datapath.MemberName(nil)
		}

		key = key.Elem()
	}

	if !key.CanInterface() {
		return ""
	}

	return datapath.MemberName(key.Interface())
}

// DataLocator finds where a location in decoded data lies in the document
// the data came from. It suits a caller that needs the path and not an
// error, such as one that edits the value or reads its ranges. It reads
// names as [AtData] does. An error at such a location takes AtData
// instead, which a [Node] translates when it binds the error:
//
//	loc := n.DataLocator()
//
//	p, ok := loc.Path("ports", "16", "name") // $.ports.0x10.name, true
//
// Create instances with [Node.DataLocator].
//
// A DataLocator is safe for concurrent use, and it reads each mapping
// once, so a caller creates one for all the lookups of a run. Its reads
// of `<<` merge keys share one budget, as large as the budget that
// [paths.ErrExcessiveMerging] allows one path selector. A caller thus
// creates a DataLocator for each run and does not keep one for the life
// of the document.
type DataLocator struct {
	// The node the names read from.
	root ast.Node
	// Finds the members of each mapping by the names a decode gives them.
	idx *datapath.Index
	// The `$` path to root.
	base paths.Path
	// Guards idx, which two walks must not read at the same time.
	mu sync.Mutex
}

// DataLocator creates a new [*DataLocator] for the data the Node decodes
// to. The names it reads start at the Node, as the data from
// [Node.Decode] does. A [Validator] thus creates one from the Node it got,
// whatever the scope of that Node. Each path it writes starts at `$`, so
// [Node.At] of any Node of the document resolves it.
//
// A document that did not parse does not decode to any data, as
// [Node.Err] describes. Every name then leads nowhere.
func (n *Node) DataLocator() *DataLocator {
	return &DataLocator{
		root: n.valueNode(),
		idx:  datapath.NewIndex(n.DocumentResolver(), n.keyReader()),
		base: n.base,
	}
}

// Path returns the `$` path to the value the names lead to in the data of
// the [DataLocator], with each key spelled as the source spells it, and
// true:
//
//	p, ok := loc.Path("ports", "16", "name") // $.ports.0x10.name, true
//
// An empty list of names leads to the whole of that data. Path reports
// false where no path selects the value. The data might lack the value,
// or the names might lead behind an alias that the document cannot
// follow. Another key might also spell the text of the member, as
// [AtData] describes. The path then names the value as AtData writes it
// for an error, and a binding of such an error binds where AtData
// describes.
func (l *DataLocator) Path(names ...string) (paths.Path, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.idx.Find(datapath.Target{Path: l.base, Node: l.root}, names)
}
