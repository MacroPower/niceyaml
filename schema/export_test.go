package schema

import (
	"net/http"

	"github.com/goccy/go-yaml/ast"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/paths"
)

// Hooks into the package internals, so the Windows drive-letter handling
// can run on every platform and a test can hand the error path walk a
// tree the parser never builds.
var (
	// FileURLPath exposes fileURLPath to the external test package.
	FileURLPath = fileURLPath

	// HasDriveLetter exposes hasDriveLetter to the external test package.
	HasDriveLetter = hasDriveLetter

	// ReadFile exposes readFile to the external test package.
	ReadFile = readFile

	// CanonicalURL exposes canonicalURL to the external test package.
	CanonicalURL = canonicalURL
)

// HTTPClient returns the client r fetches schemas with.
func HTTPClient(r *Registry) *http.Client {
	return r.client
}

// NormalizeJSON exposes normalizeJSON to the external test package, with
// a member index of its own that follows no alias.
func NormalizeJSON(data any, root ast.Node) any {
	return normalizeJSON(data, root, newMemberIndex(paths.NewResolver(nil)))
}

// SourcePath exposes sourcePath to the external test package, with a
// member index of its own that follows aliases through r.
func SourcePath(root ast.Node, r *paths.Resolver, segments []jsonschema.Segment) paths.Path {
	return sourcePath(root, newMemberIndex(r), segments)
}
