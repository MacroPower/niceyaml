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
// Match documents based on their content using [Content], which extracts a
// value at a YAML path and compares it to an expected string. This is useful
// for schema discrimination based on type fields, version numbers, or other
// identifying markers within the document itself.
//
// Use [Exists] to match documents where a path exists with any non-empty value.
// This is useful when the presence of a field matters more than its specific
// value.
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
