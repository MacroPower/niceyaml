// Package yamldecode is a copy of the decoder of go-yaml, which every decode
// of niceyaml runs.
//
// The copy holds go-yaml's decode.go, context.go, struct.go, and
// internal/format at the commit that UPSTREAM_COMMIT names. The sync.sh
// script copies them from a checkout of go-yaml at that commit. It rewrites
// only the package clause and the import paths of go-yaml's internal
// packages, and it ports go-yaml's decoder and fuzz tests. The copy builds
// on the go-yaml module that go.mod requires for the ast, token, and parser
// packages. So the commit of the copy must match the version that go.mod
// requires. LICENSE holds the license of go-yaml.
//
// The files that sync.sh does not write stand in for the parts of go-yaml
// that the copy needs outside those files. The decode options are copies of
// go-yaml's, because go-yaml's options set the unexported fields of
// go-yaml's own Decoder. The Unmarshal and NodeToValue functions are copies
// of go-yaml's too. The comment, map, validator, and unmarshaler types are
// aliases of go-yaml's types. So a type that implements an unmarshaler
// interface of go-yaml decodes itself through the copy. The errors package
// builds go-yaml's error types, so [errors.As] to a
// [*github.com/goccy/go-yaml.TypeError] matches an error of the copy.
//
// A decode of the copy does not call the functions that
// [github.com/goccy/go-yaml.RegisterCustomUnmarshaler] registers, because
// go-yaml keeps them in unexported package state. Give a decode the
// [CustomUnmarshaler] option instead.
package yamldecode
