package aliasing

import (
	"errors"
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
// too. So does every node of a document whose source turns the limit off
// with niceyaml.WithAliasLimit.
//
// An alias that only a reference document resolves counts what the
// anchors of its name hold there, as [ReferenceWeights] weighs them. Each
// reference anchor that the aliases of the document reach also counts
// once, as the definition of an anchor counts in its own document. A
// reached anchor inside another reached anchor counts as part of the
// outer anchor.
//
// A document thus gets the verdict that it gets with those anchors defined
// inside it, unless the anchors form a loop of names or share a name.
// Where the reference documents define one anchor name more than once,
// each alias of the name weighs the largest anchor that a decode can read
// for it. A document whose decode reads a smaller anchor of the name can
// thus fail. ReferenceWeights describes what a loop and a shared name
// weigh.
//
// The count depends on the document and its reference documents alone, so
// the document keeps it. A check of each item of a list thus counts the
// document once.
func CheckDecode(n Node) error {
	state := stateOf(n)
	if state == nil || !state.AliasLimit() || !holdsAlias(n.AST()) {
		return nil
	}

	return state.ExcessiveAliasing(func() error {
		return checkRead(state, readValue, Budget{})
	})
}

// PastLimit reports whether aliases make up too much of what a decode of
// the whole document of n reads, as [CheckDecode] counts it. It reads the
// verdict of the document whether or not n holds an alias, so it reports
// true for every node of a document past the limit. It reports false for a
// nil Node, for a document without an alias, and for every node of a
// document whose source turns the limit off with niceyaml.WithAliasLimit.
// The document keeps the count, as it keeps the count of CheckDecode, so
// only the first call of either counts.
//
// A reader that reads the content of the aliases of a document in its own
// way, such as a lookup of the entries that a `<<` merge key brings in,
// asks PastLimit first. What it reads is part of what the count weighs, so
// it does not read the content of an alias of a document past the limit.
func PastLimit(n Node) bool {
	state := stateOf(n)
	if state == nil || !state.AliasLimit() || len(state.Facts().Aliases) == 0 {
		return false
	}

	return state.ExcessiveAliasing(func() error {
		return checkRead(state, readValue, Budget{})
	}) != nil
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
// An alias that only a reference document resolves writes out the text
// that the anchors of its name hold there. That text counts against the
// limit and the Budget as the text of an anchor of the document does. An
// alias inside a reference anchor is not an alias of the document. Its
// text spends the Budget only as a part of that text.
//
// A caller that decodes n into a type that [DecodesText] reports runs
// CheckDecodeText as well as CheckDecode.
func CheckDecodeText(n Node) error {
	state := stateOf(n)
	if state == nil || !state.AliasLimit() || !holdsAlias(n.AST()) {
		return nil
	}

	return state.ExcessiveTextAliasing(func() error {
		return checkRead(state, readText, state.Budget())
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

// stateOf returns the state of the document of n, or nil for a nil Node.
func stateOf(n Node) *State {
	if n == nil {
		return nil
	}

	return StateOf(n)
}

// checkRead returns the error of what a decode of the document of state
// reads as mode. It returns an [*ExpansionError] when the aliases of the
// document take it past a bound of budget. It returns a [*RatioError]
// when they make up too much of the read.
func checkRead(state *State, mode readMode, budget Budget) error {
	f := state.Facts()

	c := newTreeCounter(f.nulls)
	c.resolver = f.resolver
	c.reference = state.References().lookup()
	c.reached = map[*refWeight]bool{}
	c.walked = map[*refAnchor]bool{}
	c.mode = mode
	c.budget = budget

	c.count(f.body, true, mode)
	c.addReached()

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
// The tree is the body of a document, or an anchor of a reference
// document that [ReferenceWeights] weighs. In a document, the resolver
// binds each alias to its content. An alias that the resolver leaves to
// the reference documents reads the anchors of its name there, and the
// reference function returns their [refWeight]. Every alias of a
// reference anchor reads by name, so its counter does not hold a
// resolver. That holds for an alias inside an anchor of its own name
// too, so the nulls set of such a counter is empty.
// [treeCounter.resolve] finds what each alias reads.
//
// An alias that does not resolve counts as one unaliased node. So does an
// alias in the nulls set, which holds the aliases that the decoder reads
// as null, as [NewFacts] finds them in a document. Either kind of alias
// counts as one node wherever the counter reads it, so the count does not
// depend on the order of the aliases.
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
// The reached set holds the weight of each name of the reference
// documents whose anchor the aliases of a document reach, as
// [treeCounter.reach] finds them. The walked set holds each reference
// anchor whose aliases reach has read. After the counter has read the
// document, [treeCounter.addReached] adds the reads of those anchors to
// the count. The mode field holds the mode that the document reads as. A
// counter of a reference anchor has a nil reached set and does not add
// any.
//
// A counter of a reference anchor shares the [anchorCounts] of its
// [ReferenceWeights] in the kept field. It keeps there what it found for
// each anchor of the tree, and takes what the counter of another anchor
// kept. The kept field of the counter of a document is nil.
//
// For each alias that [treeCounter.reaches] has followed, the traits maps
// hold whether the content that the alias leads to has each [trait]. A
// chain of tagged aliases then costs one walk, however many aliases lead
// into it. The repeated map holds what [treeCounter.repeats] has found
// for the content of each alias.
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
	resolver  *paths.Resolver
	reference func(name string) *refWeight
	kept      *anchorCounts
	nulls     map[*ast.AliasNode]bool
	reached   map[*refWeight]bool
	walked    map[*refAnchor]bool
	sizes     [readModes]map[ast.Node]int
	open      map[ast.Node]bool
	extents   map[ast.Node]extent
	measuring map[ast.Node]bool
	traits    [traits]map[*ast.AliasNode]bool
	repeated  map[ast.Node]bool
	overrun   *ExpansionError
	heaviest  *ast.AliasNode
	budget    Budget
	mode      readMode
	level     int
	written   int
	distinct  int
	aliased   int
	anchored  int

	heaviestSize     int
	heaviestAnchored bool
}

// newTreeCounter creates a new [*treeCounter] of a tree whose aliases in
// nulls the decoder reads as null. The counter does not resolve any alias
// until its caller sets the resolver, the reference function, or both.
func newTreeCounter(nulls map[*ast.AliasNode]bool) *treeCounter {
	return &treeCounter{
		nulls:     nulls,
		sizes:     [readModes]map[ast.Node]int{{}, {}},
		open:      map[ast.Node]bool{},
		extents:   map[ast.Node]extent{},
		measuring: map[ast.Node]bool{},
		traits:    [traits]map[*ast.AliasNode]bool{{}, {}, {}},
		repeated:  map[ast.Node]bool{},
	}
}

// resolve returns what alias reads. It reports false for an alias that
// the counter takes as one node, which is an alias that does not resolve
// or one that the decoder reads as null. An alias reads one of two things:
//
//   - The content of an anchor of the tree, which is the first result.
//     That content can be nil.
//   - The anchors of its name in the reference documents, whose weight is
//     the second result.
//
// In a document, the resolver follows a chain such as *a for `&a *b` to
// the content at its end. Where the chain ends at an alias that only a
// reference document resolves, the alias reads the weight of that name.
// Where a tag lies on the way, as in `&a !!str *b`, the alias reads the
// tag as content, and the counter resolves the alias under the tag when
// it reads that content.
func (c *treeCounter) resolve(alias *ast.AliasNode) (ast.Node, *refWeight, bool) {
	if c.nulls[alias] {
		return nil, nil, false
	}

	if c.resolver == nil {
		ref := c.named(alias)

		return nil, ref, ref != nil
	}

	target, err := c.resolver.Deref(alias)
	if err == nil {
		return target, nil, true
	}

	if !errors.Is(err, paths.ErrReferenceAlias) {
		return nil, nil, false
	}

	// The resolver followed the anchors of the document from alias to an
	// alias that a reference document resolves. The loop takes the same
	// steps and stops at that alias, or at a tag before it.
	for {
		anchor, err := c.resolver.Anchor(alias)
		if err != nil {
			ref := c.named(alias)

			return nil, ref, ref != nil
		}

		content := anchor.Value
		for inner, ok := content.(*ast.AnchorNode); ok; inner, ok = content.(*ast.AnchorNode) {
			content = inner.Value
		}

		next, ok := content.(*ast.AliasNode)
		if !ok {
			return content, nil, true
		}

		alias = next
	}
}

// named returns the weight of the anchors that the reference documents
// hold for the name of alias, or nil when they do not hold one.
func (c *treeCounter) named(alias *ast.AliasNode) *refWeight {
	name, ok := astnode.Name(alias.Value)
	if !ok || c.reference == nil {
		return nil
	}

	return c.reference(name)
}

// reach records the reference anchor of ref as an anchor that the aliases
// of a document reach. The decoder reads each such anchor once, before it
// reads the document. It also reads each anchor that an alias under such
// an anchor names, so reach records those the same way. The walked set
// holds each anchor whose aliases reach has read, so it reads the aliases
// under each reference anchor once for a document.
func (c *treeCounter) reach(ref *refWeight) {
	if c.reached == nil || c.reached[ref] {
		return
	}

	c.reached[ref] = true

	// A stack keeps a long chain of anchors out of the call stack.
	for pending := []*refAnchor{ref.read}; len(pending) > 0; {
		last := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		if c.walked[last] {
			continue
		}

		c.walked[last] = true

		for _, item := range last.items {
			if item.inner != nil {
				pending = append(pending, item.inner)

				continue
			}

			next := c.reference(item.name)
			if next != nil && !c.reached[next] {
				c.reached[next] = true
				pending = append(pending, next.read)
			}
		}
	}
}

// addReached adds the reads of the reference anchors in the reached set
// to the count of a document. The read of an anchor counts as the
// definition of the anchor counts in its own document. That read holds
// the nodes of every anchor inside the anchor. A reached anchor inside
// another reached anchor therefore does not add a read of its own, and
// each node of the reference documents counts once.
func (c *treeCounter) addReached() {
	// The anchors inside a reached anchor, at every depth.
	inside := map[*refAnchor]bool{}

	var pending []*refAnchor

	for ref := range c.reached {
		for pending = ref.read.appendInner(pending[:0]); len(pending) > 0; {
			last := pending[len(pending)-1]
			pending = pending[:len(pending)-1]

			// The walk from another reached anchor has marked the
			// anchors inside a marked anchor.
			if !inside[last] {
				inside[last] = true
				pending = last.appendInner(pending)
			}
		}
	}

	for ref := range c.reached {
		if inside[ref.read] {
			continue
		}

		c.distinct = aliaslimit.AddCapped(c.distinct, ref.distinct[c.mode])
		c.aliased = aliaslimit.AddCapped(c.aliased, ref.aliased[c.mode])
	}
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

	return c.reaches(key, traitTextKey)
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
//
// A counter of a reference anchor keeps what it found for anchor in its
// [anchorCounts]. A later count of an anchor around anchor takes that
// result and does not read the nodes under anchor again.
func (c *treeCounter) anchor(anchor *ast.AnchorNode, top bool, mode readMode) int {
	if c.kept == nil {
		return c.countAnchor(anchor, top, mode)
	}

	key := anchorCount{anchor: anchor, mode: mode, top: top}

	found, ok := c.kept.counts[key]
	if !ok {
		distinct, aliased := c.distinct, c.aliased
		c.distinct, c.aliased = 0, 0

		found.size = c.countAnchor(anchor, top, mode)
		found.distinct, found.aliased = c.distinct, c.aliased
		c.kept.counts[key] = found

		c.distinct, c.aliased = distinct, aliased
	}

	c.distinct = aliaslimit.AddCapped(c.distinct, found.distinct)
	c.aliased = aliaslimit.AddCapped(c.aliased, found.aliased)

	return found.size
}

// countAnchor counts the content of anchor for [treeCounter.anchor].
func (c *treeCounter) countAnchor(anchor *ast.AnchorNode, top bool, mode readMode) int {
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
// An alias that reads the reference documents counts the size that their
// [refWeight] holds for mode, by the same rules.
//
// With top set, the alias lies outside the content of every other alias.
// It is then a node of the tree itself, so alias adds one to distinct and
// adds the size of what the alias repeats to aliased. It then checks the
// alias against the budget with [treeCounter.spend]. An alias that reads
// the reference documents also records their anchors as reached, with
// [treeCounter.reach].
func (c *treeCounter) alias(alias *ast.AliasNode, top bool, mode readMode) int {
	target, ref, ok := c.resolve(alias)

	if top && ref != nil {
		c.reach(ref)
	}

	if !ok || (ref == nil && c.open[target]) || (mode == readValue && !c.repeats(target, ref)) {
		if top {
			c.distinct = aliaslimit.AddCapped(c.distinct, 1)
		}

		return 1
	}

	size := c.contentSize(target, ref, mode)

	if top {
		// The alias is a node of its own, as gopkg.in/yaml.v3 counts it.
		c.distinct = aliaslimit.AddCapped(c.distinct, 1)
		c.aliased = aliaslimit.AddCapped(c.aliased, size)

		c.weigh(alias, size)
		c.spend(alias)
	}

	return size
}

// contentSize returns the number of nodes that an alias repeats as mode.
// The alias reads target, the content of an anchor of the tree, or ref,
// the weight of the anchors of its name in the reference documents. The
// counter reads the content of each anchor of the tree once for each
// mode.
func (c *treeCounter) contentSize(target ast.Node, ref *refWeight, mode readMode) int {
	if ref != nil {
		return ref.sizes[mode]
	}

	size, ok := c.sizes[mode][target]
	if !ok {
		c.open[target] = true
		size = c.count(target, false, mode)
		delete(c.open, target)

		c.sizes[mode][target] = size
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

	if c.budget.Depth > 0 && aliaslimit.AddCapped(c.level, content.depth) > c.budget.Depth {
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
	e.depth = max(e.depth, aliaslimit.AddCapped(child.depth, 1))
	e.units = aliaslimit.AddCapped(e.units, child.units)
	e.lines = aliaslimit.AddCapped(e.lines, child.lines)
	e.levels = aliaslimit.AddCapped(e.levels, aliaslimit.AddCapped(child.levels, child.lines))
	e.spelled = aliaslimit.AddCapped(e.spelled, child.spelled)
	e.spelledLines = aliaslimit.AddCapped(e.spelledLines, child.spelledLines)
}

// widen grows e to hold o as well, so that e weighs at least what either
// extent weighs at every level. The result is a leaf only when both are.
func (e *extent) widen(o extent) {
	e.depth = max(e.depth, o.depth)
	e.units = max(e.units, o.units)
	e.lines = max(e.lines, o.lines)
	e.levels = max(e.levels, o.levels)
	e.spelled = max(e.spelled, o.spelled)
	e.spelledLines = max(e.spelledLines, o.spelledLines)
	e.leaf = e.leaf && o.leaf
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
		return c.anchorExtent(n)
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

// anchorExtent returns the [extent] of the content of anchor, as
// [treeCounter.measure] measures it. A counter of a reference anchor
// keeps the result in its [anchorCounts]. The measure of an anchor around
// anchor then does not read the nodes under anchor again.
func (c *treeCounter) anchorExtent(anchor *ast.AnchorNode) extent {
	if c.kept == nil {
		return c.measure(anchor.Value)
	}

	e, ok := c.kept.extents[anchor]
	if !ok {
		e = c.measure(anchor.Value)
		c.kept.extents[anchor] = e
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
// is reading. An alias that reads the reference documents holds the
// extent that their [refWeight] holds. A writer that copies the content
// of an alias as the document spells it indents each line of that text to
// the line of the alias. The spelled text of alias grows by that
// indentation.
func (c *treeCounter) aliasExtent(alias *ast.AliasNode) extent {
	target, ref, ok := c.resolve(alias)
	if !ok || (ref == nil && c.measuring[target]) {
		return leafExtent
	}

	e := c.contentExtent(target, ref)

	if tk := alias.GetToken(); tk != nil && tk.Position != nil {
		e.spelled = aliaslimit.AddCapped(e.spelled, mulCapped(e.spelledLines, tk.Position.IndentNum))
	}

	return e
}

// leafExtent is the [extent] of an alias that the counter takes as one
// node, a leaf of one byte.
var leafExtent = extent{units: 1, lines: 1, leaf: true}

// contentExtent returns the [extent] of what an alias reads. The alias
// reads target, the content of an anchor of the tree, or ref, the weight
// of the anchors of its name in the reference documents. The extents map
// holds the extent of the content of each anchor of the tree. A chain of
// nested aliases thus costs one walk per anchor.
func (c *treeCounter) contentExtent(target ast.Node, ref *refWeight) extent {
	if ref != nil {
		return ref.extent
	}

	e, ok := c.extents[target]
	if !ok {
		c.measuring[target] = true
		e = c.measured(target)
		delete(c.measuring, target)

		c.extents[target] = e
	}

	return e
}

// measured returns the [extent] of node, the content of an anchor, with
// the text of node as the document spells it.
func (c *treeCounter) measured(node ast.Node) extent {
	e := c.measure(node)

	first, last := astnode.Bounds(node)
	text := spellingOf(first, last, nil)
	text.spell(&e)

	return e
}

// spelling is the text of a node as the document spells it, comments and
// spaces included. The origins of the tokens of the node hold that text,
// from the first token to the last.
type spelling struct {
	// The first token of the text, or nil for a node without a token.
	start *token.Token
	// The token after the last token of the text, or nil when the text
	// ends with the tokens of the document.
	after *token.Token
	// The length in bytes of the text.
	bytes int
	// The line breaks in the text.
	breaks int
}

// spellingOf returns the [spelling] of the node whose tokens with the
// lowest and the highest offset are first and last, as [astnode.Bounds]
// returns them. The map inner holds the spelling of nodes inside that
// node, each by its start token. The text of the node holds the text of
// each of them. So spellingOf adds what such a spelling holds and goes on
// after its last token. It then reads each token outside those nodes
// once.
func spellingOf(first, last token.Tokens, inner map[*token.Token]spelling) spelling {
	if len(first) == 0 || len(last) == 0 {
		return spelling{}
	}

	s := spelling{start: first[0]}
	end := last[0].Position.Offset
	tk := first[0]

	for tk != nil && tk.Position != nil && tk.Position.Offset <= end {
		if part, ok := inner[tk]; ok {
			s.bytes = aliaslimit.AddCapped(s.bytes, part.bytes)
			s.breaks = aliaslimit.AddCapped(s.breaks, part.breaks)
			tk = part.after

			continue
		}

		s.bytes = aliaslimit.AddCapped(s.bytes, len(tk.Origin))
		s.breaks = aliaslimit.AddCapped(s.breaks, strings.Count(tk.Origin, "\n"))
		tk = tk.Next
	}

	s.after = tk

	return s
}

// spell adds the text of s to the spelled text and lines of e. Before
// the call, they hold the text and the lines of the aliases in the node
// of s. A node without a token does not add any.
func (s spelling) spell(e *extent) {
	if s.start == nil {
		return
	}

	e.spelled = aliaslimit.AddCapped(e.spelled, s.bytes)
	e.spelledLines = aliaslimit.AddCapped(aliaslimit.AddCapped(e.spelledLines, 1), s.breaks)
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

// repeats reports whether each alias reads more than a shared scalar
// where the decoder reads the alias into a value. The alias reads target,
// the content of an anchor of the tree, or ref, the weight of the anchors
// of its name in the reference documents. The answer for target depends on
// target alone, so the counter keeps it. The counter walks the anchors
// and tags of target once, however many aliases refer to the anchor.
func (c *treeCounter) repeats(target ast.Node, ref *refWeight) bool {
	if ref != nil {
		return ref.repeats
	}

	repeated, ok := c.repeated[target]
	if !ok {
		repeated = c.repeatsContent(target)
		c.repeated[target] = repeated
	}

	return repeated
}

// repeatsContent reports whether an alias to node, the content of an
// anchor, reads more than a shared scalar where the decoder reads the
// alias into a value. It does for a text tag, for a !!binary scalar, and
// for a mapping or a sequence.
func (c *treeCounter) repeatsContent(node ast.Node) bool {
	return isTextTag(node) || c.holdsBinary(node) || c.holdsCollection(node)
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
	return c.reaches(node, traitCollection)
}

// holdsBinary reports whether node holds a !!binary tag among its
// anchors and tags, or refers to one through the aliases that
// [treeCounter.reaches] follows.
func (c *treeCounter) holdsBinary(node ast.Node) bool {
	return c.reaches(node, traitBinary)
}

// trait is a property of a node that [treeCounter.reaches] looks for
// behind the aliases that the node leads to.
type trait int

const (
	// The node holds a mapping or a sequence under its anchors and tags.
	traitCollection trait = iota

	// The decoder writes the node out as text in a key, as [isTextKey]
	// reports.
	traitTextKey

	// One of the tags around the content of the node is a !!binary tag.
	traitBinary

	// The number of traits.
	traits
)

// of reports whether node has the trait.
func (t trait) of(node ast.Node) bool {
	switch t {
	case traitCollection:
		return isCollection(node)
	case traitTextKey:
		return isTextKey(node)
	default:
		return underBinary(node)
	}
}

// reaches reports whether node has the trait want, or the content of an
// alias that node leads to has it. It follows the alias under the anchors
// and tags of node to the content of its anchor, and goes on from there
// in the same way. [paths.Resolver.Deref] stops at a tag. An alias to an
// anchor such as `&s !foo *k` therefore gives the tag over *k, and
// reaches goes on to the content that *k refers to. It stops at an alias
// that does not resolve, at one that the decoder reads as null, and at
// one that it has followed before. At an alias that reads the reference
// documents, it takes the answer that their [refWeight] holds.
//
// For each alias that reaches has followed for want, the traits map of
// want holds whether the content that the alias leads to has the trait.
// That answer does not depend on where the walk began. So reaches records
// it for every alias on the way and stops at an alias that it has
// recorded. It records an alias as false before it follows it, so a walk
// that returns to that alias finds false.
func (c *treeCounter) reaches(node ast.Node, want trait) bool {
	if want.of(node) {
		return true
	}

	var (
		results  = c.traits[want]
		followed []*ast.AliasNode
		found    bool
	)

	for {
		alias, ok := astnode.Content(node).(*ast.AliasNode)
		if !ok {
			break
		}

		if known, ok := results[alias]; ok {
			found = known

			break
		}

		results[alias] = false
		followed = append(followed, alias)

		target, ref, ok := c.resolve(alias)
		if !ok {
			break
		}

		if ref != nil {
			found = ref.traits[want]

			break
		}

		if want.of(target) {
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
