package niceyaml

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/paths"
)

// errNoScalarText is the error of a node that [Layers] cannot write into
// the merged document, since it is no collection and holds no text.
var errNoScalarText = errors.New("node has no text to merge")

// mergedKind is the kind of a [mergedValue].
type mergedKind int

// [mergedKind] constants. A value of the zero kind is a null.
const (
	mergedNull mergedKind = iota
	mergedScalar
	mergedSequence
	mergedMapping
)

// mergedValue is a value of the document that [Layers] merge their Nodes
// into. A [layerReader] reads one from each layer, with every alias and
// `<<` merge key of the layer resolved, and [mergedValue.merge] puts the
// value of a higher layer over it.
type mergedValue struct {
	// The highest layer whose document holds the value. For a mapping
	// that several layers hold, the entries can belong to lower layers.
	layer *Node
	// The index in entries of each key name.
	names map[string]int
	// The tag the layer writes on the value, as the source spells it, or
	// the empty string.
	tag string
	// The YAML text of a scalar or a null, which fits on one line.
	text string
	// The elements of a sequence.
	items []*mergedValue
	// The entries of a mapping, in the order the layers first name them.
	entries []*mergedEntry
	kind    mergedKind
}

// mergedEntry is an entry of a mapping in a [mergedValue].
type mergedEntry struct {
	value *mergedValue
	// The text a `.name` selector matches the key by, as
	// [paths.Resolver.KeyName] gives it.
	name string
	// The YAML text of the key, with its tag, as the layer of value
	// spells it.
	key string
}

// merge returns upper merged over v, where upper comes from a higher
// layer. Two mappings merge key by key, and v then holds the result. A
// null in upper keeps v, unless v is a null too. Any other upper replaces
// v. Either value may be nil, which stands for a layer that holds no
// value there.
func (v *mergedValue) merge(upper *mergedValue) *mergedValue {
	switch {
	case v == nil:
		return upper
	case upper == nil:
		return v
	case upper.kind == mergedNull:
		if v.kind == mergedNull {
			return upper
		}

		return v

	case v.kind != mergedMapping || upper.kind != mergedMapping:
		return upper
	}

	for _, entry := range upper.entries {
		i, ok := v.names[entry.name]
		if !ok {
			v.names[entry.name] = len(v.entries)
			v.entries = append(v.entries, entry)

			continue
		}

		held := v.entries[i]
		held.value = held.value.merge(entry.value)

		// The key reads as the layer of the value spells it, so a path
		// to the key resolves in that layer.
		if held.value.layer == entry.value.layer {
			held.key = entry.key
		}
	}

	v.layer = upper.layer

	if upper.tag != "" {
		v.tag = upper.tag
	}

	return v
}

// block reports whether v takes lines of its own in the merged document:
// a mapping or a sequence that is not empty.
func (v *mergedValue) block() bool {
	return len(v.entries) > 0 || len(v.items) > 0
}

// inline returns the text of a value that is no block, with its tag in
// front.
func (v *mergedValue) inline() string {
	text := v.text

	switch v.kind {
	case mergedMapping:
		text = "{}"
	case mergedSequence:
		text = "[]"
	case mergedNull, mergedScalar:
	}

	if v.tag == "" {
		return text
	}

	return v.tag + " " + text
}

// layerReader reads the value of one layer of a [Layers] as the go-yaml
// decoder reads it, into a [mergedValue] that holds no alias and no `<<`
// merge key.
//
// An alias reads the anchor the [paths.Resolver] of the document binds it
// to, which is the anchor a decode of the layer reads, as [decodeTree]
// describes. An alias the resolver binds to no anchor reads an anchor of
// the reference documents of the [Source] by name, and so does every
// alias inside a reference document. An alias inside the anchor it refers
// to reads as null, as the decoder reads it.
//
// Create instances with [newLayerReader].
type layerReader struct {
	layer    *Node
	resolver *paths.Resolver
	// The anchors of the reference documents of the layer by name, which
	// references fills on its first call.
	refs map[string]*ast.AnchorNode
	// The anchors whose content the reader is inside.
	open map[*ast.AnchorNode]bool
}

// newLayerReader creates a new [*layerReader] for layer.
func newLayerReader(layer *Node) *layerReader {
	return &layerReader{
		layer:    layer,
		resolver: layer.doc.pathResolver(),
		open:     map[*ast.AnchorNode]bool{},
	}
}

// read returns the value of the layer, or nil when the layer holds none,
// as the document of comments alone does. A layer that a decode rejects
// returns the error of that decode, bound in the layer, so a layer fails
// here as it fails on its own. The reader writes each alias out in full,
// so the layer must also pass the alias limit as a decode into a type
// that reads text does.
func (r *layerReader) read(ctx context.Context) (*mergedValue, error) {
	n := r.layer

	if n.doc.err != nil {
		return nil, n.doc.err
	}

	var discard any

	err := n.decodeInto(ctx, &discard, decodeConfig{skipSelfValidation: true})
	if err != nil {
		return nil, err
	}

	err = aliasing.CheckDecodeText(n)
	if err != nil {
		return nil, n.Invalid(err, atToken(contentStart(n.AST())))
	}

	if !astnode.HasContent(n.AST()) {
		return nil, nil //nolint:nilnil // A layer with no content holds no value.
	}

	v, err := r.value(n.AST(), false)
	if err != nil {
		return nil, n.bindOwn(err)
	}

	return v, nil
}

// value returns the value under node. The ref flag says that node lies in
// a reference document, where an alias reads the reference documents
// alone.
func (r *layerReader) value(node ast.Node, ref bool) (*mergedValue, error) {
	var v *mergedValue

	err := r.follow(node, ref, "", func(content ast.Node, tag string, ref bool) error {
		var err error

		v, err = r.content(content, ref)
		if err != nil {
			return err
		}

		v.tag = tag

		return nil
	})

	return v, err
}

// follow calls visit with the content under node, which is the node
// behind every anchor, alias, tag, and `?` indicator on the way. It passes
// the first tag it looks through, as the source spells it, and whether
// the content lies in a reference document. The tag starts as outer, the
// tag a caller found above node, or the empty string. A tag over no
// value that the decoder reads as null, as [isTaggedNull] reports, is no
// tag to pass, since the text "null" behind it reads as another value.
// Each anchor on the way stays open while visit runs. The content of an
// alias inside the anchor it refers to is nil, which reads as null.
func (r *layerReader) follow(
	node ast.Node, ref bool, outer string, visit func(content ast.Node, tag string, ref bool) error,
) error {
	if astnode.IsNil(node) {
		return visit(nil, outer, ref)
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		if r.open[n] {
			return visit(nil, outer, ref)
		}

		r.open[n] = true
		defer delete(r.open, n)

		return r.follow(n.Value, ref, outer, visit)

	case *ast.AliasNode:
		// An alias that lies inside the anchor it refers to reads as null
		// wherever the reader reaches it from. The open anchors alone miss
		// one that a `<<` merge key brought in, since mapping reads that
		// value once the anchor has closed.
		if !ref && r.layer.doc.enclosedAliases()[n] {
			return visit(nil, outer, ref)
		}

		anchor, inRef, err := r.anchor(n, ref)
		if err != nil {
			return err
		}

		return r.follow(anchor, inRef, outer, visit)

	case *ast.TagNode:
		if outer == "" && !tagsNoValue(n) {
			outer = n.GetToken().Value
		}

		return r.follow(n.Value, ref, outer, visit)

	case *ast.MappingKeyNode:
		return r.follow(n.Value, ref, outer, visit)
	case *ast.DocumentNode:
		return r.follow(n.Body, ref, outer, visit)
	default:
		return visit(node, outer, ref)
	}
}

// tagsNoValue reports whether tag stands over no value and the go-yaml
// decoder reads it as null, as [isTaggedNull] describes. The merged
// document writes that null as text. Behind a local tag the text reads
// as a string, and behind a tag such as "!!map" it does not parse.
func tagsNoValue(tag *ast.TagNode) bool {
	return isTaggedNull(tag) && (astnode.IsNil(tag.Value) || tag.Value.Type() == ast.NullType)
}

// anchor returns the anchor alias reads, and whether that anchor lies in
// a reference document. An alias of the document reads the anchor the
// resolver binds it to. An alias the resolver binds to no anchor, and
// every alias of a reference document, reads the last anchor of its name
// in the reference documents.
func (r *layerReader) anchor(alias *ast.AliasNode, ref bool) (*ast.AnchorNode, bool, error) {
	if !ref {
		found, err := r.resolver.Anchor(alias)
		if anchor, ok := found.(*ast.AnchorNode); err == nil && ok && anchor != nil {
			return anchor, false, nil
		}
	}

	name, _ := nodeName(alias.Value)

	anchor, ok := r.references()[name]
	if !ok {
		err := fmt.Errorf("%w: *%s has no anchor to read", paths.ErrAlias, name)

		return nil, false, r.invalid(err, alias, ref)
	}

	return anchor, true, nil
}

// references returns the anchors of the reference documents of the layer
// by name, and reads the documents on the first call. The decoder reads
// the reference documents in order and keeps the anchors by name, so the
// last anchor of a name wins.
func (r *layerReader) references() map[string]*ast.AnchorNode {
	if r.refs != nil {
		return r.refs
	}

	r.refs = map[string]*ast.AnchorNode{}

	for _, text := range r.layer.source.references {
		// The decode in read rejected a layer with a reference document
		// that does not parse.
		file, err := parser.ParseBytes(text, 0)
		if err != nil {
			continue
		}

		for _, doc := range file.Docs {
			ast.Walk(anchorCollector(r.refs), doc)
		}
	}

	return r.refs
}

// anchorCollector is an [ast.Visitor] that keeps each anchor it visits
// under its name, with a later anchor in place of an earlier one.
type anchorCollector map[string]*ast.AnchorNode

// Visit implements [ast.Visitor].
func (c anchorCollector) Visit(node ast.Node) ast.Visitor {
	if anchor, ok := node.(*ast.AnchorNode); ok && anchor != nil {
		if name, ok := nodeName(anchor.Name); ok {
			c[name] = anchor
		}
	}

	return c
}

// content returns the value of content, a node [layerReader.follow]
// found.
func (r *layerReader) content(content ast.Node, ref bool) (*mergedValue, error) {
	v := &mergedValue{layer: r.layer}

	switch n := content.(type) {
	case nil:
		v.text = "null"
	case *ast.NullNode:
		v.text = n.GetToken().Value
	case *ast.MappingNode, *ast.MappingValueNode:
		entries, _ := mappingEntries(content)

		return v, r.mapping(v, entries, ref)

	case *ast.SequenceNode:
		v.kind = mergedSequence

		for _, elem := range n.Values {
			item, err := r.value(elem, ref)
			if err != nil {
				return nil, err
			}

			v.items = append(v.items, item)
		}

	default:
		text, ok := scalarText(content)
		if !ok {
			return nil, r.invalid(errNoScalarText, content, ref)
		}

		v.kind, v.text = mergedScalar, text
	}

	return v, nil
}

// invalid returns err as the error of node, a node the layer cannot
// merge, at the token that starts the content of node. A node of a
// reference document has no position in the layer, so its error carries
// none.
func (r *layerReader) invalid(err error, node ast.Node, ref bool) error {
	if ref {
		return Invalid(err)
	}

	return Invalid(err, atToken(astnode.FirstToken(astnode.Content(node))))
}

// mappingEntries returns the entries of content, and reports whether
// content is a mapping.
func mappingEntries(content ast.Node) ([]*ast.MappingValueNode, bool) {
	switch n := content.(type) {
	case *ast.MappingNode:
		return n.Values, true
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{n}, true
	default:
		return nil, false
	}
}

// flatEntry is an entry of a mapping as [layerReader.flatten] finds it:
// the key and the value, and whether they lie in a reference document.
type flatEntry struct {
	key   ast.Node
	value ast.Node
	ref   bool
}

// mapping fills v with the entries of a mapping, which holds entries
// itself and the entries its `<<` merge keys bring in. It reads the value
// of each entry the mapping keeps, and no other.
func (r *layerReader) mapping(v *mergedValue, entries []*ast.MappingValueNode, ref bool) error {
	v.kind, v.names = mergedMapping, map[string]int{}

	var order []string

	kept := map[string]flatEntry{}

	err := r.flatten(entries, ref, func(name string, entry flatEntry) {
		if _, ok := kept[name]; !ok {
			order = append(order, name)
		}

		kept[name] = entry
	})
	if err != nil {
		return err
	}

	for _, name := range order {
		entry := kept[name]

		key, err := r.key(entry.key, entry.ref)
		if err != nil {
			return err
		}

		value, err := r.value(entry.value, entry.ref)
		if err != nil {
			return err
		}

		v.names[name] = len(v.entries)
		v.entries = append(v.entries, &mergedEntry{name: name, key: key, value: value})
	}

	return nil
}

// flatten calls keep with each entry of entries under its key name, in
// document order, and with the entries of each `<<` merge key where the
// key stands. The go-yaml decoder sets the entries of a mapping in that
// order, so the last call for a name holds the entry it keeps, as
// [paths.Resolver.Entry] finds it. A key with no name returns an error.
func (r *layerReader) flatten(entries []*ast.MappingValueNode, ref bool, keep func(string, flatEntry)) error {
	for _, entry := range entries {
		if entry == nil {
			continue
		}

		if _, merge := astnode.Content(entry.Key).(*ast.MergeKeyNode); merge {
			err := r.merged(entry.Value, ref, keep)
			if err != nil {
				return err
			}

			continue
		}

		name, ok := r.resolver.KeyName(entry.Key)
		if !ok {
			return r.invalid(ErrUnnamedKey, entry.Key, ref)
		}

		keep(name, flatEntry{key: entry.Key, value: entry.Value, ref: ref})
	}

	return nil
}

// merged calls keep with the entries the value of a `<<` merge key brings
// in. The value is a mapping, or a sequence of mappings that apply in
// order. Any other value brings in nothing, and so does an element of
// the sequence that is no mapping.
func (r *layerReader) merged(value ast.Node, ref bool, keep func(string, flatEntry)) error {
	source := func(content ast.Node, _ string, ref bool) error {
		if entries, ok := mappingEntries(content); ok {
			return r.flatten(entries, ref, keep)
		}

		return nil
	}

	return r.follow(value, ref, "", func(content ast.Node, tag string, ref bool) error {
		seq, ok := content.(*ast.SequenceNode)
		if !ok {
			return source(content, tag, ref)
		}

		for _, elem := range seq.Values {
			err := r.follow(elem, ref, "", source)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// key returns the YAML text of key, the key of a mapping entry, with its
// tag in front.
func (r *layerReader) key(key ast.Node, ref bool) (string, error) {
	var text string

	err := r.follow(key, ref, "", func(content ast.Node, tag string, _ bool) error {
		scalar, ok := scalarText(content)
		if !ok {
			return r.invalid(ErrUnnamedKey, key, ref)
		}

		text = scalar
		if tag != "" {
			text = tag + " " + scalar
		}

		return nil
	})

	return text, err
}

// scalarText returns the YAML text that reads as the scalar node on one
// line of a block collection, and reports false for a node that is no
// scalar. A scalar that is no string keeps the text of the source, such
// as 0x10 or 1.50. A string keeps its text and its quotes where they read
// the same on one line, and takes double quotes otherwise, as a block
// scalar does.
func scalarText(node ast.Node) (string, bool) {
	switch n := node.(type) {
	case nil:
		return "", false

	case *ast.LiteralNode:
		if n.Value == nil {
			return `""`, true
		}

		return strconv.Quote(n.Value.Value), true

	case *ast.StringNode:
		return stringText(n), true

	case ast.ScalarNode:
		if tk := n.GetToken(); tk != nil {
			return tk.Value, true
		}
	}

	return "", false
}

// stringText returns the YAML text of the string n, as [scalarText]
// describes.
func stringText(n *ast.StringNode) string {
	tk := n.GetToken()

	switch {
	case tk == nil, tk.Type == token.DoubleQuoteType, strings.ContainsFunc(n.Value, notPrintable):
		return strconv.Quote(n.Value)
	case tk.Type == token.SingleQuoteType:
		return "'" + strings.ReplaceAll(n.Value, "'", "''") + "'"
	case !plainSafe(n.Value):
		return strconv.Quote(n.Value)
	default:
		return n.Value
	}
}

// notPrintable reports whether r is a character a quoted scalar writes as
// an escape, such as a line break or a tab.
func notPrintable(r rune) bool {
	return !unicode.IsPrint(r)
}

// plainSafe reports whether the text s of a plain scalar reads as that
// scalar on a line of a block collection, as a key or as a value. A
// flow collection takes plain scalars a block collection reads as
// something else, and the root of a document reads "---" and "..." as
// markers.
func plainSafe(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}

	if strings.ContainsAny(s[:1], "[]{},#&*!|>'\"%@`") {
		return false
	}

	for _, indicator := range []string{"-", "?", ":"} {
		if s == indicator || strings.HasPrefix(s, indicator+" ") {
			return false
		}
	}

	return !strings.HasPrefix(s, "---") && !strings.HasPrefix(s, "...") &&
		!strings.Contains(s, ": ") && !strings.Contains(s, " #") && !strings.HasSuffix(s, ":")
}

// mergedWriter writes a [mergedValue] as the text of a YAML document, in
// block style, with one entry or element on each line.
type mergedWriter struct {
	sb strings.Builder
}

// document writes v as the content of a document. A nil v writes nothing.
func (w *mergedWriter) document(v *mergedValue) {
	switch {
	case v == nil:
	case !v.block():
		w.sb.WriteString(v.inline())
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
// of its element.
func (w *mergedWriter) block(v *mergedValue, indent int, first bool) {
	for _, entry := range v.entries {
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
func (w *mergedWriter) pad(indent int, started bool) {
	if !started {
		w.sb.WriteString(strings.Repeat(" ", indent))
	}
}

// nested writes v behind the ":" of its key or the "-" of its element,
// and ends the line. A block goes on the lines below at indent. With
// compact set, a block with no tag starts on the line itself, as the
// element of a sequence does.
func (w *mergedWriter) nested(v *mergedValue, indent int, compact bool) {
	switch {
	case !v.block():
		w.sb.WriteByte(' ')
		w.sb.WriteString(v.inline())
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

// layering is what the [Source] that [Layers] merge their Nodes into
// knows of those Nodes: the merged value, which names the layer each
// value came from, and the lowest and the highest layer. An error bound
// in the one document of that Source binds in one of the layers instead,
// since the merged text is no file of the caller. An error with a
// location binds in the layer [layering.layer] picks, and any other
// error in the lowest layer, whose name the Source has.
type layering struct {
	// The merged value, or nil when no layer holds a value.
	root *mergedValue
	// The lowest and the highest layer.
	lowest, top *Node
}

// home returns the layer an error binds in when it found no layer of its
// own, which is the lowest one, or nil for a nil l.
func (l *layering) home() *Node {
	if l == nil {
		return nil
	}

	return l.lowest
}

// layer returns the Node an error at path binds in, and path as the
// document of that Node reads it. The path starts at `$` and reads from
// the root of doc, the merged document. A nil l binds in doc with path
// as it is.
//
// The search follows path down the merged value for as long as the value
// holds it. The result is the layer of the last value it reaches, which
// is the highest layer whose document holds that value. For a path the
// merged value holds, that is the layer the value came from. For a path
// that names a key a mapping leaves out, it is the highest layer that
// holds the mapping. It is the highest layer of all when no layer holds a
// value.
func (l *layering) layer(doc *Node, path paths.Path) (*Node, paths.Path) {
	if l == nil {
		return doc, path
	}

	// The merged value stands at the root of its document, so the rest of
	// the path reads from the value of each layer.
	rel, _ := path.CutPrefix(paths.Doc())

	layer := l.top

	if at := l.root; at != nil {
		for sel := range rel.Selectors() {
			next := at.child(sel)
			if next == nil {
				break
			}

			at = next
		}

		layer = at.layer
	}

	return layer, layer.base.Join(rel)
}

// child returns the value sel selects in v, or nil when v holds none. The
// `~` selector of a key selects v itself, since the key of an entry
// belongs to the layer of its value.
func (v *mergedValue) child(sel paths.Selector) *mergedValue {
	switch sel.Kind {
	case paths.SelectorKey:
		return v

	case paths.SelectorChild:
		if i, ok := v.names[sel.Name]; ok {
			return v.entries[i].value
		}

	case paths.SelectorIndex:
		if v.kind == mergedSequence && sel.Index >= 0 && sel.Index < len(v.items) {
			return v.items[sel.Index]
		}

	case paths.SelectorChildAll, paths.SelectorIndexAll, paths.SelectorRecursive, paths.SelectorRecursiveAll:
	}

	return nil
}

// mergedLayers is what the Nodes of a [Layers] merge into.
type mergedLayers struct {
	// The root Node of the merged document.
	doc *Node
	// The lowest layer that holds no value a decode can read, such as one
	// that did not parse, or nil when every layer holds one.
	failed *Node
	// The error of failed.
	err error
}

// mergeLayers returns what nodes merge into, lowest first. A layer that
// holds no value a decode can read adds nothing to the merged document.
// Every use of the [Layers] shares the result, so ctx must never end.
//
// The document belongs to a [Source] of its own, which holds the merged
// value as YAML text below the preamble of the lowest layer. The Source
// takes its name, its file path, and its file system from the Source of
// that layer, with the settings [WithAllowDuplicateKeys],
// [WithAliasLimit], and [WithYAMLParserOptions] gave it. It takes no
// reference documents, since the merged value holds no alias. With no
// nodes, the Source is empty and has no name.
//
// A text that does not parse to one document gives that error, with the
// Node [noLayers] returns, so the layers then bind every error with no
// position.
func mergeLayers(ctx context.Context, nodes []*Node) mergedLayers {
	if len(nodes) == 0 {
		return mergedLayers{doc: noLayers()}
	}

	var (
		root   *mergedValue
		merged mergedLayers
	)

	for _, n := range nodes {
		v, err := newLayerReader(n).read(ctx)
		if err != nil {
			if merged.err == nil {
				merged.failed, merged.err = n, err
			}

			continue
		}

		root = root.merge(v)
	}

	lowest := nodes[0]

	var w mergedWriter

	w.sb.WriteString(preambleText(lowest))
	w.document(root)

	doc, err := newMergedDocument(lowest.source, w.sb.String())
	if err != nil {
		if merged.err == nil {
			merged.err = fmt.Errorf("merge layers: %w", err)
		}

		merged.doc = noLayers()

		return merged
	}

	doc.source.layers = &layering{root: root, lowest: lowest, top: nodes[len(nodes)-1]}
	merged.doc = doc

	return merged
}

// preambleText returns the text of the preamble of the document of n, as
// [Node.Preamble] describes it, ending in a line break. A document that
// did not parse gives none.
func preambleText(n *Node) string {
	if n.doc.err != nil {
		return ""
	}

	var sb strings.Builder

	for _, tk := range n.doc.tokens[:n.doc.preamble] {
		sb.WriteString(tk.Origin)
	}

	text := strings.TrimRight(sb.String(), " \t")
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}

	return text + "\n"
}

// newMergedDocument returns the root Node of the one document of a new
// [Source] that holds text, with the name, the file, and the settings of
// from, as [mergeLayers] lists them. It returns an error when text does
// not parse to one document.
func newMergedDocument(from *Source, text string) (*Node, error) {
	src := NewSourceFromString(text, func(c *sourceConfig) {
		*c = from.sourceConfig

		// The merged value holds no alias, so it reads no reference
		// document.
		c.references = nil

		// The parser options of from hold the one that allows a duplicate
		// key already. The constructor adds it again, which changes
		// nothing, and the copy keeps it out of the options of from.
		c.parserOpts = slices.Clone(c.parserOpts)
	})

	docs := src.documents()

	if len(docs) != 1 {
		return nil, src.Bind(fmt.Errorf("%w: merged layers", ErrMultipleDocuments))
	}

	if docs[0].doc.err != nil {
		return nil, docs[0].doc.err
	}

	return docs[0], nil
}
