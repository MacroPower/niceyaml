// Package matcher provides strategies for matching YAML documents to schemas.
//
// Matchers determine whether a schema should be applied to a document. They
// are evaluated in registration order by [registry.Registry]; the first matcher
// that returns true wins, and its associated schema is used for validation.
//
// # Matching Strategies
//
// Match documents based on their content using [Content], which extracts a
// value at a YAML path and compares it to an expected string. This is useful
// for schema discrimination based on type fields, version numbers, or other
// identifying markers within the document itself.
//
// Match documents based on their source file using [FilePath], which tests
// the document's file path against a regular expression. This works well for
// directory-based conventions where file location implies schema.
//
// Use [Always] as a fallback matcher at the end of a registry to provide a
// default schema when no other matchers apply.
//
// # Composing Matchers
//
// Combine matchers with [All] (AND) and [Any] (OR) to express complex
// matching conditions. For example, validating Kubernetes resources often
// requires matching both apiVersion and kind:
//
//	apiVersion := paths.Root().Child("apiVersion").Path()
//	kind := paths.Root().Child("kind").Path()
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
//	m := matcher.Func(func(ctx context.Context, doc *niceyaml.DocumentDecoder) bool {
//	    // Custom logic here.
//	    return true
//	})
package matcher
