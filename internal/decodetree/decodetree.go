package decodetree

import (
	"index/suffixarray"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// Tree is the tree the go-yaml decoder reads for one document.
//
// The decoder keeps the anchors it has read in maps keyed by name, so an
// anchor it reads later hides an earlier one of the same name from every
// alias it reads after that. It reads a node in two passes, and a decode
// of a node below the body reads the anchors outside the node before it,
// so neither order matches the document. The tree therefore gives a name
// of its own to each anchor whose name another anchor of the document
// shares, and to each anchor that follows an alias of its name with no
// anchor of that name before it. It gives each alias the name of the
// anchor the document's [paths.Resolver] binds it to, so every alias
// reads that anchor whatever order the decoder reads the anchors in. An
// alias the resolver binds to no anchor keeps its name, so it reads an
// anchor of a reference document or none.
//
// The decoder keeps the anchors of the reference documents in the same
// maps, and it reads those documents first. It reads the value of each
// anchor there into an interface, and it reads the nodes of the anchor
// again for each target of another type. An alias among those nodes finds
// its anchor by name at every read. The second read thus finds an anchor
// of the document that shares the name, where the first found the anchor
// of the reference document. When the Source has reference documents from
// niceyaml.WithReferences, the tree therefore gives every anchor of the
// document a name of its own, so an alias of a reference document reads
// the anchors of the reference documents alone.
//
// The decoder reads an alias inside the anchor it refers to as null,
// since it has not finished reading the anchor. For a type with an
// UnmarshalText method, or one that reads YAML bytes, the decoder writes
// out an alias to that anchor as the text of its content. That content
// holds the alias again, so the writing never ends and overflows the
// stack. The tree therefore holds a null in place of each alias inside
// the anchor it refers to. An alias that a `<<` merge key merges stays,
// since the decoder rejects it before it writes anything.
//
// The decoder reads an integer under a !!int tag as a Go int, which it
// then rejects for every integer type. The tree therefore gives the
// token of each !!int tag on an integer the value [verbatimIntegerTag],
// so the decoder reads the integer as it reads one with no tag. The
// go-yaml formatter writes the text of each token, which still spells
// the tag as the document does, so the bytes the decoder hands an
// UnmarshalYAML method keep the tag. A tree without the tag node would
// lose that text, along with the space before the value that the token
// holds.
//
// Renaming, nulls, and verbatim tags change the tree, which the tree the
// Source shares must not see. A document that renames an anchor, or
// holds such an alias or such a tag, therefore reads a second parse of
// the Source that [Options] returns. A renamed anchor keeps the text
// of the Source, so the bytes the decoder hands an UnmarshalYAML method
// hold the names the document spells. The decoder looks an alias up by
// the text of its name, so an alias carries the new name in its text as
// well. An [ast.Node] the decoder fills, or one it hands an UnmarshalYAML
// method, thus spells an alias to a renamed anchor with the new name,
// such as `*x [2]`, and each anchor as the document does.
//
// Any other document reads the tree of the Source through [DecodeView],
// which copies only the aliases, and the nodes above them, to drop the
// comments on their names.
//
// Create instances with [New].
type Tree struct {
	// The body of the document as the Source parsed it.
	source ast.Node
	// The body of the document as the decoder reads it.
	body ast.Node
	// The anchors and aliases of source.
	facts *aliasing.Facts
	// The node of body that stands for each node of source outside its
	// comments, when body comes from a second parse, and nil otherwise.
	nodes map[ast.Node]ast.Node
	// Maps each token of the second parse body comes from to its index
	// in the input, or nil when body comes from no second parse. The
	// Source shares it between the trees of all its documents, and no
	// tree writes to it.
	index map[*token.Token]int
	// Returns the tokens the nodes of body hold, including the ones the
	// parser made for values the document leaves out, and collects them
	// on the first call. The errors of the decoder may name them.
	nodeTokens func() map[*token.Token]struct{}
	// Spells each name the tree gives an anchor as the document does,
	// for the messages the decoder reports, or nil when no anchor has a
	// name of its own.
	names *strings.Replacer
	// Returns the anchors of body, and lists them on the first call, for
	// the first decode of a node below the body.
	anchors func() *anchorIndex
	// Returns the aliases under `<<` merge keys that the decoder finds no
	// mapping for, and lists them on the first call.
	merges func() []mergeAlias
	// Returns the aliases of source in the order of their offsets, and
	// lists them on the first call.
	aliases func() []aliasRead
}

// Options says how [New] builds a tree that changes the document.
type Options struct {
	// Reparse returns the body of the document in a second parse of its
	// Source, with the index of that parse, which maps each token the
	// parser read to its index in the input. It returns a nil index when
	// the parse holds no such document. The second parse holds a null in
	// the token of each alias the facts hold as enclosed, as [WriteNull]
	// writes it.
	Reparse func() (ast.Node, map[*token.Token]int)
	// Texts returns the texts a new name such as "x [2]" must not spell:
	// those of the document and of its reference documents.
	Texts func() []string
	// References reports whether the Source has reference documents, from
	// niceyaml.WithReferences.
	References bool
}

// New creates a new [*Tree] for body, a document body with facts. A body
// that renames an anchor, holds an alias inside the anchor it refers to,
// or holds a tag [isIntegerTag] reports reads a second parse of the
// Source, which opts returns, as [Tree.parse] describes. Any other body
// reads the tree of the Source through [DecodeView].
func New(body ast.Node, facts *aliasing.Facts, opts Options) *Tree {
	t := &Tree{source: body, facts: facts}

	names := renamedAnchorNames(facts, opts.References)

	changes := len(names) > 0 || len(facts.Enclosed) > 0 || holdsIntegerTag(body)
	if !changes || !t.parse(names, opts) {
		t.body = DecodeView(body)
	}

	t.nodeTokens = sync.OnceValue(func() map[*token.Token]struct{} {
		tokens := map[*token.Token]struct{}{}

		astnode.Inspect(t.body, func(node ast.Node) bool {
			if tk := node.GetToken(); tk != nil {
				tokens[tk] = struct{}{}
			}

			return true
		})

		return tokens
	})

	t.anchors = sync.OnceValue(t.newAnchorIndex)
	t.merges = sync.OnceValue(t.unresolvedMerges)
	t.aliases = sync.OnceValue(t.offsetAliases)

	return t
}

// parse makes the body of the tree from the second parse opts returns.
// The tree gives a name of its own to each anchor whose name is in names,
// holds a null in place of each alias the facts hold as enclosed, and
// spells each tag [isIntegerTag] reports in its verbatim form. It reports
// false when the second parse does not give the document the same nodes,
// which a parse of the same tokens always does.
func (t *Tree) parse(names map[string]bool, opts Options) bool {
	body, index := opts.Reparse()
	if index == nil {
		return false
	}

	source, views := astnode.Nodes(t.source), astnode.Nodes(body)
	if len(source) != len(views) {
		return false
	}

	// Each anchor with a name in names gets the name followed by a count in
	// brackets, higher than the count of the last anchor of that name, so
	// no two new names match. A key, a value, a comment, or a quoted anchor
	// name can hold the same text, so the count skips a new name that the
	// document or a reference document spells anywhere, even in part. An
	// alias that spells such a name would read the wrong anchor, and
	// RestoreNames would rewrite the text in any message that quotes it.
	spelled := map[string]bool{}

	if len(names) > 0 {
		newSpellings(opts.Texts()).find(names, spelled)
	}

	anchors := make(map[*ast.AliasNode]*ast.AnchorNode, len(t.facts.Aliases))
	for _, a := range t.facts.Aliases {
		anchors[a.Node] = a.Anchor
	}

	var (
		pairs   []string
		renamed = map[*ast.AnchorNode]string{}
		count   = map[string]int{}
		nodes   = make(map[ast.Node]ast.Node, len(source))
		nulls   = nodeReplacer{}
	)

	// The walk visits an anchor before the aliases that refer to it.
	for i, n := range source {
		view := views[i]
		if n.Type() != view.Type() {
			return false
		}

		nodes[n] = view

		switch n := n.(type) {
		case *ast.AnchorNode:
			name, ok := astnode.Name(n.Name)
			anchor, isAnchor := view.(*ast.AnchorNode)

			if !ok || !isAnchor || !names[name] {
				continue
			}

			unique := t.newName(name, count, spelled)
			renamed[n] = unique
			pairs = append(pairs, unique, name)

			// The decoder names an anchor by the value of the token of its
			// name, while the go-yaml formatter writes the token's text.
			if tk := astnode.Token(anchor.Name); tk != nil {
				tk.Value = unique
			}

		case *ast.AliasNode:
			alias, ok := view.(*ast.AliasNode)
			if !ok {
				continue
			}

			// The second parse holds a null in the token of the `*`, as
			// [WriteNull] writes it.
			if t.facts.Enclosed[n] && alias.Start != nil {
				nulls[alias] = ast.Null(alias.Start)
				nodes[n] = nulls[alias]

				continue
			}

			// An alias that names no anchor before it keeps its name, so the
			// decoder reports it as the document spells it.
			renameAlias(alias, renamed[anchors[n]])

		case *ast.TagNode:
			// The decoder reads a tag by the value of its token, while the
			// go-yaml formatter writes the token's text.
			if tag, ok := view.(*ast.TagNode); ok && tag.Start != nil && isIntegerTag(n) {
				tag.Start.Value = verbatimIntegerTag
			}
		}
	}

	ast.Walk(nulls, body)

	t.body, t.nodes, t.index = body, nodes, index

	if len(pairs) > 0 {
		t.names = strings.NewReplacer(pairs...)
	}

	return true
}

// newName returns a new name for an anchor named name: the name followed
// by a count in brackets, one past the count of the last anchor of that
// name, that spelled does not hold.
func (*Tree) newName(name string, count map[string]int, spelled map[string]bool) string {
	for {
		count[name]++

		unique := name + " [" + strconv.Itoa(count[name]) + "]"
		if !spelled[unique] {
			return unique
		}
	}
}

// WriteNull writes a null into tk, the token of the `*` of an alias, in
// place. The null takes over the token, so an error at the null names the
// alias. The go-yaml formatter writes the text of each token as the chain
// of tokens holds it, so the token keeps its place in the chain and reads
// null there.
func WriteNull(tk *token.Token) {
	null := token.New("null", strings.TrimSuffix(tk.Origin, "*")+"null", tk.Position)
	null.Prev, null.Next = tk.Prev, tk.Next
	*tk = *null
}

// nodeReplacer is an [ast.Visitor] that puts the node each key maps to in
// place of the key, in the parent of the key.
type nodeReplacer map[ast.Node]ast.Node

// Visit implements [ast.Visitor].
func (r nodeReplacer) Visit(node ast.Node) ast.Visitor {
	if astnode.IsNil(node) {
		return nil
	}

	switch n := node.(type) {
	case *ast.AnchorNode:
		n.Value = r.replace(n.Value)

	case *ast.TagNode:
		n.Value = r.replace(n.Value)

	case *ast.MappingKeyNode:
		n.Value = r.replace(n.Value)

	case *ast.MappingValueNode:
		if key, ok := r.replace(n.Key).(ast.MapKeyNode); ok {
			n.Key = key
		}

		n.Value = r.replace(n.Value)

	case *ast.SequenceNode:
		for i, elem := range n.Values {
			n.Values[i] = r.replace(elem)
		}

		// The go-yaml formatter writes the elements from the entries.
		for _, entry := range n.Entries {
			if entry != nil {
				entry.Value = r.replace(entry.Value)
			}
		}
	}

	return r
}

// replace returns the node that node maps to, or node itself when it
// maps to none.
func (r nodeReplacer) replace(node ast.Node) ast.Node {
	if v, ok := r[node]; ok {
		return v
	}

	return node
}

// renameAlias gives alias, a node of a second parse, a name free of
// comments, as [DecodeView] does, and changes that name to name when name
// is not empty. The decoder looks an alias up by the value of the token
// of its name, and by the text of the node of its name.
func renameAlias(alias *ast.AliasNode, name string) {
	tk := astnode.Token(alias.Value)
	if tk == nil {
		return
	}

	if name != "" {
		tk.Value = name
	}

	alias.Value = ast.String(tk)
}

// View returns the node of the tree that stands for node, a node of the
// document outside its comments. A tree that reads the Source through
// [DecodeView] holds no such nodes, so View returns the DecodeView of
// node there, as it does for a node the tree does not hold, which no node
// of the document is.
func (t *Tree) View(node ast.Node) ast.Node {
	if node == t.source {
		return t.body
	}

	if v, ok := t.nodes[node]; ok {
		return v
	}

	return DecodeView(node)
}

// Parsed returns the node of the tree that stands for node, a node
// of the document outside its comments, when the tree comes from a
// second parse, and node itself otherwise. A tree that reads the Source
// through [DecodeView] differs from it only in the names of its aliases,
// so a decoder that knows none of the anchors of the document reads node
// as it reads the tree.
func (t *Tree) Parsed(node ast.Node) ast.Node {
	if v, ok := t.nodes[node]; ok {
		return v
	}

	return node
}

// Holds reports whether tk is a token of the parse the tree reads: one of
// the tokens of its second parse, or the token of a node of its body,
// such as the null the parser makes for a key without a value. A tree
// that reads the Source through [DecodeView] shares every token with the
// Source.
func (t *Tree) Holds(tk *token.Token) bool {
	if _, ok := t.index[tk]; ok {
		return true
	}

	_, ok := t.nodeTokens()[tk]

	return ok
}

// UnresolvedMerge returns the token and the message of the first alias
// under a `<<` merge key inside scope, a node of the tree, that the
// decoder finds no mapping for and that err, which the decoder returned
// for scope, reports. It reports false when scope holds no such alias.
// The decoder finds no mapping for an alias that names no anchor before
// it as the document's resolver binds it, or for one inside the anchor it
// names, which the decoder has not finished reading when it merges.
//
// The document's resolver knows nothing of the reference documents a
// decode may carry, from niceyaml.WithReferences, and the decoder merges an
// alias that one of them defines. So err reports an alias only when it
// is the decoder's own failure for that alias. That is the message the
// decoder gives an alias it finds no anchor for, and, for an alias
// inside the anchor it names, any [yaml.Error] whose token is not one
// of the tree's, such as the null the decoder merges in its place.
func (t *Tree) UnresolvedMerge(scope ast.Node, err error) (*token.Token, string, bool) {
	first, last := astnode.Bounds(scope)
	if len(first) == 0 {
		return nil, "", false
	}

	lo, hi := first[0].Position.Offset, last[0].Position.Offset
	msg := t.RestoreNames(err.Error())
	_, yamlErr := err.(yaml.Error) //nolint:errorlint // A wrapped error is the unmarshaler's own.

	for _, m := range t.merges() {
		off := m.token.Position.Offset
		if off < lo || off > hi {
			continue
		}

		if msg == m.message() || m.enclosed && yamlErr {
			return m.token, m.message(), true
		}
	}

	return nil, "", false
}

// unresolvedMerges returns the aliases under the `<<` merge keys of the
// tree that the decoder finds no mapping for, as
// [Tree.unresolvedMerge] describes, in document order.
func (t *Tree) unresolvedMerges() []mergeAlias {
	var found []mergeAlias

	for _, a := range t.facts.Aliases {
		if !a.Merge || a.Node.Start == nil || a.Node.Start.Position == nil || a.Anchor != nil && !a.Inside {
			continue
		}

		found = append(found, mergeAlias{token: a.Node.Start, name: a.Name, enclosed: a.Anchor != nil})
	}

	sort.SliceStable(found, func(i, j int) bool {
		return found[i].token.Position.Offset < found[j].token.Position.Offset
	})

	return found
}

// ReferenceAliases returns the tokens of the aliases to a reference
// document, from niceyaml.WithReferences, that scope, a node of the tree,
// holds, in document order. Those are the aliases that name no anchor
// before them as the document's resolver binds them, inside scope or
// inside an anchor of the document that an alias the walk reaches refers
// to, including anchors outside scope. The walk ignores the target type
// of the decode, so it returns aliases the type never reads. An alias
// inside the anchor it refers to reads null, so the walk does not follow
// it.
func (t *Tree) ReferenceAliases(scope ast.Node) []*token.Token {
	aliases := t.aliases()

	var refs []*token.Token

	seen := map[*token.Token]bool{}
	visited := map[ast.Node]bool{}
	work := []ast.Node{scope}

	for len(work) > 0 {
		node := work[len(work)-1]
		work = work[:len(work)-1]

		first, last := astnode.Bounds(node)
		if len(first) == 0 {
			continue
		}

		lo, hi := first[0].Position.Offset, last[0].Position.Offset
		start := sort.Search(len(aliases), func(i int) bool {
			return aliases[i].token.Position.Offset >= lo
		})

		for _, a := range aliases[start:] {
			if a.token.Position.Offset > hi {
				break
			}

			switch {
			case a.anchor == nil:
				if !seen[a.token] {
					seen[a.token] = true
					refs = append(refs, a.token)
				}

			case !visited[a.anchor] && !a.enclosed:
				visited[a.anchor] = true
				work = append(work, a.anchor)
			}
		}
	}

	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].Position.Offset < refs[j].Position.Offset
	})

	return refs
}

// offsetAliases returns the aliases of the tree in the order of their
// offsets, for [Tree.referenceAliases].
func (t *Tree) offsetAliases() []aliasRead {
	var aliases []aliasRead

	for _, a := range t.facts.Aliases {
		if a.Node.Start == nil || a.Node.Start.Position == nil {
			continue
		}

		read := aliasRead{token: a.Node.Start, enclosed: a.Inside}
		if a.Anchor != nil {
			read.anchor = a.Anchor
		}

		aliases = append(aliases, read)
	}

	sort.SliceStable(aliases, func(i, j int) bool {
		return aliases[i].token.Position.Offset < aliases[j].token.Position.Offset
	})

	return aliases
}

// aliasRead is an alias of the document and the anchor it reads.
type aliasRead struct {
	// The token of the alias.
	token *token.Token
	// The anchor the document's resolver binds the alias to, or nil when
	// it binds the alias to no anchor.
	anchor ast.Node
	// Whether the alias lies inside anchor.
	enclosed bool
}

// mergeAlias is an alias under a `<<` merge key that the decoder finds no
// mapping for.
type mergeAlias struct {
	// The token of the alias.
	token *token.Token
	// The name of the alias as the document spells it.
	name string
	// Whether the alias lies inside the anchor it names.
	enclosed bool
}

// message returns the message the decoder gives the alias when a decode
// of the whole document finds no anchor for it.
func (m mergeAlias) message() string {
	return "cannot find anchor by alias name " + m.name
}

// RestoreNames returns msg with each name the tree gave an anchor spelled
// as the document does. A nil tree gave no anchor a name.
func (t *Tree) RestoreNames(msg string) string {
	if t == nil || t.names == nil {
		return msg
	}

	return t.names.Replace(msg)
}

// renamedAnchorNames returns the names of the anchors of a document, as
// facts holds them, that the tree gives a name of their own. Those are
// the names more than one anchor has, and the names of anchors that
// follow an alias of their name that the document's resolver binds to no
// anchor. The decoder would read such an alias as the later anchor,
// although it names no anchor of the document before it and reads a
// reference document's anchor, if any. When references is set, the
// Source has reference documents, which may define an anchor of any
// name, so the result holds the name of every anchor.
func renamedAnchorNames(facts *aliasing.Facts, references bool) map[string]bool {
	anchors := map[string]int{}
	unbound := map[string]bool{}

	for _, a := range facts.Anchors {
		if a.Name != "" {
			anchors[a.Name]++
		}
	}

	for _, a := range facts.Aliases {
		if a.Anchor == nil && a.Name != "" {
			unbound[a.Name] = true
		}
	}

	names := map[string]bool{}

	for name, count := range anchors {
		if references || count > 1 || unbound[name] {
			names[name] = true
		}
	}

	return names
}

// spellings indexes the counts in brackets that tokens spell, such as the
// " [2]" of "x [2]", so [spellings.find] reports the new names
// [document.parsedTree] cannot give an anchor.
//
// Create instances with [newSpellings].
type spellings struct {
	// Finds each place a name precedes " [" in data.
	index *suffixarray.Index
	// Each count in brackets of data, by the offset of its " [".
	brackets map[int]bracket
	// The strings that spell a count, joined.
	data []byte
}

// bracket is a count in brackets in the data of a [spellings]. The string
// that holds it starts at start, and the bracket ends before end.
type bracket struct{ start, end int }

// newSpellings creates a new [*spellings] for texts.
func newSpellings(texts []string) *spellings {
	// The data joins the strings, so that one index finds each name in all
	// of them. A string that spells no count spells no new name, so the
	// data leaves it out.
	s := &spellings{brackets: map[int]bracket{}}

	for _, str := range texts {
		start := len(s.data)
		counts := 0

		for off := 0; ; {
			i := strings.Index(str[off:], " [")
			if i < 0 {
				break
			}

			at := off + i
			off = at + 1

			end := at + len(" [")
			for end < len(str) && str[end] >= '0' && str[end] <= '9' {
				end++
			}

			if end == at+len(" [") || end == len(str) || str[end] != ']' {
				continue
			}

			s.brackets[start+at] = bracket{start: start, end: start + end + 1}
			counts++
		}

		if counts > 0 {
			s.data = append(s.data, str...)
		}
	}

	// The index finds each place a name precedes " [", so the time grows
	// with those places rather than with the names times the brackets.
	s.index = suffixarray.New(s.data)

	return s
}

// find adds to spelled the new names that the tokens of s spell, even in
// part, among those [document.parsedTree] can give an anchor with a name
// in names, the name followed by a count in brackets.
func (s *spellings) find(names, spelled map[string]bool) {
	for name := range names {
		for _, at := range s.index.Lookup([]byte(name+" ["), -1) {
			// A place where the name starts in an earlier string spells
			// nothing.
			b, ok := s.brackets[at+len(name)]
			if ok && at >= b.start {
				spelled[name+string(s.data[at+len(name):b.end])] = true
			}
		}
	}
}

// anchorIndex lists the anchors of a [Tree], so a decode of a node
// below the body primes the anchors its aliases need without reading the
// whole body.
//
// Create instances with [Tree.newAnchorIndex].
type anchorIndex struct {
	// The anchors of each name, as indexes into entries.
	named map[string][]int
	// The names the aliases under each entry read, which reads finds for
	// the first decode that needs them.
	names [][]string
	// The anchors, in document order.
	entries []anchorEntry
	// Guards names, since decodes of several nodes share the index.
	mu sync.Mutex
}

// anchorEntry is an anchor, which registers its name and the names of the
// anchors inside it when the decoder reads it.
type anchorEntry struct {
	// The anchor, a node of the document.
	node *ast.AnchorNode
	// The offsets of the first and the last token under node.
	start, end int
	// The index of the innermost entry that holds node, or -1.
	parent int
}

// newAnchorIndex creates a new [*anchorIndex] for the anchors of the
// tree, each under the name the tree gives it.
func (t *Tree) newAnchorIndex() *anchorIndex {
	idx := &anchorIndex{named: map[string][]int{}}

	for _, a := range t.facts.Anchors {
		if a.Extent.OK {
			idx.entries = append(idx.entries, anchorEntry{node: a.Node, start: a.Extent.Lo, end: a.Extent.Hi})
		}
	}

	// The walk visits a node before the nodes inside it, and a stable sort
	// keeps that order for nodes that start at one offset.
	sort.SliceStable(idx.entries, func(i, j int) bool {
		return idx.entries[i].start < idx.entries[j].start
	})

	idx.names = make([][]string, len(idx.entries))

	// The entries that hold the current one, innermost last.
	var holders []int

	for i := range idx.entries {
		e := &idx.entries[i]

		for len(holders) > 0 && idx.entries[holders[len(holders)-1]].end < e.start {
			holders = holders[:len(holders)-1]
		}

		e.parent = -1
		if len(holders) > 0 {
			e.parent = holders[len(holders)-1]
		}

		holders = append(holders, i)

		// A tree from a second parse names the anchor as the decoder does.
		view, ok := t.Parsed(e.node).(*ast.AnchorNode)
		if !ok {
			continue
		}

		if name, ok := astnode.Name(view.Name); ok {
			idx.named[name] = append(idx.named[name], i)
		}
	}

	return idx
}

// reads returns the names the aliases under entry i of idx, an index of
// the tree, read, and finds them on the first call. An alias the tree
// holds a null in place of reads nothing.
func (t *Tree) reads(idx *anchorIndex, i int) []string {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.names[i] == nil {
		view := t.Parsed(idx.entries[i].node)
		idx.names[i] = slices.AppendSeq(make([]string, 0), maps.Keys(aliasNames(view)))
	}

	return idx.names[i]
}

// candidate returns the index of the entry whose decode registers the
// anchor of entry i for a node that starts at offset start, or -1 when no
// such decode precedes the node. That is the outermost entry that holds
// the anchor and ends before the node, or the anchor itself when it holds
// the node, which [pendingAnchor] then stands for.
func (idx *anchorIndex) candidate(i, start int) int {
	e := idx.entries[i]

	switch {
	case e.start >= start:
		return -1

	case e.end >= start:
		return i
	}

	for {
		p := idx.entries[i].parent
		if p < 0 || idx.entries[p].end >= start {
			return i
		}

		i = p
	}
}

// Prime returns the anchors that a decoder registers, in order, before it
// decodes node, a node of the tree below its body, so the aliases in node
// find the anchors they refer to. Those are the anchors that end before
// node, in document order. It lists only the ones that register a name
// node reads, or a name read by one listed after them, so the decodes
// cost what node needs rather than the size of the document. An anchor
// that holds node counts for the anchors inside it that end before node,
// and comes back as a [pendingAnchor] that registers its own name as
// null. A decode of the whole document reads an alias inside an anchor to
// that anchor as null too, into every target, since the tree holds a null
// in its place.
func (t *Tree) Prime(node ast.Node) []*ast.AnchorNode {
	idx := t.anchors()
	if len(idx.entries) == 0 {
		return nil
	}

	reads := aliasNames(node)
	if len(reads) == 0 {
		return nil
	}

	first, _ := astnode.Bounds(node)
	if len(first) == 0 {
		return nil
	}

	start := first[0].Position.Offset

	// For each name something reads, the index of the last entry that
	// reads it. Node reads after every entry.
	readBy := make(map[string]int, len(reads))
	work := make([]string, 0, len(reads))

	for name := range reads {
		readBy[name] = len(idx.entries)
		work = append(work, name)
	}

	// Keep each entry that registers a name an entry after it, or node,
	// reads, and add the names it reads itself.
	kept := map[int]bool{}

	for len(work) > 0 {
		name := work[len(work)-1]
		work = work[:len(work)-1]

		for _, i := range idx.named[name] {
			c := idx.candidate(i, start)
			if c < 0 || c >= readBy[name] || kept[c] {
				continue
			}

			kept[c] = true

			// An anchor that holds node reads nothing before node.
			if idx.entries[c].end >= start {
				continue
			}

			for _, read := range t.reads(idx, c) {
				if last, ok := readBy[read]; ok && last >= c {
					continue
				}

				readBy[read] = c
				work = append(work, read)
			}
		}
	}

	order := make([]int, 0, len(kept))
	for i := range kept {
		order = append(order, i)
	}

	sort.Ints(order)

	anchors := make([]*ast.AnchorNode, 0, len(order))

	for _, i := range order {
		anchor, ok := t.View(idx.entries[i].node).(*ast.AnchorNode)
		if !ok {
			continue
		}

		if idx.entries[i].end >= start {
			anchor = pendingAnchor(anchor)
		}

		anchors = append(anchors, anchor)
	}

	return anchors
}

// pendingAnchor returns an anchor with the name of anchor over a null.
// While the decoder reads the value of an anchor into an interface, it
// registers the name as null, so an alias inside that value reads as
// null. A decode of the pending anchor registers the name that way for a
// node inside anchor.
func pendingAnchor(anchor *ast.AnchorNode) *ast.AnchorNode {
	var pos *token.Position

	if anchor.Start != nil {
		pos = anchor.Start.Position
	}

	return &ast.AnchorNode{
		BaseNode: &ast.BaseNode{},
		Start:    anchor.Start,
		Name:     anchor.Name,
		Value:    ast.Null(token.New("null", "null", pos)),
	}
}

// aliasNames returns the names the aliases under node read.
func aliasNames(node ast.Node) map[string]struct{} {
	names := map[string]struct{}{}

	astnode.Inspect(node, func(n ast.Node) bool {
		if alias, ok := n.(*ast.AliasNode); ok {
			if name, ok := astnode.Name(alias.Value); ok {
				names[name] = struct{}{}
			}
		}

		return true
	})

	return names
}
