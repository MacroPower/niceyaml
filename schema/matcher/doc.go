// Package matcher provides predicates that decide whether a schema applies
// to a YAML document.
//
// A [Matcher] guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema.When]: the guarded resolver
// names its schema only for documents the matcher accepts and reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] for the rest, so a
// [go.jacobcolvin.com/niceyaml/schema.Registry] moves on to its
// next resolver.
//
// # Matching Strategies
//
// Match documents based on their content using [Content], which decodes
// the value at a YAML path as the type of the value it is given and
// compares the two, so a string matches the text of a field such as kind
// and a number matches a version number however the document spells it.
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
//	apiVersion := paths.Root().Child("apiVersion")
//	kind := paths.Root().Child("kind")
//	m := matcher.All(
//	    matcher.Content(apiVersion, "apps/v1"),
//	    matcher.Content(kind, "Deployment"),
//	)
//
// # Custom Matching Logic
//
// Implement the [Matcher] interface for reusable custom matchers, or use
// [Func] for one-off matching logic that doesn't warrant a separate type:
//
//	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Document) bool {
//	    // Custom logic here.
//	    return true
//	})
package matcher
