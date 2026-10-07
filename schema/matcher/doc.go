// Package matcher provides predicates that decide whether a schema applies
// to a YAML document.
//
// A [Matcher] guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema.When]. The guarded resolver
// names its schema only for documents the matcher accepts and reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] for the rest, so a
// [go.jacobcolvin.com/niceyaml/schema.Registry] moves on to its
// next resolver. A matcher that cannot decide, because its context ended
// or the document holds an alias its path cannot follow, returns an
// error. The registry then stops at that document rather than routing it
// to a resolver further down.
//
// # Matching Strategies
//
// Match documents by the scalar at a YAML path using [Content] or
// [Text]. A scalar has two readings, and they differ for a scalar that
// looks like a number. Its text is the string the document writes, so
// the text of version: 1.10 is "1.10". Its value is what YAML reads from
// that text, which here is the number 1.1.
//
// A field that names something, such as kind or apiVersion, is text even
// when it looks like a number. [Content] compares a string with the
// text, and [Text] hands the text to a function, which tests it for a
// prefix or a pattern. [Content] compares a number or a bool with the
// value, so 16 matches replicas: 0x10 and 1.0 matches version: 1, while
// a quoted "2" is a string and matches no number.
//
// Use [Exists] to match documents that hold a node at a path, whatever its
// value, when the presence of a field matters more than its content.
//
// Match documents based on their source file using [FilePath], which tests
// the document's file path against a glob pattern. This works well for
// directory-based conventions where file location implies schema.
//
// # Composing Matchers
//
// Combine matchers with [All] (AND) and [Any] (OR) to express complex
// matching conditions. For example, validating Kubernetes resources often
// requires matching both apiVersion and kind:
//
//	apiVersion := paths.Doc().Child("apiVersion")
//	kind := paths.Doc().Child("kind")
//	m := matcher.All(
//	    matcher.Content(apiVersion, "apps/v1"),
//	    matcher.Content(kind, "Deployment"),
//	)
//
// # Custom Matching Logic
//
// A [Func] makes a test the matchers above cannot: of a null, of a
// mapping or a sequence, of a number in a range, or of two fields that
// must agree. It selects a node with [niceyaml.Node.At] and reads it
// with [niceyaml.Node.Decode], which converts the scalar to the Go type
// it fills. A decode into a string reads version: 1.10 as "1.1", and a
// decode into an int reads a quoted "2" as 2. A Func thus tests a
// decoded value, where [Content] and [Text] read the text or the YAML
// value:
//
//	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Node) (bool, error) {
//	    // Custom logic here.
//	    return true, nil
//	})
//
// Implement the [Matcher] interface for a custom matcher that several
// callers share.
package matcher
