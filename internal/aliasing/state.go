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

// State is what one YAML document keeps for the checks of this package.
// It holds whether the alias limit applies to the document, the [Budget]
// of the text its aliases write out, the [Facts] of its anchors and
// aliases, and the result of each count. Every Node of the document
// shares it, so a check of each item of a list counts the document once.
//
// Create instances with [NewState].
type State struct {
	facts     func() *Facts
	aliasErr  error
	textErr   error
	budget    Budget
	aliasOnce sync.Once
	textOnce  sync.Once
	limit     bool
}

// NewState creates a new [*State] for a document. The alias limit
// applies to the document when limit is set, and the text its aliases
// write out must fit budget. The first call of [State.Facts] calls doc
// for the body of the document and the resolver its aliases resolve
// through.
func NewState(limit bool, budget Budget, doc func() (ast.Node, *paths.Resolver)) *State {
	return &State{
		facts:  sync.OnceValue(func() *Facts { return NewFacts(doc()) }),
		budget: budget,
		limit:  limit,
	}
}

// AliasLimit reports whether the alias limit applies to the document.
func (s *State) AliasLimit() bool {
	return s.limit
}

// Budget returns the [Budget] of the text the aliases of the document
// write out.
func (s *State) Budget() Budget {
	return s.budget
}

// Facts returns the [*Facts] of the document, and finds them on the first
// call. Every caller gets the same Facts, so no caller may change them.
func (s *State) Facts() *Facts {
	return s.facts()
}

// ExcessiveAliasing returns the error of count, which checks what a
// decode of the document reads. The first call runs count, and every
// later call returns its error.
func (s *State) ExcessiveAliasing(count func() error) error {
	s.aliasOnce.Do(func() {
		s.aliasErr = count()
	})

	return s.aliasErr
}

// ExcessiveTextAliasing returns the error of count, which checks the text
// the decoder writes out for the document. The first call runs count, and
// every later call returns its error. The State keeps it apart from the
// error of [State.ExcessiveAliasing].
func (s *State) ExcessiveTextAliasing(count func() error) error {
	s.textOnce.Do(func() {
		s.textErr = count()
	})

	return s.textErr
}
