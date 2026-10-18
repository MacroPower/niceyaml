// Package pathshook holds what the paths package hands to the internal
// packages of niceyaml without adding it to its API.
//
// The paths package sets each variable when it loads, so a package that
// imports both finds every variable set. A parameter that names a type of
// the paths package, such as a *paths.Resolver, has the type any here,
// because the paths package imports this package.
package pathshook

import "github.com/goccy/go-yaml/ast"

var (
	// MappingKeys returns the index of the keys of mapping that r, a
	// *paths.Resolver, keeps for the lookups of its paths. The names map holds
	// the index in mapping of the last entry with each key name, as
	// [paths.Resolver.KeyName] spells keys. It also holds the last `<<` merge
	// key under the name `<<` when mapping does not hold a real key of that
	// name. The merges slice holds the index of each `<<` merge key, in order.
	//
	// The resolver reads the keys of mapping the first time a lookup asks for
	// them, and later lookups share what it read. A caller must not change the
	// map or the slice.
	MappingKeys func(r any, mapping *ast.MappingNode) (names map[string]int, merges []int)

	// UnresolvedAlias returns the alias that err names, when err is the error
	// of a resolve of the paths package at an alias of the document that does
	// not resolve, such as one without an anchor or one that reads a reference
	// document. It reports false for any other error.
	UnresolvedAlias func(err error) (*ast.AliasNode, bool)
)
