// Package schema provides utilities for working with JSON schemas.
//
// The [JSON] type is an alias for [github.com/invopop/jsonschema.Schema]
// allowing imports from this package instead of the underlying library.
//
// Use [GetProperty] to retrieve schema properties by name, or [MustGetProperty]
// for cases where the property is known to exist.
package schema
