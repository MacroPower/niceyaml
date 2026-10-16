package schema

import (
	"fmt"

	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/httpfetch"
)

// DefaultLoadLimit is the most bytes of a schema document a [Registry]
// reads when [WithLoadLimit] sets no other limit. [Compile] refuses YAML
// of more than this many bytes.
const DefaultLoadLimit = 10 << 20

// WithLoadLimit is a [RegistryOption] that sets the most bytes of each
// schema document the registry reads. The limit covers the file a [Ref]
// from [File] or [FileFS] names, the URL a Ref from [URL] names, and each
// document a $ref names. A document that holds more fails to load with a
// [*LoadError] that holds a [*FetchError] whose cause matches
// [niceyaml.ErrLimit]. The schema is at fault for that error, not the
// document it validates, so [niceyaml.IsInvalid] reports false for it.
//
// The limit also covers the YAML text of each schema the registry
// compiles, such as the bytes of a Ref from [Embedded] or [Loadable].
// YAML text of more than the limit fails its compile with an error that
// matches [ErrCompile] and niceyaml.ErrLimit. The registry compiles JSON
// text from those Refs whatever its size.
//
// The go-yaml decoder takes several hundred times the size of its text
// in memory, so a YAML schema of 10 MiB can take gigabytes. A registry
// for untrusted documents sets a lower limit, as the Untrusted Documents
// section of the package documentation describes:
//
//	reg := schema.NewRegistry(
//	    schema.WithLoadLimit(1<<20),
//	    schema.WithResolvers(schema.Directive()),
//	)
//
// Defaults to [DefaultLoadLimit]. A limit of zero or less keeps the
// default, and a program that wants no limit passes [math.MaxInt]. Given
// more than once, the last option with a positive limit wins.
func WithLoadLimit(n int) RegistryOption {
	return func(c *registryConfig) {
		if n > 0 {
			c.loadLimit = n
		}
	}
}

// LoadError is the error of a [Registry] that could not load or compile
// the schema a [Ref] names. [Registry.Schema] and [Registry.Load] return
// it, and so do [Registry.Lookup] and [Registry.Check], bound to the
// document. It matches [ErrLoad] or [ErrCompile], whichever Phase holds,
// and [errors.Is] and [errors.As] see through it to Err. Its message
// names the schema by [Ref.Name]:
//
//	load schema: "https://example.com/s": fetch https://example.com/s: status 404
//
// A program that shows the error to the author of a document, such as a
// server, can build a message of its own from the fields. The
// [*FetchError] of each document that did not read names the file or URL,
// and for a URL it holds the status of the response:
//
//	fetchErr, ok := errors.AsType[*schema.FetchError](err)
//	if ok && fetchErr.StatusCode == http.StatusNotFound {
//	    return errSchemaNotFound
//	}
type LoadError struct {
	// Err is why the schema did not load or compile. For a schema whose
	// $refs name documents that did not load, Err holds the error of each
	// such document, as [Registry.Schema] describes.
	Err error
	// Phase is [ErrLoad] for a schema that did not load, and [ErrCompile]
	// for one that did not compile.
	Phase error
	// Ref names the schema, as the resolver or the caller gave it. Its
	// [Ref.Key] keeps any password in the userinfo of a URL, and
	// [Ref.Name] replaces the password, as the message does.
	Ref Ref
}

// Error returns the message of Phase, the name of the schema in quotes,
// and the message of Err.
func (e *LoadError) Error() string {
	return fmt.Sprintf("%v: %q: %v", e.Phase, e.Ref.Name(), e.Err)
}

// Is reports whether target is Phase.
func (e *LoadError) Is(target error) bool {
	return target == e.Phase
}

// Unwrap returns Err.
func (e *LoadError) Unwrap() error {
	return e.Err
}

// FetchError is the error of one schema document that did not read. The
// document is a file the registry reads, a URL it fetches, or the
// catalog a [go.jacobcolvin.com/niceyaml/schema/schemastore.Store]
// fetches. A [*LoadError] holds the FetchError of the schema it names,
// or the FetchError of each document a $ref names that did not read.
// [errors.As] finds one inside a join of several too.
//
// The registry refuses some URLs before any fetch, such as a URL that
// does not parse or one that [WithURLPrefixes] does not cover. The error
// of such a URL holds no FetchError. A path the registry refuses before
// any read is a FetchError still, such as an absolute path in a file
// system or a path outside [WithFSAt]. Its Err matches [fs.ErrInvalid].
//
// The message reads as "fetch https://example.com/s.json: status 404"
// for a URL and as "read /srv/schemas/s.json: ..." for a file. A
// document can name a path through its directive, so the message draws
// each control character as a picture and each tab as four spaces. URL
// and Err keep their text as written.
type FetchError struct {
	// Err is why the document did not read. It matches
	// [niceyaml.ErrLimit] for a document past the limit of
	// [WithLoadLimit].
	Err error
	// URL names the document. It holds an HTTP or HTTPS URL with any
	// password replaced by "xxxxx", or the path of a file.
	URL string
	// StatusCode is the HTTP status of the response, or 0 when no
	// response came, as for a file. A response with status 200 OK keeps
	// 200 when its body did not read or held more than the limit.
	StatusCode int
}

// Error returns "fetch" for an HTTP or HTTPS URL and "read" otherwise,
// then the URL and the message of Err.
func (e *FetchError) Error() string {
	verb := "read "
	if httpfetch.IsHTTPURL(e.URL) {
		verb = "fetch "
	}

	if e.Err == nil {
		return escape.Line(verb + e.URL)
	}

	return escape.Line(verb + e.URL + ": " + e.Err.Error())
}

// Unwrap returns Err.
func (e *FetchError) Unwrap() error {
	return e.Err
}

// readError returns the [*FetchError] of the file at name, which did not
// read because of err.
func readError(name string, err error) error {
	return &FetchError{URL: name, Err: err}
}
