// Package matcher provides predicates that decide whether a schema applies
// to a YAML document.
//
// A [Matcher] guards a [go.jacobcolvin.com/niceyaml/schema.Resolver] through
// [go.jacobcolvin.com/niceyaml/schema.When]. The guarded resolver names its
// schema only for documents that the matcher accepts. It reports
// [go.jacobcolvin.com/niceyaml/schema.ErrNoMatch] for the rest, so a
// [go.jacobcolvin.com/niceyaml/schema.Registry] moves on to its next
// resolver. Matchers that cannot decide return an error. The causes
// include a context that ended, an alias in the document that the path
// cannot follow, and a file path pattern without a base directory. The
// registry then stops at that document rather than routing it to a later
// resolver.
//
// # Matching Strategies
//
// Match documents by the scalar at a YAML path using [Content] or
// [Text]. Each scalar has two readings, and they differ when the scalar
// looks like a number. Its text is the string that the document writes,
// so the text of version: 1.10 is "1.10". Its value is what YAML reads
// from that text, so the value of version: 1.10 is the number 1.1.
//
// Fields that name something, such as kind or apiVersion, are text even
// when they look like a number. [Content] compares a string with the
// text, and [Text] passes the text to a function that can test it for a
// prefix or a pattern. [Content] compares a number or a bool with the
// value, so 16 matches replicas: 0x10 and 1.0 matches version: 1. A
// quoted "2" is a string and does not match any number.
//
// Use [Exists] to match documents that hold a node at a path, whatever its
// value, when the presence of a field matters more than its content.
//
// Match documents by their source file using [FilePath], which tests the
// file path of the document against a glob pattern. [FilePath] suits
// directory conventions in which the location of a file implies its
// schema.
//
// A relative pattern such as "configs/*.yaml" applies under a base
// directory. Documents read from disk carry the absolute path of their
// file, so the pattern matches them however the caller spelled that path:
//
//	m := matcher.MustFilePath("configs/*.yaml")
//
//	source, err := niceyaml.NewSourceFromFile("./configs/app.yaml")
//
// The base is the working directory at the time [FilePath] runs.
// [WithBaseDir] names another base, such as the root of a project or the
// directory of a configuration file. Documents that carry a relative
// path, such as those from an [io/fs.FS], match by that path as written.
//
// # Composing Matchers
//
// Combine matchers with [All] (AND) and [Any] (OR) to build conditions from
// several matchers. For example, a schema for a Kubernetes resource applies
// to one pair of apiVersion and kind:
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
// A [Func] makes tests that the other matchers cannot make. It can test
// for a null, a mapping, or a sequence, for a number in a range, or for
// two fields that must agree:
//
//	m := matcher.Func(func(ctx context.Context, doc *niceyaml.Node) (bool, error) {
//	    // Custom logic here.
//	    return true, nil
//	})
//
// Inside the function, select a node with [niceyaml.Node.At] and read it
// with [niceyaml.Node.Decode], which converts the scalar to the Go type
// that it fills. The decoded value can differ from the text and the YAML
// value that [Content] and [Text] read. A decode into a string reads
// version: 1.10 as "1.1", and a decode into an int reads a quoted "2" as
// 2.
//
// Implement the [Matcher] interface for a custom matcher that several
// callers share.
package matcher
