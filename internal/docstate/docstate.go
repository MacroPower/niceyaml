// Package docstate hands the other packages of the module the state that
// package niceyaml keeps for each document behind a Node.
//
// A check that reads the whole document, such as the alias count taken
// before a decode of a node, gives the same result for every Node of
// one document, since no Node edits the tree. Keeping that result on the
// document lets a caller that checks each item of a list pay for the
// document once rather than once per item. Package niceyaml imports this
// package to create a [State] for each document and sets [Of], so a
// package that holds a Node reaches the State without an import cycle.
package docstate

import (
	"sync"

	"go.jacobcolvin.com/niceyaml/paths"
)

// Of returns the [*State] of the document that n belongs to, where n is a
// *niceyaml.Node, or nil for a nil Node. Package niceyaml sets Of when it
// initializes, before any package that imports it runs.
var Of func(n any) *State

// State is the state niceyaml keeps for one document, which every Node of
// the document shares. A State is safe for concurrent use.
//
// Create instances with [New].
type State struct {
	resolver       func() *paths.Resolver
	aliasOnce      sync.Once
	textOnce       sync.Once
	referenceOnce  sync.Once
	excessive      bool
	excessiveText  bool
	referenceAlias bool
}

// New creates a new [*State] whose [State.Resolver] returns the resolver
// that resolver returns.
func New(resolver func() *paths.Resolver) *State {
	return &State{resolver: resolver}
}

// Resolver returns the [*paths.Resolver] that binds the aliases of the
// whole document, which niceyaml creates once for the document.
func (s *State) Resolver() *paths.Resolver {
	return s.resolver()
}

// ExcessiveAliasing returns what count reports for the document. It
// calls count on its first call alone and returns that result from then
// on, so count must depend on the document alone.
func (s *State) ExcessiveAliasing(count func() bool) bool {
	s.aliasOnce.Do(func() {
		s.excessive = count()
	})

	return s.excessive
}

// ExcessiveTextAliasing is [State.ExcessiveAliasing] for the count of
// the document read as text, which the State keeps apart from the
// count [State.ExcessiveAliasing] keeps.
func (s *State) ExcessiveTextAliasing(count func() bool) bool {
	s.textOnce.Do(func() {
		s.excessiveText = count()
	})

	return s.excessiveText
}

// ReferenceAlias returns what find reports for the document, which is
// whether the document holds an alias to a reference document. It calls
// find on its first call alone and returns that result from then on, so
// find must depend on the document alone.
func (s *State) ReferenceAlias(find func() bool) bool {
	s.referenceOnce.Do(func() {
		s.referenceAlias = find()
	})

	return s.referenceAlias
}
