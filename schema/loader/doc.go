// Package loader provides resolvers that load schema data from a fixed
// source.
//
// Each loader is a [go.jacobcolvin.com/niceyaml/schema.Resolver] that names
// the same schema for every document and never reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch]. Register one directly
// with a [go.jacobcolvin.com/niceyaml/schema/registry.Registry] to validate
// every document against it, or wrap it with
// [go.jacobcolvin.com/niceyaml/schema/registry.When] to apply it only to
// documents a [go.jacobcolvin.com/niceyaml/schema/matcher.Matcher] accepts.
//
// A loader's Resolve call is cheap. It returns a
// [go.jacobcolvin.com/niceyaml/schema.Ref] whose URL identifies the schema
// and whose Load reads the bytes. The registry checks its cache by URL first,
// so a file is read or a URL fetched once per registry, however many
// documents name it.
//
// Embed a schema in the binary with go:embed:
//
//	//go:embed schema.json
//	var schemaBytes []byte
//
//	reg.Register(loader.Embedded("example.com/config/schema.json", schemaBytes))
//
// Read a schema from disk or over HTTP:
//
//	reg.Register(loader.File("./schemas/config.json"))
//	reg.Register(loader.URL("https://example.com/schema.json"))
//
// Route a reference as written in a directive or on a command line, which
// may be a file path or a URL, with [FileOrURL].
package loader
