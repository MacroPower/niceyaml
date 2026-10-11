package aliasing

import (
	"sync"

	"github.com/goccy/go-yaml/ast"

	"go.jacobcolvin.com/niceyaml/paths"
)

// StateOf returns the [*State] of the document of n, which must be a
// *niceyaml.Node, or nil for a nil Node. The niceyaml package sets it
// when it loads, since this package cannot import that one.
var StateOf func(n any) *State

// State is what one YAML document keeps for the checks of this package:
// whether the alias limit applies to it, the [Facts] of its anchors and
// aliases, and the verdict of each count. Every Node of the document
// shares it, so a check of each item of a list counts the document once.
//
// Create instances with [NewState].
type State struct {
	facts         func() *Facts
	aliasOnce     sync.Once
	textOnce      sync.Once
	excessive     bool
	excessiveText bool
	limit         bool
}

// NewState creates a new [*State] for a document. The alias limit
// applies to the document when limit is set. The first call of
// [State.Facts] calls doc for the body of the document and the resolver
// its aliases resolve through.
func NewState(limit bool, doc func() (ast.Node, *paths.Resolver)) *State {
	return &State{
		facts: sync.OnceValue(func() *Facts { return NewFacts(doc()) }),
		limit: limit,
	}
}

// AliasLimit reports whether the alias limit applies to the document.
func (s *State) AliasLimit() bool {
	return s.limit
}

// Facts returns the [*Facts] of the document, and finds them on the first
// call. Every caller gets the same Facts, so no caller may change them.
func (s *State) Facts() *Facts {
	return s.facts()
}

// ExcessiveAliasing returns the verdict of count, which reports whether
// aliases make up too much of what a decode of the document reads. The
// first call runs count, and every later call returns its verdict.
func (s *State) ExcessiveAliasing(count func() bool) bool {
	s.aliasOnce.Do(func() {
		s.excessive = count()
	})

	return s.excessive
}

// ExcessiveTextAliasing returns the verdict of count, which reports
// whether aliases make up too much of the text the decoder writes out for
// the document. The first call runs count, and every later call returns
// its verdict. The State keeps it apart from the verdict of
// [State.ExcessiveAliasing].
func (s *State) ExcessiveTextAliasing(count func() bool) bool {
	s.textOnce.Do(func() {
		s.excessiveText = count()
	})

	return s.excessiveText
}
