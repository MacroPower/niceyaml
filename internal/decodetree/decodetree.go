package decodetree

import (
	"sort"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

// Tree binds the aliases of one document for the go-yaml decoder.
//
// The decoder asks [Tree.Resolve] for the node that each alias reads, as
// [go.jacobcolvin.com/niceyaml/internal/yamldecode.ResolveAlias] describes.
// An alias then reads the anchor that the document's
// [go.jacobcolvin.com/niceyaml/paths.Resolver] binds it to, whatever order
// the decoder reads the anchors in. The decoder reads the nodes of the
// document itself, so each node that it fills or hands an UnmarshalYAML
// method is a node of the document.
//
// Create instances with [New].
type Tree struct {
	// The body of the document.
	body ast.Node
	// The anchors and aliases of body.
	facts *aliasing.Facts
	// The function that returns the fact of each alias of body. It lists
	// them on the first call.
	facted func() map[*ast.AliasNode]aliasing.AliasFact
	// The function that returns the tokens of the nodes of body. It lists
	// them on the first call.
	nodeTokens func() map[*token.Token]struct{}
	// The function that returns the aliases of body in the order of their
	// offsets. It lists them on the first call.
	aliases func() []aliasRead
	// The function that returns the aliases to a reference document that a
	// decode of body reads. It lists them on the first call.
	bodyReads func() []aliasRead
}

// New creates a new [*Tree] for body, a document body with facts.
func New(body ast.Node, facts *aliasing.Facts) *Tree {
	t := &Tree{body: body, facts: facts}

	t.facted = sync.OnceValue(t.newFacted)
	t.nodeTokens = sync.OnceValue(t.newNodeTokens)
	t.aliases = sync.OnceValue(t.offsetAliases)
	t.bodyReads = sync.OnceValue(func() []aliasRead { return t.walkReferenceReads(t.body) })

	return t
}

// newFacted maps each alias of the tree to its fact.
func (t *Tree) newFacted() map[*ast.AliasNode]aliasing.AliasFact {
	facted := make(map[*ast.AliasNode]aliasing.AliasFact, len(t.facts.Aliases))
	for _, a := range t.facts.Aliases {
		facted[a.Node] = a
	}

	return facted
}

// newNodeTokens returns the tokens that the nodes of the tree hold.
func (t *Tree) newNodeTokens() map[*token.Token]struct{} {
	tokens := map[*token.Token]struct{}{}

	astnode.Inspect(t.body, func(node ast.Node) bool {
		if tk := node.GetToken(); tk != nil {
			tokens[tk] = struct{}{}
		}

		return true
	})

	return tokens
}

// Resolve returns the node that the decoder reads for alias. The last
// result reports whether the tree binds alias. It does not bind an alias
// outside the document, or one that the document's resolver does not
// bind to an anchor. Such an alias reads an anchor of a reference
// document from niceyaml.WithReferences, by its name.
//
// For any other alias, the node is the value of the anchor that the
// resolver binds the alias to. The document reads an alias inside the
// anchor it refers to as null wherever a read reaches it. For such an
// alias, the node is a null that holds the token of the alias, so an
// error at the null names the alias. An alias that a `<<` merge key merges
// inside its own anchor is the exception, and its node is the value of the
// anchor. The decoder reads that alias inside the anchor and rejects the
// loop.
func (t *Tree) Resolve(alias *ast.AliasNode) (ast.Node, bool) {
	a, ok := t.facted()[alias]
	if !ok || a.Anchor == nil {
		return nil, false
	}

	if t.facts.Enclosed[alias] {
		return ast.Null(alias.Start), true
	}

	return a.Anchor.Value, true
}

// Anchor returns the anchor that the decoder reads for alias, an alias of
// the document. That anchor is the one the document's resolver binds the
// alias to. Anchor reports false when the resolver does not bind alias to
// an anchor, as for an alias to a reference document. It also reports
// false for an alias inside the anchor it refers to, which the decoder
// does not read through.
func (t *Tree) Anchor(alias *ast.AliasNode) (*ast.AnchorNode, bool) {
	a, ok := t.facted()[alias]
	if !ok || a.Anchor == nil || a.Inside {
		return nil, false
	}

	return a.Anchor, true
}

// Holds reports whether tk is the token of a node of the tree. The null
// that the parser makes for a key without a value is one such token,
// which the lexer does not make.
func (t *Tree) Holds(tk *token.Token) bool {
	_, ok := t.nodeTokens()[tk]

	return ok
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
// argument scope is a node of the document. Such an alias is one that the
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

// Changes reports whether go-yaml's own decoder, which binds each alias by
// its name, can read the document with facts apart from the document. It
// reads the anchors and the aliases of the facts. References reports
// whether the Source has reference documents, from
// niceyaml.WithReferences.
//
// Go-yaml binds an alias to the last anchor of its name that it has read,
// in the order that it reads them. A document that gives several anchors
// one name can thus read an alias otherwise. So can a document with an
// alias that the resolver does not bind to an anchor, beside an anchor of
// that name. So can a document with reference documents, because they
// might define any name. Go-yaml also reads an alias inside the anchor it
// refers to as null only while it reads that anchor. The document reads
// such an alias as null wherever a read reaches it.
func Changes(facts *aliasing.Facts, references bool) bool {
	return anchorNamesRebind(facts, references) || len(facts.Enclosed) > 0
}

// anchorNamesRebind reports whether go-yaml can bind some anchor name of
// facts otherwise, as [Changes] describes. Several anchors share such a
// name, or an alias that the resolver does not bind to an anchor spells
// it. With references set, any anchor name can bind otherwise,
// because a reference document might define it.
func anchorNamesRebind(facts *aliasing.Facts, references bool) bool {
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

	for name, count := range anchors {
		if references || count > 1 || unbound[name] {
			return true
		}
	}

	return false
}
