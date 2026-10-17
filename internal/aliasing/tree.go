package aliasing

import (
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/fault"
	"go.jacobcolvin.com/niceyaml/internal/quantity"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Node is a node of a YAML document, such as a *niceyaml.Node, whose
// document state [StateOf] returns.
type Node interface {
	// AST returns the go-yaml node that the Node selects.
	AST() ast.Node
}

// CheckDecode returns a [*RatioError] when n holds an alias and aliases make
// up too much of what a decode of its document reads. The count covers the
// whole document, with each alias reading its content in full. A decode of a
// node below the root reads only the anchors that the node needs. But
// CheckDecode applies one limit per document, so every node that holds an
// alias gets the verdict of its document. A node without an alias decodes on
// its own and does not read anything twice, so it passes. A nil Node passes
// too. So does every node of a document that the limit does not apply to, as
// [Limited] reports.
//
// The count depends on the document alone, so the document keeps it, and
// a check of each item of a list counts the document once.
func CheckDecode(n Node) error {
	state := stateOf(n)
	if state == nil || !state.AliasLimit() || !holdsAlias(n.AST()) {
		return nil
	}

	return state.ExcessiveAliasing(func() error {
		return checkRead(state.Facts(), readValue, Budget{})
	})
}

// CheckDecodeText returns an error when n holds an alias and aliases make
// up too much of the text that the decoder writes out for the node. To decode
// a type with an UnmarshalText method, or one that reads YAML bytes, the
// decoder writes the node out as text with each alias in full. The count
// covers the whole document as [CheckDecode] does. It reads every node as
// text, so each scalar counts one node per byte of its text.
//
// The text must fit the [Budget] of the document as well as the limit.
// An [*ExpansionError] reports the first alias of the document that takes
// the text past a bound of the Budget. For a document past the limit and
// within the Budget, CheckDecodeText returns a [*RatioError]. A node
// without an alias passes. So
// do a nil Node and every node of a document that the limit does not
// apply to. The document keeps the result of the count, as it keeps the
// result of CheckDecode.
//
// A caller that decodes n into a type that [DecodesText] reports runs
// CheckDecodeText as well as CheckDecode.
func CheckDecodeText(n Node) error {
	state := stateOf(n)
	if state == nil || !state.AliasLimit() || !holdsAlias(n.AST()) {
		return nil
	}

	return state.ExcessiveTextAliasing(func() error {
		return checkRead(state.Facts(), readText, state.Budget())
	})
}

// Budget bounds the text that the aliases of a document write out where
// the decoder writes a node out as text. It applies in addition to the
// alias limit. The niceyaml package takes each bound from the byte limit
// and the depth limit of the source of the document. A bound of zero is
// off.
type Budget struct {
	// Bytes is the most text that the aliases of the document repeat, as
	// a writer in block style writes it. Such a writer puts each mapping
	// entry and each sequence element on a line of its own.
	// [CheckDecodeText] weighs each node that an alias repeats by its
	// text: the length in bytes of a scalar, or one byte for a mapping or
	// a sequence. It adds one byte to each node, and two bytes of
	// indentation for each level around the node in the document.
	Bytes int
	// Depth is the most levels that the document nests with each alias
	// written out in full. An alias nests the levels around it and the
	// levels that its content nests, through the aliases there.
	Depth int
}

// ExpansionError is the failure of a document whose aliases, written out
// in full, take its text past a bound of its [Budget]. It matches
// [aliaslimit.ErrExcessiveAliasing] and [fault.ErrLimit].
type ExpansionError struct {
	// Alias is the first alias, in the order of the document, that takes
	// the text past the bound.
	Alias *ast.AliasNode
	// Limit is the bound that the text passes, in bytes for the byte bound
	// and in levels for the depth bound.
	Limit int
	// Depth reports that the text passes the depth bound of the Budget
	// and not its byte bound.
	Depth bool
}

// Error returns the message of e, such as "aliases repeat more than
// 256 KiB of text" or "aliases nest the document more than 100 levels".
func (e *ExpansionError) Error() string {
	if e.Depth {
		return "aliases nest the document more than " + strconv.Itoa(e.Limit) + " levels"
	}

	return "aliases repeat more than " + quantity.Bytes(e.Limit) + " of text"
}

// Is reports whether target is [aliaslimit.ErrExcessiveAliasing] or
// [fault.ErrLimit].
func (e *ExpansionError) Is(target error) bool {
	return target == aliaslimit.ErrExcessiveAliasing || target == fault.ErrLimit
}

// RatioError is the failure of a document whose aliases make up too much
// of what a decode of it reads, as [aliaslimit.Excessive] reports. It
// matches [aliaslimit.ErrExcessiveAliasing].
type RatioError struct {
	// Alias is the alias outside the content of every other alias that
	// repeats the most nodes. When several aliases repeat that count,
	// Alias is the first of them outside every anchor, such as the value
	// that reads a chain of anchors, or else the first of them. Counts stop
	// at [aliaslimit.CountCap], so in a deep enough chain several aliases
	// repeat the same count.
	Alias *ast.AliasNode
}

// Error returns the message of e, "aliases expand the document past the
// alias limit".
func (e *RatioError) Error() string {
	return "aliases expand the document past the alias limit"
}

// Is reports whether target is [aliaslimit.ErrExcessiveAliasing].
func (e *RatioError) Is(target error) bool {
	return target == aliaslimit.ErrExcessiveAliasing
}

// Limited reports whether the alias limit applies to the document of n.
// It applies unless the source of the document turned it off with
// niceyaml.WithAliasLimit. [CheckDecode] and [CheckDecodeText] pass
// every node of a document that it does not apply to. A caller that
// applies [aliaslimit.Excessive] to a value that it decoded from n, as
// [HoldsReferenceAlias] describes, asks Limited first. The limit does not
// apply to a nil Node.
func Limited(n Node) bool {
	state := stateOf(n)

	return state != nil && state.AliasLimit()
}

// HoldsReferenceAlias reports whether the document of n holds a
// reference alias. A reference alias is an alias that does not have an
// anchor of its name before it in the document. A decode resolves such an
// alias only against a reference document, such as one of
// niceyaml.WithReferences. [CheckDecode] and [CheckDecodeText] cannot see
// a reference document, so they count the alias as one node whatever its
// anchor holds. A caller that reads a decoded value again at every alias
// applies [aliaslimit.Excessive] to that value when the document holds a
// reference alias and [Limited] reports that the limit applies. A nil
// Node does not hold one.
//
// The answer depends on the document alone, so HoldsReferenceAlias reads
// the [Facts] that the document keeps.
func HoldsReferenceAlias(n Node) bool {
	state := stateOf(n)
	if state == nil {
		return false
	}

	return state.Facts().ReferenceAlias
}

// stateOf returns the state of the document of n, or nil for a nil Node.
func stateOf(n Node) *State {
	if n == nil {
		return nil
	}

	return StateOf(n)
}

// checkRead returns the error of what a decode of the document of f reads
// as mode. It returns an [*ExpansionError] when the aliases of the
// document take it past a bound of budget. It returns a [*RatioError]
// when they make up too much of the read.
func checkRead(f *Facts, mode readMode, budget Budget) error {
	c := treeCounter{
		resolver:    f.resolver,
		nulls:       f.nulls,
		budget:      budget,
		sizes:       [readModes]map[ast.Node]int{{}, {}},
		open:        map[ast.Node]bool{},
		extents:     map[ast.Node]extent{},
		measuring:   map[ast.Node]bool{},
		collections: map[*ast.AliasNode]bool{},
		textKeys:    map[*ast.AliasNode]bool{},
		binaries:    map[*ast.AliasNode]bool{},
		repeated:    map[ast.Node]bool{},
	}

	c.count(f.body, true, mode)

	if c.overrun != nil {
		return c.overrun
	}

	if aliaslimit.Excessive(c.distinct, c.aliased) {
		return &RatioError{Alias: c.heaviest}
	}

	return nil
}

// holdsAlias reports whether node or any node below it is an alias.
func holdsAlias(node ast.Node) bool {
	if astnode.IsNil(node) {
		return false
	}

	var found aliasFinder

	ast.Walk(&found, node)

	return bool(found)
}

// aliasFinder is an [ast.Visitor] that records whether it visited an
// alias and stops the walk after it has.
type aliasFinder bool

// Visit implements [ast.Visitor].
func (f *aliasFinder) Visit(node ast.Node) ast.Visitor {
	if bool(*f) || astnode.IsNil(node) {
		return nil
	}

	if _, ok := node.(*ast.AliasNode); ok {
		*f = true

		return nil
	}

	return f
}

// readMode is how the decoder reads a node. The mode decides what a read
// of an alias to a scalar costs.
type readMode int

const (
	// A read into a value shares a scalar between its aliases.
	readValue readMode = iota

	// A read as text copies the text of a scalar at each alias to it.
	readText

	// The number of read modes.
	readModes
)

// treeCounter counts the nodes that a decode of a document tree reads,
// the way gopkg.in/yaml.v3 counts them for its alias limit. In that
// count, each alias reads the content that it refers to in full. The count
// covers a mapping that a merge key brings in, which the decoder reads
// again at every merge.
//
// The resolver binds each alias to its content. An alias that does not
// resolve counts as one unaliased node. So does an alias in the nulls
// set, which holds the aliases that the decoder reads as null, as
// [NewFacts] finds them. Either kind of alias counts as one node wherever
// the counter reads it, so the count does not depend on the order of the
// aliases.
//
// Where the decoder reads a node into a value, an alias to a scalar
// counts as one unaliased node, as a shared scalar does in a decoded
// value. The decoder writes some nodes out as text instead. It does so
// for a key that is or refers to a mapping, a sequence, or a !!binary
// scalar, and for the value under a text tag. A text tag is a !!str,
// !!int, !!bool, or !!binary tag, or any tag after a %TAG directive that
// redefines the "!!" handle.
//
// Each copy of a scalar in that text costs its length. In that text, a
// scalar therefore counts one node per byte of its text. An alias to one
// adds that count to the aliased nodes. The content of an anchor on a
// scalar counts its bytes too where the counter reads its definition. A
// scalar written out once therefore weighs what its anchor does.
//
// An alias to a text tag, such as *s for `&s !!str x`, counts the text
// under the tag even where the decoder reads the alias into a value.
// Some types, such as a named string type, cannot take the value that the
// decoder converted for the anchor. Where an alias decodes into such a
// type, the decoder converts the text under the tag again at each alias.
//
// An alias to a tag over another alias, such as *s for `&s !foo *k`,
// refers to what *k refers to. A mapping under k then counts in full at
// each *s, and a key *s counts as text when *k refers to a !!binary
// scalar.
//
// An alias that refers to a !!binary scalar counts the text of the
// scalar even where the decoder reads the alias into a value. A decode
// shares the bytes between such aliases, and the schema validator spells
// them as text again at each one. The !!binary tag can lie under another
// tag and anchor, as in `&s !foo &b !!binary x`, or behind a tagged
// alias, as it does for *s with `&s !foo *b`.
//
// The distinct field counts the nodes of the tree once each, and an alias
// in the tree is one of them. The aliased field counts the nodes that the
// aliases in the tree repeat. The sizes maps hold the size of the content
// of each alias after the counter has read it, one map for each read
// mode. A chain of nested aliases therefore costs one read per anchor.
// The open map holds the content of each alias that the counter is
// reading. An alias that leads back into that content then counts as one
// node, and the count ends.
//
// For each alias that [treeCounter.reaches] has followed, the collections
// map holds whether the alias refers to a mapping or a sequence. The
// textKeys map holds whether the decoder writes out a key that is the
// alias as text. The binaries map holds whether such an alias refers to a
// !!binary scalar. A chain of tagged aliases then costs one walk, however
// many aliases lead into it. The repeated map holds what
// [treeCounter.repeats] has found for the content of each alias.
//
// The budget bounds the text that the aliases repeat and the levels that
// they add to the tree, as [Budget] describes. A bound of zero is off. The
// level field holds the levels around the node that the counter reads in
// the tree, outside the content of every alias. The written field holds
// the text that the aliases of the tree repeat so far, as the byte bound
// weighs it.
//
// The extents map holds the [extent] of the content of each alias, after
// [treeCounter.measure] has read it. The measuring map holds the content
// of each alias that measure is reading. The overrun field holds the
// error of the first alias that takes the tree past a bound of the
// budget.
//
// The heaviest field holds the alias of the tree that repeats the most
// nodes so far, as [RatioError] picks it. The heaviestSize field holds
// what that alias repeats. The anchored field counts the anchors around
// the node that the counter reads in the tree, outside the content of
// every alias. The heaviestAnchored field holds whether an anchor lies
// around heaviest.
type treeCounter struct {
	resolver    *paths.Resolver
	nulls       map[*ast.AliasNode]bool
	sizes       [readModes]map[ast.Node]int
	open        map[ast.Node]bool
	extents     map[ast.Node]extent
	measuring   map[ast.Node]bool
	collections map[*ast.AliasNode]bool
	textKeys    map[*ast.AliasNode]bool
	binaries    map[*ast.AliasNode]bool
	repeated    map[ast.Node]bool
	overrun     *ExpansionError
	heaviest    *ast.AliasNode
	budget      Budget
	level       int
	written     int
	distinct    int
	aliased     int
	anchored    int

	heaviestSize     int
	heaviestAnchored bool
}

// count returns the number of nodes that a decode of node reads as mode.
// A scalar counts one, or its length in bytes when read as text. A
// mapping or a sequence counts one plus the count of each key, value, or
// element in it. An anchor, a tag, or the `?` of an explicit key counts
// what it wraps. With top set, node lies outside the content of every
// alias, and count adds its nodes to distinct and the nodes that its
// aliases repeat to aliased.
func (c *treeCounter) count(node ast.Node, top bool, mode readMode) int {
	if astnode.IsNil(node) {
		return 0
	}

	switch n := node.(type) {
	case *ast.AliasNode:
		return c.alias(n, top, mode)
	case *ast.AnchorNode:
		return c.anchor(n, top, mode)
	case *ast.TagNode:
		return c.count(n.Value, top, tagMode(n, mode))
	case *ast.MappingKeyNode:
		return c.count(n.Value, top, mode)
	case *ast.CommentGroupNode:
		return 0
	}

	size := 1
	if mode == readText && !isCollection(node) {
		size = textSize(node)
	}

	if top {
		c.distinct = aliaslimit.AddCapped(c.distinct, size)
	}

	// A mapping or a sequence of the tree opens a level around the nodes
	// in it.
	opens := top && isCollection(node)
	if opens {
		c.level++
	}

	switch n := node.(type) {
	case *ast.MappingNode:
		for _, entry := range n.Values {
			size = aliaslimit.AddCapped(size, c.entry(entry, top, mode))
		}

	case *ast.MappingValueNode:
		size = aliaslimit.AddCapped(size, c.entry(n, top, mode))

	case *ast.SequenceNode:
		for _, elem := range n.Values {
			size = aliaslimit.AddCapped(size, c.count(elem, top, mode))
		}
	}

	if opens {
		c.level--
	}

	return size
}

// entry returns the number of nodes that a decode of the key and the
// value of a mapping entry reads, as [treeCounter.count] counts them. The key
// counts as text where the decoder writes it out as text.
func (c *treeCounter) entry(entry *ast.MappingValueNode, top bool, mode readMode) int {
	if entry == nil {
		return 0
	}

	keyMode := mode
	if c.keyAsText(entry.Key) {
		keyMode = readText
	}

	return aliaslimit.AddCapped(c.count(entry.Key, top, keyMode), c.count(entry.Value, top, mode))
}

// keyAsText reports whether the decoder writes key out as text. It does
// so for a key that decodes to neither a string nor a number. Such a key
// is a mapping, a sequence, or a !!binary scalar, or an alias that refers
// to one through the aliases that [treeCounter.reaches] follows.
func (c *treeCounter) keyAsText(key ast.Node) bool {
	if astnode.IsNil(key) {
		return false
	}

	return c.reaches(key, isTextKey, c.textKeys)
}

// tagMode returns how the decoder reads the value under tag when it reads
// the tagged node as mode.
func tagMode(tag *ast.TagNode, mode readMode) readMode {
	if isTextTag(tag) {
		return readText
	}

	return mode
}

// isTextTag reports whether node is a text tag: a !!str, !!int, !!bool,
// or !!binary tag, or any tag after a %TAG directive that redefines the
// "!!" handle. The decoder writes out the value under such a tag as text
// to convert it.
func isTextTag(node ast.Node) bool {
	tag, ok := node.(*ast.TagNode)
	if !ok || tag.Start == nil {
		return false
	}

	if tag.Directive != nil {
		return true
	}

	switch token.ReservedTagKeyword(tag.Start.Value) {
	case token.StringTag, token.IntegerTag, token.BooleanTag, token.BinaryTag:
		return true
	default:
		return false
	}
}

// anchor returns the number of nodes that a decode of the content of the
// anchor reads, as [treeCounter.count] counts them. With top set, the
// counter reads the definition of the anchor. The content of an anchor on
// a scalar counts as text there, because an alias to it might copy that
// text. Elsewhere, such as in an alias to a mapping around the anchor, the
// decoder reads the anchored scalar as it reads any other. The scalar
// then counts as mode reads it. An anchor on an alias does not define
// content of its own, so its alias counts the same as an alias without
// the anchor.
func (c *treeCounter) anchor(anchor *ast.AnchorNode, top bool, mode readMode) int {
	_, onAlias := astnode.Content(anchor.Value).(*ast.AliasNode)
	if top && !onAlias && !isCollection(anchor.Value) {
		mode = readText
	}

	if !top {
		return c.count(anchor.Value, top, mode)
	}

	c.anchored++
	size := c.count(anchor.Value, top, mode)
	c.anchored--

	return size
}

// alias returns the number of nodes that a decode of the alias reads as
// mode. The number is one for an alias that the decoder reads as null.
// Otherwise it is the size of the mapping or sequence that the alias
// refers to, or the length of the text of a scalar that it refers to when
// read as text.
//
// An alias to a text tag, such as a !!str tag or any tag after a %TAG
// directive that redefines the "!!" handle, reads the text under the tag
// in either mode. So does an alias that refers to a !!binary scalar under
// other tags and anchors or through a tagged alias, as
// [treeCounter.holdsBinary] finds it. Any other alias counts one. An
// alias to a tag over another alias, as in `&s !foo *k`, refers to what
// that alias refers to.
//
// With top set, the alias lies outside the content of every other alias.
// It is then a node of the tree itself, so alias adds one to distinct and
// adds the size of what the alias repeats to aliased. It then checks the
// alias against the budget with [treeCounter.spend].
func (c *treeCounter) alias(alias *ast.AliasNode, top bool, mode readMode) int {
	target, err := c.resolver.Deref(alias)
	if err != nil || c.nulls[alias] || c.open[target] || (mode == readValue && !c.repeats(target)) {
		if top {
			c.distinct = aliaslimit.AddCapped(c.distinct, 1)
		}

		return 1
	}

	size, ok := c.sizes[mode][target]
	if !ok {
		c.open[target] = true
		size = c.count(target, false, mode)
		delete(c.open, target)

		c.sizes[mode][target] = size
	}

	if top {
		// The alias is a node of its own, as gopkg.in/yaml.v3 counts it.
		c.distinct = aliaslimit.AddCapped(c.distinct, 1)
		c.aliased = aliaslimit.AddCapped(c.aliased, size)

		c.weigh(alias, size)
		c.spend(alias)
	}

	return size
}

// weigh records alias, an alias of the tree that repeats size nodes, as
// the heaviest when it repeats more nodes than the heaviest so far. On a
// tie, an alias outside every anchor takes the place of one inside an
// anchor, and otherwise the earlier alias stays.
func (c *treeCounter) weigh(alias *ast.AliasNode, size int) {
	anchored := c.anchored > 0

	switch {
	case c.heaviest == nil, size > c.heaviestSize:
	case size == c.heaviestSize && c.heaviestAnchored && !anchored:
	default:
		return
	}

	c.heaviest, c.heaviestSize, c.heaviestAnchored = alias, size, anchored
}

// spend records an [*ExpansionError] at alias, an alias of the tree, when
// alias is the first alias that takes the tree past a bound of the budget.
// It adds the text that alias repeats to written, as
// [extent.text] weighs the content of alias at the level of alias. The
// written field must not pass the byte bound. The levels around alias
// and the levels that its content nests must not pass the depth bound.
func (c *treeCounter) spend(alias *ast.AliasNode) {
	if c.overrun != nil || c.budget == (Budget{}) {
		return
	}

	content := c.aliasExtent(alias)

	c.written = aliaslimit.AddCapped(c.written, content.text(c.level))
	if c.budget.Bytes > 0 && c.written > c.budget.Bytes {
		c.overrun = &ExpansionError{Alias: alias, Limit: c.budget.Bytes}

		return
	}

	if c.budget.Depth > 0 && c.level+content.depth > c.budget.Depth {
		c.overrun = &ExpansionError{Alias: alias, Limit: c.budget.Depth, Depth: true}
	}
}

// extent is what a node holds with each alias in it written out in full.
// The budget weighs the extent. A writer can give the node the text of
// block style, with each mapping entry and each sequence element on a
// line of its own. It can also copy the node as the document spells it,
// with the text of each alias in place of the alias.
type extent struct {
	// The levels that the node nests, one for each mapping or sequence on
	// the way from the node to its deepest value.
	depth int
	// The text of the node in block style without the indentation and the
	// punctuation of its lines. It counts the length in bytes of each
	// scalar and tag, and two bytes for each mapping or sequence.
	units int
	// The lines of the node in block style. Each key and each other leaf
	// that is not the value of a mapping entry starts a line. A leaf is a
	// scalar, an empty mapping, or an empty sequence.
	lines int
	// The levels between the node and the leaf that starts each of its
	// lines, summed.
	levels int
	// The length in bytes of the text of the node as the document spells
	// it. That text holds the comments and the spaces between its tokens,
	// and the text of each alias in it.
	spelled int
	// The lines of that text.
	spelledLines int
	// Whether the node is a leaf.
	leaf bool
}

// text returns the bytes that a writer of the node of e gives it when
// level mappings and sequences lie around it, as [Budget] weighs them.
// The result is the larger of the text in block style and the text as the
// document spells it. In block style, each line weighs one byte of
// punctuation and two bytes of indentation for each level around the
// leaf that starts it.
func (e *extent) text(level int) int {
	perLine := mulCapped(e.lines, aliaslimit.AddCapped(1, mulCapped(2, level)))
	block := aliaslimit.AddCapped(aliaslimit.AddCapped(e.units, perLine), mulCapped(2, e.levels))

	return max(block, e.spelled)
}

// nest adds child, a key, a value, or an element one level inside the
// mapping or the sequence of e.
func (e *extent) nest(child extent) {
	e.depth = max(e.depth, child.depth+1)
	e.units = aliaslimit.AddCapped(e.units, child.units)
	e.lines = aliaslimit.AddCapped(e.lines, child.lines)
	e.levels = aliaslimit.AddCapped(e.levels, aliaslimit.AddCapped(child.levels, child.lines))
	e.spelled = aliaslimit.AddCapped(e.spelled, child.spelled)
	e.spelledLines = aliaslimit.AddCapped(e.spelledLines, child.spelledLines)
}

// measure returns the [extent] of node. A mapping or a sequence nests one
// level more than the deepest key, value, or element in it. A scalar
// starts one line and does not nest a level. An anchor, a tag, or the `?`
// of an explicit key holds what it wraps, and a tag adds its own text. An
// alias holds the extent of its content, as [treeCounter.aliasExtent]
// finds it. The spelled text of node counts only the aliases in it, and
// aliasExtent adds the text of the content that holds them.
func (c *treeCounter) measure(node ast.Node) extent {
	if astnode.IsNil(node) {
		return extent{}
	}

	switch n := node.(type) {
	case *ast.AliasNode:
		return c.aliasExtent(n)
	case *ast.AnchorNode:
		return c.measure(n.Value)
	case *ast.MappingKeyNode:
		return c.measure(n.Value)
	case *ast.CommentGroupNode:
		return extent{}
	case *ast.TagNode:
		e := c.measure(n.Value)
		if n.Start != nil {
			e.units = aliaslimit.AddCapped(e.units, len(n.Start.Value)+1)
		}

		return e
	}

	if !isCollection(node) {
		return extent{units: textSize(node), lines: 1, leaf: true}
	}

	e := extent{depth: 1, units: 2}

	switch n := node.(type) {
	case *ast.MappingNode:
		for _, entry := range n.Values {
			c.measureEntry(&e, entry)
		}

	case *ast.MappingValueNode:
		c.measureEntry(&e, n)

	case *ast.SequenceNode:
		for _, elem := range n.Values {
			e.nest(c.measure(elem))
		}
	}

	if e.lines == 0 {
		e.lines, e.leaf = 1, true
	}

	return e
}

// measureEntry nests the key and the value of entry, an entry of the
// mapping of e, in e. A leaf value goes on the line of its key, so it
// does not start a line of its own.
func (c *treeCounter) measureEntry(e *extent, entry *ast.MappingValueNode) {
	if entry == nil {
		return
	}

	e.nest(c.measure(entry.Key))

	value := c.measure(entry.Value)
	if value.leaf {
		value.lines, value.levels = 0, 0
	}

	e.nest(value)
}

// aliasExtent returns the [extent] of the content of alias. An alias holds
// a leaf of one byte when it does not resolve, when the decoder reads it
// as null, or when it leads back into content that [treeCounter.measure]
// is reading. A writer that copies the content of an alias as the
// document spells it indents each line of that text to the line of the
// alias. The spelled text of alias grows by that indentation. The
// extents map holds the extent of the content of each alias, so a chain
// of nested aliases costs one walk per anchor.
func (c *treeCounter) aliasExtent(alias *ast.AliasNode) extent {
	target, err := c.resolver.Deref(alias)
	if err != nil || c.nulls[alias] || c.measuring[target] {
		return extent{units: 1, lines: 1, leaf: true}
	}

	e, ok := c.extents[target]
	if !ok {
		c.measuring[target] = true
		e = c.measure(target)
		delete(c.measuring, target)

		e.spelled, e.spelledLines = spell(target, e.spelled, e.spelledLines)

		c.extents[target] = e
	}

	if tk := alias.GetToken(); tk != nil && tk.Position != nil {
		e.spelled = aliaslimit.AddCapped(e.spelled, mulCapped(e.spelledLines, tk.Position.IndentNum))
	}

	return e
}

// spell returns the length in bytes and the lines of the text of node as
// the document spells it, comments and spaces included. The origins of
// the tokens of node hold that text, from the first token to the last.
// It adds them to bytes and lines, the text and the lines of the aliases
// in node.
func spell(node ast.Node, bytes, lines int) (int, int) {
	first, last := astnode.Bounds(node)
	if len(first) == 0 || len(last) == 0 {
		return bytes, lines
	}

	end := last[0].Position.Offset
	lines = aliaslimit.AddCapped(lines, 1)

	for tk := first[0]; tk != nil && tk.Position != nil && tk.Position.Offset <= end; tk = tk.Next {
		bytes = aliaslimit.AddCapped(bytes, len(tk.Origin))
		lines = aliaslimit.AddCapped(lines, strings.Count(tk.Origin, "\n"))
	}

	return bytes, lines
}

// mulCapped returns a*b, or [aliaslimit.CountCap] when the product is
// more than CountCap. Both a and b fall between zero and CountCap,
// inclusive.
func mulCapped(a, b int) int {
	if b != 0 && a > aliaslimit.CountCap/b {
		return aliaslimit.CountCap
	}

	return a * b
}

// repeats reports whether each alias to target, the content of an
// anchor, reads more than a shared scalar where the decoder reads the
// alias into a value. It does for a text tag, for a !!binary scalar, and
// for a mapping or a sequence. The answer depends on target alone, so
// the counter keeps it. The counter walks the anchors and tags of target
// once, however many aliases refer to the anchor.
func (c *treeCounter) repeats(target ast.Node) bool {
	repeated, ok := c.repeated[target]
	if !ok {
		repeated = isTextTag(target) || c.holdsBinary(target) || c.holdsCollection(target)
		c.repeated[target] = repeated
	}

	return repeated
}

// textSize returns the length in bytes of the text of the scalar that
// node holds, but at least one.
func textSize(node ast.Node) int {
	size := 0

	switch n := astnode.Content(node).(type) {
	case *ast.StringNode:
		size = len(n.Value)

	case *ast.LiteralNode:
		if n.Value != nil {
			size = len(n.Value.Value)
		}

	case ast.ScalarNode:
		if tk := n.GetToken(); tk != nil {
			size = len(tk.Value)
		}
	}

	return max(size, 1)
}

// holdsCollection reports whether node holds a mapping or a sequence
// under its anchors and tags, or refers to one through the aliases that
// [treeCounter.reaches] follows.
func (c *treeCounter) holdsCollection(node ast.Node) bool {
	return c.reaches(node, isCollection, c.collections)
}

// holdsBinary reports whether node holds a !!binary tag among its
// anchors and tags, or refers to one through the aliases that
// [treeCounter.reaches] follows.
func (c *treeCounter) holdsBinary(node ast.Node) bool {
	return c.reaches(node, underBinary, c.binaries)
}

// reaches reports whether match holds for node or for the content of an
// alias that node leads to. It follows the alias under the anchors and
// tags of node to the content of its anchor, and goes on from there in
// the same way. [paths.Resolver.Deref] stops at a tag. An alias to an
// anchor such as `&s !foo *k` therefore gives the tag over *k, and
// reaches goes on to the content that *k refers to. It stops at an alias
// that does not resolve, at one that the decoder reads as null, and at
// one that it has followed before.
//
// For each alias that reaches has followed with match, the results map
// holds whether match holds for the content that the alias leads to. That
// answer does not depend on where the walk began. So reaches records it
// for every alias on the way and stops at an alias that it has recorded. It
// records an alias as false before it follows it, so a walk that returns
// to that alias finds false.
func (c *treeCounter) reaches(node ast.Node, match func(ast.Node) bool, results map[*ast.AliasNode]bool) bool {
	if match(node) {
		return true
	}

	var (
		followed []*ast.AliasNode
		found    bool
	)

	for {
		alias, ok := astnode.Content(node).(*ast.AliasNode)
		if !ok || c.nulls[alias] {
			break
		}

		if known, ok := results[alias]; ok {
			found = known

			break
		}

		results[alias] = false
		followed = append(followed, alias)

		target, err := c.resolver.Deref(alias)
		if err != nil {
			break
		}

		if match(target) {
			found = true

			break
		}

		node = target
	}

	for _, alias := range followed {
		results[alias] = found
	}

	return found
}

// isTextKey reports whether node holds a mapping or a sequence or is a
// !!binary tag, either of which the decoder writes out as text in a key.
func isTextKey(node ast.Node) bool {
	return isCollection(node) || isBinary(node)
}

// isBinary reports whether node is a !!binary tag, whose value the
// decoder reads as bytes.
func isBinary(node ast.Node) bool {
	tag, ok := node.(*ast.TagNode)

	return ok && tag.Start != nil && token.ReservedTagKeyword(tag.Start.Value) == token.BinaryTag
}

// underBinary reports whether one of the tags around the content of node
// is a !!binary tag. It looks through the anchors and tags of node, so
// it finds the tag in `!foo &b !!binary x`.
func underBinary(node ast.Node) bool {
	for !astnode.IsNil(node) {
		switch n := node.(type) {
		case *ast.AnchorNode:
			node = n.Value

		case *ast.TagNode:
			if isBinary(n) {
				return true
			}

			node = n.Value

		default:
			return false
		}
	}

	return false
}

// isCollection reports whether node holds a mapping or a sequence under
// its anchors and tags.
func isCollection(node ast.Node) bool {
	switch astnode.Content(node).(type) {
	case *ast.MappingNode, *ast.MappingValueNode, *ast.SequenceNode:
		return true
	default:
		return false
	}
}
