// Package schemahook holds what the schema package hands to its manifest
// package without adding it to its API.
//
// The schema package sets each variable when it loads, so a package that
// imports both finds every variable set. A parameter that names a type of
// the schema package, such as a *schema.Schema, has the type any here,
// because this package cannot import the schema package.
package schemahook

import (
	"context"

	"go.jacobcolvin.com/x/jsonschema"
)

// Validator returns the validator that s, a *schema.Schema, checks with,
// or nil for a schema that checks nothing, as the schema of a
// "$schema=none" directive does.
var Validator func(s any) *jsonschema.Validator

// offKey is the key of the switch on a context.
type offKey struct{}

// DeprecationsOff returns a copy of ctx under which a schema check reports
// no deprecation. The schema of an older version still checks a document,
// but its deprecated keywords warn nothing, so a load runs that check
// under such a context.
func DeprecationsOff(ctx context.Context) context.Context {
	return context.WithValue(ctx, offKey{}, true)
}

// ReportsDeprecations reports whether a schema check under ctx reports
// its deprecations, which it does unless ctx comes from
// [DeprecationsOff].
func ReportsDeprecations(ctx context.Context) bool {
	off, _ := ctx.Value(offKey{}).(bool) //nolint:errcheck // A missing switch reads as false.

	return !off
}
