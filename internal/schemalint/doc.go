// Package schemalint reads a compiled JSON schema without a document. The
// schema package and the manifest package lint a schema and the Go type
// that a decode reads with it through this package.
//
// A [Doc] resolves the $ref and $dynamicRef keywords inside one schema
// document. It joins each schema with the schemas whose keywords apply to
// a value along with it. It then reads what the result says about a
// property, such as whether the schema requires or deprecates it.
//
// [RequiredDeprecated] and [UnusedDefaults] return the findings of the
// lints that need only a schema, a Go type, and its decode defaults. Each
// [Finding] names a path and its message. Each caller words the error and
// picks the sentinel that it matches.
package schemalint
