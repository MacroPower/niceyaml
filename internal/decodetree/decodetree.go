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
// The decoder keeps the anchors it has read in maps keyed by name. An
// anchor it reads later therefore hides an earlier one of the same name
// from every alias it reads after that. The decoder reads a node in two
// passes. A decode of a node below the body also reads the anchors
// outside the node first. Each order differs from the order of the
// document.
//
// The tree therefore gives a name of its own to each anchor whose name
// another anchor of the document shares. It also gives a name of its own
// to each anchor that follows an alias of its name when the alias does
// not follow an anchor of that name. It gives each alias the name of the
// anchor the document's [go.jacobcolvin.com/niceyaml/paths.Resolver]
// binds it to. Every alias then reads that anchor, whatever order the
// decoder reads the anchors in. An alias that the resolver does not bind
// to an anchor keeps its name. So it reads the anchor of that name in a
// reference document, if one exists.
//
// The decoder keeps the anchors of the reference documents in the same
// maps. Before a decode, niceyaml has the decoder read the anchors of the
// reference documents that the decode needs. The decoder reads the value
// of each such anchor into an interface. It reads the nodes of the anchor
// again for each target of another type.
//
// An alias among those nodes finds its anchor by name at every read. The
// second read thus finds an anchor of the document that shares the name,
// but the first read found the anchor of the reference document. When the
// Source has reference documents from niceyaml.WithReferences, the tree
// therefore gives every anchor of the document a name of its own. An
// alias of a reference document then reads only the anchors of the
// reference documents.
//
// The decoder reads an alias inside the anchor it refers to as null,
// because it has not finished reading the anchor. For a type with an
// UnmarshalText method, or one that reads YAML bytes, the decoder writes
// an alias to that anchor as the text of its content. That content holds
// the alias again, so the writing never ends and overflows the stack. The
// tree therefore holds a null in place of each alias inside the anchor it
// refers to. An alias that a `<<` merge key merges stays, because the
// decoder rejects it before it writes anything.
//
// The decoder reads an integer under a !!int tag as a Go int, and then
// rejects that int for every integer type. The tree therefore gives the
// token of each !!int tag on an integer the value [verbatimIntegerTag].
// The decoder then reads the integer as it reads one without a tag. The
// go-yaml formatter writes the text of each token, and that text still
// spells the tag as the document does. The bytes the decoder hands an
// UnmarshalYAML method therefore keep the tag. A tree that drops the tag
// node loses that text, along with the space before the value that the
// token holds.
//
// Renaming, nulls, and verbatim tags change the tree, and the tree that
// the Source shares must not see those changes. A document that renames
// an anchor, or holds such an alias or such a tag, therefore reads a
// second parse of the Source that [Options.Reparse] returns. A renamed
// anchor keeps the text of the Source, so the bytes the decoder hands an
// UnmarshalYAML method hold the names the document spells. The decoder
// finds the anchor of an alias by the text of its name, so an alias
// carries the new name in its text as well. Each [ast.Node] that the
// decoder fills or hands an UnmarshalYAML method thus spells an alias to a
// renamed anchor with the new name, such as `*x [2]`. It spells each
// anchor as the document does.
//
// Any other document reads the tree of the Source through [DecodeView].
// DecodeView copies only the aliases and the nodes above them, to drop
// the comments on their names.
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
	// comments, when body comes from a second parse. The map is nil
	// otherwise.
	nodes map[ast.Node]ast.Node
	// The index in the input of each token of the second parse that body
	// comes from. The map is nil when body does not come from a second
	// parse. The Source shares it between the trees of all its documents,
	// and the trees do not write to it.
	index map[*token.Token]int
	// The tokens the nodes of body hold, including the ones the parser
	// made for values the document omits. The errors of the decoder might
	// name them.
	nodeTokens map[*token.Token]struct{}
	// The replacer that spells each name the tree gives an anchor as the
	// document does, for the messages the decoder reports. It is nil when
	// the tree does not rename any anchor.
	names *strings.Replacer
	// The function that returns the anchors of body, for the first decode
	// of a node below the body. It lists them on the first call.
	anchors func() *anchorIndex
	// The function that returns the aliases under `<<` merge keys that
	// the decoder does not find a mapping for. It lists them on the first
	// call.
	merges func() []mergeAlias
	// The function that returns the aliases of source in the order of
	// their offsets. It lists them on the first call.
	aliases func() []aliasRead
	// The function that returns the aliases to a reference document that a
	// decode of body reads. It lists them on the first call.
	bodyReads func() []aliasRead
}

// Options says how [New] builds a tree that changes the document.
type Options struct {
	// Reparse returns the body of the document in a second parse of its
	// Source, with the index of that parse. The index maps each token the
	// parser read to its index in the input. Reparse returns a nil index
	// when the parse does not hold such a document. The second parse
	// holds a null in the token of each alias the facts hold as enclosed,
	// as [WriteNull] writes it.
	Reparse func() (ast.Node, map[*token.Token]int)
	// Texts returns the texts of the document that a new name such as
	// "x [2]" must not spell.
	Texts func() []string
	// ReferenceSpellings returns the [Spellings] of the reference
	// documents that niceyaml.WithReferences gives the Source. A new name
	// must not spell what they index either. A nil ReferenceSpellings, or
	// one that returns nil, means that the Source does not have reference
	// documents.
	ReferenceSpellings func() []*Spellings
	// References reports whether the Source has reference documents, from
	// niceyaml.WithReferences.
	References bool
}

// New creates a new [*Tree] for body, a document body with facts.
func New(body ast.Node, facts *aliasing.Facts, opts Options) *Tree {
	t := &Tree{source: body, facts: facts}

	names := renamedAnchorNames(facts, opts.References)
	if !changes(body, facts, names) || !t.parse(names, opts) {
		t.body = DecodeView(body)
	}

	t.nodeTokens = map[*token.Token]struct{}{}

	astnode.Inspect(t.body, func(node ast.Node) bool {
		if tk := node.GetToken(); tk != nil {
			t.nodeTokens[tk] = struct{}{}
		}

		return true
	})

	t.anchors = sync.OnceValue(t.newAnchorIndex)
	t.merges = sync.OnceValue(t.unresolvedMerges)
	t.aliases = sync.OnceValue(t.offsetAliases)
	t.bodyReads = sync.OnceValue(func() []aliasRead { return t.walkReferenceReads(t.body) })

	return t
}

// parse makes the body of the tree from the second parse opts returns. It
// renames each anchor with a name in names, holds a null in place of each
// enclosed alias, and spells each tag [isIntegerTag] reports in verbatim
// form. It reports false when opts does not return a parse of the
// document, or returns one whose nodes differ from those of the document.
func (t *Tree) parse(names map[string]bool, opts Options) bool {
	body, index := opts.Reparse()
	if index == nil {
		return false
	}

	source, views := astnode.Nodes(t.source), astnode.Nodes(body)
	if len(source) != len(views) {
		return false
	}

	// [Tree.newName] skips each name that the document or a reference
	// document spells, even in part, such as in a key or a comment. An
	// alias that spells such a name would read the wrong anchor, and
	// RestoreNames would rewrite the text in any message that quotes it.
	spelled := map[string]bool{}

	if len(names) > 0 {
		NewSpellings(opts.Texts()).find(names, spelled)

		if opts.ReferenceSpellings != nil {
			for _, s := range opts.ReferenceSpellings() {
				s.find(names, spelled)
			}
		}
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

// newName returns a new name for an anchor named name. The new name is
// name followed by a count in brackets, such as "x [2]". The count is one
// past the count of the last anchor of that name, and newName skips each
// count that gives a name in spelled.
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
// place. The null reuses the token, so an error at the null names the
// alias. The go-yaml formatter writes the text of each token as the chain
// of tokens holds it. The token therefore keeps its place in the chain and
// reads null there.
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

// replace returns the node that node maps to, or node itself when it does
// not map to one.
func (r nodeReplacer) replace(node ast.Node) ast.Node {
	if v, ok := r[node]; ok {
		return v
	}

	return node
}

// renameAlias gives alias, a node of a second parse, a name free of
// comments, as [DecodeView] does. It changes that name to name when name
// is not empty. The decoder finds the anchor of an alias by the value of
// the token of its name, and by the text of the node of its name.
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
// document outside its comments. It returns the [DecodeView] of node when
// the tree reads the Source through DecodeView, or does not hold a node
// for node.
func (t *Tree) View(node ast.Node) ast.Node {
	if node == t.source {
		return t.body
	}

	if v, ok := t.nodes[node]; ok {
		return v
	}

	return DecodeView(node)
}

// Changes reports whether the [*Tree] that [New] makes for body, a document
// body with facts, can change the document, as [Tree.Changed] tells. It
// reads the anchors, the aliases, and the tags of body, and it does not make
// the tree. A tree changes its document only when Changes reports true.
// References reports whether the Source has reference documents, as
// [Options] holds it.
func Changes(body ast.Node, facts *aliasing.Facts, references bool) bool {
	return changes(body, facts, renamedAnchorNames(facts, references))
}

// changes reports whether a tree of body with facts, whose anchors of the
// names in names take new names, changes the document, as [Changes] tells.
func changes(body ast.Node, facts *aliasing.Facts, names map[string]bool) bool {
	return len(names) > 0 || len(facts.Enclosed) > 0 || holdsIntegerTag(body)
}

// Changed reports whether the tree changes the document. A tree that
// renames an anchor, holds a null in place of an alias inside its anchor,
// or spells an !!int tag in verbatim form changes it. Such a tree comes
// from a second parse. The decoder reads any other tree as it reads the
// document. Go-yaml's comment-free parse of the text, which yaml.Unmarshal
// reads, can still read the text apart from the document, as it reads
// "p: !!str &k [1]", "# l", and "q: 1" without q.
func (t *Tree) Changed() bool {
	return t.nodes != nil
}

// Parsed returns the node of the tree that stands for node when the tree
// comes from a second parse, and node itself otherwise. The argument node
// is a node of the document outside its comments. A tree that reads the
// Source through [DecodeView] differs from the Source only in the names of
// its aliases. A decoder that does not know any of the anchors of the
// document therefore reads node as it reads the tree.
func (t *Tree) Parsed(node ast.Node) ast.Node {
	if v, ok := t.nodes[node]; ok {
		return v
	}

	return node
}

// Anchor returns the anchor of the tree that the decoder reads for alias,
// an alias of the tree. That anchor is the one the document's resolver
// binds the alias to. Anchor reports false when the resolver does not
// bind alias to an anchor, as for an alias to a reference document. It
// also reports false for an alias inside the anchor it refers to, which
// the decoder does not read through.
func (t *Tree) Anchor(alias *ast.AliasNode) (*ast.AnchorNode, bool) {
	if alias == nil || alias.Start == nil || alias.Start.Position == nil {
		return nil, false
	}

	off := alias.Start.Position.Offset
	aliases := t.aliases()

	i := sort.Search(len(aliases), func(i int) bool {
		return aliases[i].token.Position.Offset >= off
	})
	if i == len(aliases) || aliases[i].token.Position.Offset != off {
		return nil, false
	}

	a := aliases[i]
	if a.anchor == nil || a.enclosed {
		return nil, false
	}

	anchor, ok := t.View(a.anchor).(*ast.AnchorNode)

	return anchor, ok
}

// Holds reports whether tk is a token of the parse the tree reads. Such a
// token is one of the tokens of its second parse, or the token of a node
// of its body. The null that the parser makes for a key without a value
// is one such token. A tree that reads the Source through [DecodeView]
// shares every token with the Source.
func (t *Tree) Holds(tk *token.Token) bool {
	if _, ok := t.index[tk]; ok {
		return true
	}

	_, ok := t.nodeTokens[tk]

	return ok
}

// UnresolvedMerge finds the first alias under a `<<` merge key inside
// scope that the decoder does not find a mapping for and that err
// reports. It returns the token and the message of that alias. The
// argument scope is a node of the tree, and err is the error the decoder
// returned for scope. UnresolvedMerge reports false when scope does not
// hold such an alias.
//
// The document's resolver might not bind an alias to an anchor before it,
// and the decoder then does not find a mapping for that alias. The
// decoder also does not find a mapping for an alias inside the anchor it
// names. It has not finished reading that anchor when it merges.
//
// The document's resolver does not know the anchors of the reference
// documents from niceyaml.WithReferences, which a decode reads first. The
// decoder merges an alias whose anchor one of those documents defines.
// UnresolvedMerge therefore takes err to report an alias only when err is
// the decoder's own failure for that alias. That failure is the message
// the decoder gives an alias that it does not find an anchor for. For an
// alias inside the anchor it names, the failure is also any [yaml.Error]
// whose token is not one of the tree's, such as the null the decoder
// merges in its place.
//
// The decoder can also miss an anchor that the resolver binds, and its
// message then names the alias by name alone. UnresolvedMerge then finds
// the first alias of that name inside scope.
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

	// The decoder can miss an anchor that the resolver binds, such as one
	// inside a collection under a tag that converts it, which the decoder
	// does not read. Its message names the alias by name alone, and the
	// first alias of that name inside scope is the first that it reads.
	for _, a := range t.facts.Aliases {
		tk := a.Node.Start
		if tk == nil || tk.Position == nil || tk.Position.Offset < lo || tk.Position.Offset > hi {
			continue
		}

		if m := (mergeAlias{token: tk, name: a.Name}); msg == m.message() {
			return tk, msg, true
		}
	}

	return nil, "", false
}

// unresolvedMerges returns the aliases under the `<<` merge keys of the
// tree that the decoder does not find a mapping for, as
// [Tree.UnresolvedMerge] describes, in document order.
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

// ReferenceAlias is an alias of the document to a reference document
// from niceyaml.WithReferences, as [Tree.ReferenceAliases] returns it.
type ReferenceAlias struct {
	// Token is the token of the alias, a token of the Source.
	Token *token.Token
	// Name is the name of the alias as the document spells it, or empty
	// when the alias does not have one.
	Name string
	// Merge reports whether a `<<` merge key merges the alias.
	Merge bool
}

// ReferenceAliases returns the aliases to a reference document from
// niceyaml.WithReferences that scope holds, in document order. The
// argument scope is a node of the tree. Such an alias is one that the
// document's resolver does not bind to an anchor before it. The alias
// lies inside scope, or inside an anchor of the document that an alias
// the walk reaches refers to, even an anchor outside scope. The walk
// ignores the target type of the decode, so it returns aliases the type
// never reads. An alias inside the anchor it refers to reads null, so the
// walk does not follow it.
func (t *Tree) ReferenceAliases(scope ast.Node) []ReferenceAlias {
	reads := t.referenceReads(scope)

	refs := make([]ReferenceAlias, len(reads))
	for i, a := range reads {
		refs[i] = ReferenceAlias{Token: a.token, Name: a.name, Merge: a.merge}
	}

	return refs
}

// ReferenceNames returns the names of the aliases [Tree.ReferenceAliases]
// returns for scope, each once, in document order. A decode of scope
// reads the anchors of those names in the reference documents.
func (t *Tree) ReferenceNames(scope ast.Node) []string {
	var names []string

	seen := map[string]bool{}

	for _, a := range t.referenceReads(scope) {
		if a.name != "" && !seen[a.name] {
			seen[a.name] = true
			names = append(names, a.name)
		}
	}

	return names
}

// referenceReads returns the aliases [Tree.ReferenceAliases] describes
// for scope, in document order. The tree lists those of its body once,
// because each decode of the body reads them.
func (t *Tree) referenceReads(scope ast.Node) []aliasRead {
	if scope == t.body {
		return t.bodyReads()
	}

	return t.walkReferenceReads(scope)
}

// walkReferenceReads lists the aliases [Tree.referenceReads] returns for
// scope.
func (t *Tree) walkReferenceReads(scope ast.Node) []aliasRead {
	aliases := t.aliases()

	var refs []aliasRead

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
					refs = append(refs, a)
				}

			case !visited[a.anchor] && !a.enclosed:
				visited[a.anchor] = true
				work = append(work, a.anchor)
			}
		}
	}

	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].token.Position.Offset < refs[j].token.Position.Offset
	})

	return refs
}

// offsetAliases returns the aliases of the tree in the order of their
// offsets.
func (t *Tree) offsetAliases() []aliasRead {
	var aliases []aliasRead

	for _, a := range t.facts.Aliases {
		if a.Node.Start == nil || a.Node.Start.Position == nil {
			continue
		}

		read := aliasRead{token: a.Node.Start, name: a.Name, enclosed: a.Inside, merge: a.Merge}
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
	// The anchor the document's resolver binds the alias to, or nil when
	// the resolver does not bind the alias to an anchor.
	anchor ast.Node
	// The token of the alias.
	token *token.Token
	// The name of the alias as the document spells it, or empty when the
	// alias does not have one.
	name string
	// Whether the alias lies inside anchor.
	enclosed bool
	// Whether a `<<` merge key merges the alias.
	merge bool
}

// mergeAlias is an alias under a `<<` merge key that the decoder does not
// find a mapping for.
type mergeAlias struct {
	// The token of the alias.
	token *token.Token
	// The name of the alias as the document spells it.
	name string
	// Whether the alias lies inside the anchor it names.
	enclosed bool
}

// message returns the message the decoder gives the alias when a decode
// of the whole document does not find an anchor for it.
func (m mergeAlias) message() string {
	return "cannot find anchor by alias name " + m.name
}

// RestoreNames returns msg with each name the tree gave an anchor spelled
// as the document does. A nil tree did not rename any anchor.
func (t *Tree) RestoreNames(msg string) string {
	if t == nil || t.names == nil {
		return msg
	}

	return t.names.Replace(msg)
}

// renamedAnchorNames returns the anchor names of facts that the tree
// renames. The tree renames each name that several anchors share. It also
// renames an anchor name when the resolver does not bind an alias of that
// name to an anchor. The decoder might read such an alias as a later
// anchor of that name. With references set, the result holds every name,
// because a reference document might define any name.
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

// Spellings indexes the counts in brackets that texts spell, such as the
// " [2]" of "x [2]", so a [Tree] finds the new names it cannot give an
// anchor. A Source builds the Spellings of the text of each reference
// document once. Every tree that reads that document shares them.
//
// Create instances with [NewSpellings].
type Spellings struct {
	// The index that finds each place a name precedes " [" in data.
	index *suffixarray.Index
	// Each count in brackets of data, by the offset of its " [".
	brackets map[int]bracket
	// The strings that spell a count, joined.
	data []byte
}

// bracket is a count in brackets in the data of a [Spellings]. The string
// that holds it starts at start, and the bracket ends before end.
type bracket struct{ start, end int }

// NewSpellings creates new [*Spellings] for texts.
func NewSpellings(texts []string) *Spellings {
	// The data joins the strings, so that one index finds each name in all
	// of them. A string that spells no count spells no new name, so the
	// data leaves it out.
	s := &Spellings{brackets: map[int]bracket{}}

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

// find adds to spelled the new names that the texts of s spell, even in
// part, among those [Tree.newName] can give an anchor with a name in
// names.
func (s *Spellings) find(names, spelled map[string]bool) {
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
	// The names the aliases under each entry read. [Tree.reads] finds the
	// names of an entry on the first decode that needs them.
	names [][]string
	// The anchors, in document order.
	entries []anchorEntry
	// The mutex that guards names, because decodes of several nodes share
	// the index.
	mu sync.Mutex
}

// anchorEntry is an anchor of the document. When the decoder reads the
// anchor, it registers the name of the anchor and the names of the
// anchors inside it.
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

// reads returns the names that the aliases under entry i of idx read, and
// finds them on the first call. The argument idx is an index of the tree.
// An alias that the tree holds a null in place of does not read a name.
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
// anchor of entry i for a node that starts at offset start. It returns -1
// when such a decode does not precede the node. The entry is the
// outermost entry that holds the anchor and ends before the node. When
// the anchor holds the node, the entry is the anchor itself, and
// [pendingAnchor] then stands for it.
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
// decodes node, so the aliases in node find the anchors they refer to.
// The argument node is a node of the tree below its body. The anchors are
// the ones that end before node, in document order. Prime lists only the
// anchors that register a name that node reads, or a name that a later
// listed anchor reads. The decodes then cost what node needs rather than
// the size of the document.
//
// Prime also lists each anchor that holds node, by the same rule. It
// returns such an anchor as a [pendingAnchor], which registers its own
// name as null. The tree holds a null in place of each alias inside the
// anchor it refers to. A decode of the whole document therefore reads
// such an alias as null too, into every target.
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
